package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

func TestNativeRequestFieldsRoundTrip(t *testing.T) {
	const body = `{"model":"claude-sonnet-5-5","max_tokens":2048,"messages":[{"role":"user","content":"hello"}],"container":"container_1","thinking":{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}},"output_config":{"effort":"high","format":{"type":"json_schema","schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`
	req, err := NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
	require.NoError(t, err)
	// Metadata must remain readable after storing/restoring unified requests.
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var restored llm.Request
	require.NoError(t, json.Unmarshal(data, &restored))
	out, err := NewOutboundTransformer("https://api.anthropic.com", "synthetic")
	require.NoError(t, err)
	wire, err := out.TransformRequest(t.Context(), &restored)
	require.NoError(t, err)
	for _, field := range []string{"output_config.format", "thinking.block_binding", "container"} {
		require.JSONEq(t, gjson.Get(body, field).Raw, gjson.GetBytes(wire.Body, field).Raw, field)
	}
	require.Contains(t, wire.Headers.Values("Anthropic-Beta"), "thinking-binding-controls-2026-08-01")
}

func TestNativeUnmappedToolsRoundTripAndBoundary(t *testing.T) {
	const body = `{"model":"synthetic","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}},{"type":"code_execution_20250825","name":"code_execution","vendor_option":{"keep":true}},{"type":"web_search_20260209","name":"web_search","max_uses":2}],"tool_choice":{"type":"tool","name":"code_execution","disable_parallel_tool_use":true}}`
	req, err := NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
	require.NoError(t, err)
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var restored llm.Request
	require.NoError(t, json.Unmarshal(data, &restored))
	out, err := NewOutboundTransformer("https://api.anthropic.com", "synthetic")
	require.NoError(t, err)
	wire, err := out.TransformRequest(t.Context(), &restored)
	require.NoError(t, err)
	for _, field := range []string{"tools", "tool_choice"} {
		require.JSONEq(t, gjson.Get(body, field).Raw, gjson.GetBytes(wire.Body, field).Raw)
	}
	chat, err := openai.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	_, err = chat.TransformRequest(t.Context(), &restored)
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	require.True(t, llm.HasConversionRequirements(&restored))
}

func TestNativeToolOnlyRequestAndCacheControl(t *testing.T) {
	const body = `{"model":"synthetic","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"code_execution_20250825","name":"code_execution","cache_control":{"type":"ephemeral"},"vendor_option":true}],"tool_choice":{"type":"tool","name":"code_execution","disable_parallel_tool_use":true}}`
	req, err := NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
	require.NoError(t, err)
	out, err := NewOutboundTransformer("https://api.anthropic.com", "synthetic")
	require.NoError(t, err)
	wire, err := out.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	for _, field := range []string{"tools", "tool_choice"} {
		require.JSONEq(t, gjson.Get(body, field).Raw, gjson.GetBytes(wire.Body, field).Raw)
	}
	unsupported, err := NewOutboundTransformerWithConfig(&Config{Type: PlatformLongCat, BaseURL: "https://example.invalid"})
	require.NoError(t, err)
	_, err = unsupported.TransformRequest(t.Context(), req)
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	var tool Tool
	require.NoError(t, json.Unmarshal([]byte(gjson.Get(body, "tools.0").Raw), &tool))
	// The existing cache optimizer can remove a breakpoint without raw replay
	// restoring the removed marker or losing unrelated native options.
	tool.CacheControl = nil
	encoded, err := json.Marshal(tool)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(encoded, "cache_control").Exists())
	require.True(t, gjson.GetBytes(encoded, "vendor_option").Bool())
}

