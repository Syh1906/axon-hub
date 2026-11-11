package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/gemini"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestConversionRejectsUnrepresentedResponsesTools(t *testing.T) {
	for _, body := range []string{
		`{"model":"synthetic","input":"hello","tools":[{"type":"file_search","vector_store_ids":["vs_test"]}]}`,
		`{"model":"synthetic","input":"hello","tools":[{"type":"mcp","server_label":"test","server_url":"https://example.invalid"}]}`,
		`{"model":"synthetic","input":"hello","tools":[{"type":"namespace","name":"tools","tools":[{"type":"function","name":"lookup"},{"type":"custom","name":"exec"}]}]}`,
		`{"model":"synthetic","input":"hello","conversation":"conv_1"}`,
		`{"model":"synthetic","previous_response_id":"resp_1","input":[{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]}`,
	} {
		req, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body)})
		require.NoError(t, err)
		chat, err := openai.NewOutboundTransformer("https://example.invalid", "synthetic")
		require.NoError(t, err)
		claude, err := anthropic.NewOutboundTransformer("https://api.anthropic.com", "synthetic")
		require.NoError(t, err)
		google, err := gemini.NewOutboundTransformer("https://example.invalid", "synthetic")
		require.NoError(t, err)
		for _, out := range []transformer.Outbound{chat, claude, google} {
			_, err := out.TransformRequest(t.Context(), req)
			require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
			require.ErrorIs(t, err, transformer.ErrInvalidRequest)
		}
		native, err := responses.NewOutboundTransformer("https://example.invalid", "synthetic")
		require.NoError(t, err)
		_, err = native.TransformRequest(t.Context(), req)
		require.NoError(t, err)
	}
}

func TestConversionFailureDoesNotExecuteUpstream(t *testing.T) {
	out, err := openai.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	executor := pipeline.NewMockExecutor(t)
	pipe := pipeline.NewFactory(executor).Pipeline(responses.NewInboundTransformer(), out)
	_, err = pipe.Process(t.Context(), &httpclient.Request{Body: []byte(`{"model":"synthetic","input":"search","tools":[{"type":"file_search","vector_store_ids":["vs_test"]}]}`)})
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	// No Do/DoStream expectation: any provider execution fails this test.
}

func TestConversionAllowsFunctionNamespace(t *testing.T) {
	req, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"synthetic","input":"hello","tools":[{"type":"namespace","name":"tools","tools":[{"type":"function","name":"lookup"}]}]}`)})
	require.NoError(t, err)
	require.Empty(t, llm.ResponsesOnlyFeature(req))
	chat, err := openai.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	_, err = chat.TransformRequest(t.Context(), req)
	require.NoError(t, err)
}

func TestConversionRequirementsStayScoped(t *testing.T) {
	for _, tc := range []struct {
		tool      string
		supported llm.APIFormat
		rejected  llm.APIFormat
	}{
		{llm.ToolTypeWebSearch, llm.APIFormatAnthropicMessage, llm.APIFormatOpenAIChatCompletion},
		{llm.ToolTypeGoogleCodeExecution, llm.APIFormatGeminiContents, llm.APIFormatOpenAIResponse},
		{llm.ToolTypeImageGeneration, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage},
	} {
		req := &llm.Request{Tools: []llm.Tool{{Type: tc.tool}}}
		require.Empty(t, llm.UnsupportedRequestFeature(req, tc.supported))
		require.Equal(t, tc.tool, llm.UnsupportedRequestFeature(req, tc.rejected))
	}
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents, llm.APIFormatOpenAIResponse, llm.APIFormatOllamaChat} {
		require.Empty(t, llm.UnsupportedRequestFeature(&llm.Request{Tools: []llm.Tool{{Type: llm.ToolTypeFunction}}}, format))
	}
}

func TestConversionPreservesStructuredOutputAcrossProtocols(t *testing.T) {
	const schema = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`
	const body = `{"model":"synthetic","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"output_config":{"format":{"type":"json_schema","schema":` + schema + `}}}`
	req, err := anthropic.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
	require.NoError(t, err)
	chat, err := openai.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	resp, err := responses.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	google, err := gemini.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	claude, err := anthropic.NewOutboundTransformer("https://api.anthropic.com", "synthetic")
	require.NoError(t, err)
	for _, tc := range []struct {
		out  transformer.Outbound
		path string
	}{
		{chat, "response_format.json_schema.schema"}, {resp, "text.format.schema"}, {google, "generationConfig.responseJsonSchema"}, {claude, "output_config.format.schema"},
	} {
		wire, err := tc.out.TransformRequest(t.Context(), req)
		require.NoError(t, err)
		require.JSONEq(t, schema, gjson.GetBytes(wire.Body, tc.path).Raw)
	}
}

func TestConversionPreservesAnthropicServerToolHistoryBoundary(t *testing.T) {
	const body = `{"model":"synthetic","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"srv_1","name":"code_execution","input":{"code":"print(1)"}},{"type":"code_execution_tool_result","tool_use_id":"srv_1","content":{"type":"code_execution_result","stdout":"1"}}]},{"role":"user","content":"continue"}]}`
	req, err := anthropic.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Headers: map[string][]string{"Content-Type": {"application/json"}}})
	require.NoError(t, err)
	data, err := json.Marshal(req)
	require.NoError(t, err)
	req = &llm.Request{}
	require.NoError(t, json.Unmarshal(data, req))
	chat, err := openai.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	resp, err := responses.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	google, err := gemini.NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	for _, out := range []transformer.Outbound{chat, resp, google} {
		_, err := out.TransformRequest(t.Context(), req)
		require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	}
	native, err := anthropic.NewOutboundTransformer("https://api.anthropic.com", "synthetic")
	require.NoError(t, err)
	wire, err := native.TransformRequest(t.Context(), req)
	require.NoError(t, err)
	require.JSONEq(t, gjson.Get(body, "messages.0.content").Raw, gjson.GetBytes(wire.Body, "messages.0.content").Raw)
}