func TestNativeResponseStateRoundTrip(t *testing.T) {
	out, err := NewOutboundTransformer("https://api.anthropic.com", "synthetic")
	require.NoError(t, err)
	for _, reason := range []string{"pause_turn", "stop_sequence", "model_context_window_exceeded", "end_turn", "refusal"} {
		t.Run(reason, func(t *testing.T) {
			body := `{"id":"m1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"text","text":"continue"}],"stop_reason":"` + reason + `","stop_sequence":"END","container":{"id":"container_1"},"usage":{"input_tokens":1,"output_tokens":1}}`
			resp, err := out.TransformResponse(t.Context(), &httpclient.Response{Body: []byte(body)})
			require.NoError(t, err)
			data, err := json.Marshal(resp)
			require.NoError(t, err)
			var restored llm.Response
			require.NoError(t, json.Unmarshal(data, &restored))
			wire, err := NewInboundTransformer().TransformResponse(t.Context(), &restored)
			require.NoError(t, err)
			require.Equal(t, reason, gjson.GetBytes(wire.Body, "stop_reason").String())
			require.Equal(t, "END", gjson.GetBytes(wire.Body, "stop_sequence").String())
			require.Equal(t, "container_1", gjson.GetBytes(wire.Body, "container.id").String())
		})
	}
}

func TestNativeNullContainerDoesNotRestrictConversion(t *testing.T) {
	const body = `{"model":"synthetic","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"container":null}`
	req, err := NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
	require.NoError(t, err)
	require.False(t, llm.HasConversionRequirements(req))
	require.Empty(t, llm.UnsupportedRequestFeature(req, llm.APIFormatOpenAIChatCompletion))
}

func TestNativePausedStreamRoundTrip(t *testing.T) {
	out, err := NewOutboundTransformer("https://api.anthropic.com", "synthetic")
	require.NoError(t, err)
	events := []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"message_start","message":{"id":"m1","model":"claude-sonnet-5-5","role":"assistant","content":[],"container":{"id":"container_1"},"usage":{"input_tokens":1,"output_tokens":0}}}`)},
		{Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"continue"}}`)},
		{Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"pause_turn"},"usage":{"output_tokens":1}}`)},
		{Data: []byte(`{"type":"message_stop"}`)},
	}
	for _, event := range events {
		event.Type = gjson.GetBytes(event.Data, "type").String()
	}
	unified, err := out.TransformStream(t.Context(), nil, streams.SliceStream(events))
	require.NoError(t, err)
	wire, err := NewInboundTransformer().TransformStream(t.Context(), unified)
	require.NoError(t, err)
	defer wire.Close()
	chunks, err := streams.All(wire)
	require.NoError(t, err)
	var reason, container string
	for _, chunk := range chunks {
		if gjson.GetBytes(chunk.Data, "type").String() == "message_delta" {
			reason = gjson.GetBytes(chunk.Data, "delta.stop_reason").String()
		}
		if gjson.GetBytes(chunk.Data, "type").String() == "message_start" {
			container = gjson.GetBytes(chunk.Data, "message.container.id").String()
		}
	}
	require.Equal(t, "pause_turn", reason)
	require.Equal(t, "container_1", container)
	aggregated, _, err := AggregateStreamChunks(t.Context(), events, PlatformDirect)
	require.NoError(t, err)
	require.Equal(t, "pause_turn", gjson.GetBytes(aggregated, "stop_reason").String())
	require.Equal(t, "container_1", gjson.GetBytes(aggregated, "container.id").String())
}

func TestNativeStopSequenceSurvivesUsageDelta(t *testing.T) {
	events := []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"message_start","message":{"id":"m1","type":"message","model":"synthetic","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)},
		{Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"stop_sequence","stop_sequence":"END"}}`)},
		{Data: []byte(`{"type":"message_delta","delta":{},"usage":{"output_tokens":1}}`)},
		{Data: []byte(`{"type":"message_stop"}`)},
	}
	aggregated, _, err := AggregateStreamChunks(t.Context(), events, PlatformDirect)
	require.NoError(t, err)
	require.Equal(t, "stop_sequence", gjson.GetBytes(aggregated, "stop_reason").String())
	require.Equal(t, "END", gjson.GetBytes(aggregated, "stop_sequence").String())
	require.Equal(t, int64(1), gjson.GetBytes(aggregated, "usage.output_tokens").Int())
}
