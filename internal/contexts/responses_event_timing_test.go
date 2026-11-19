package contexts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestResponsesEventClocksDistinguishStructureAndDeliveredDelta(t *testing.T) {
	_, clock := WithRequestClock(t.Context(), time.Now())
	a := clock.AddAttempt(1, 2, nil)
	for _, event := range []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.created"}`)},
		{Type: "response.output_text.delta", Data: []byte(`{"delta":""}`)},
		{Type: "untrusted-custom-type", Data: []byte(`{}`)},
	} {
		a.ObserveResponsesEvent(event)
		clock.WroteResponsesEvent(event)
	}
	s := clock.Snapshot()
	require.Equal(t, "response.created", s.Attempts[0].FirstEvent.Type)
	require.Nil(t, s.Attempts[0].FirstDelta)
	require.Nil(t, s.FirstWriteDelta)
	event := &httpclient.StreamEvent{Data: []byte(`{"type":"response.function_call_arguments.delta","delta":"fragment"}`)}
	a.ObserveResponsesEvent(event)
	require.Nil(t, clock.Snapshot().FirstWriteDelta)
	clock.WroteResponsesEvent(event)
	later := &httpclient.StreamEvent{Type: "response.output_text.delta", Data: []byte(`{"delta":"later"}`)}
	a.ObserveResponsesEvent(later)
	clock.WroteResponsesEvent(later)
	s = clock.Snapshot()
	require.Equal(t, "response.function_call_arguments.delta", s.Attempts[0].FirstDelta.Type)
	require.Equal(t, s.Attempts[0].FirstDelta.Type, s.FirstWriteDelta.Type)
	require.False(t, s.FirstWriteDelta.At.Before(s.Attempts[0].FirstDelta.At))
	require.Equal(t, len(event.Data), s.FirstWriteDelta.Bytes)
}

func TestResponsesWholeItemPrecedesLaterWrittenDelta(t *testing.T) {
	_, clock := WithRequestClock(t.Context(), time.Now())
	clock.WroteResponsesEvent(&httpclient.StreamEvent{Data: []byte(`{"type":"response.completed","response":{"output":[]}}`)})
	require.Nil(t, clock.Snapshot().FirstWriteContent)
	e := &httpclient.StreamEvent{Data: []byte(`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"done"}]}}`)}
	clock.WroteResponsesEvent(e)
	s := clock.Snapshot()
	require.Nil(t, s.FirstWriteDelta)
	require.Equal(t, "response.output_item.done", s.FirstWriteContent.Type)
	clock.WroteResponsesEvent(&httpclient.StreamEvent{Data: []byte(`{"type":"response.output_text.delta","delta":"later"}`)})
	require.Equal(t, s.FirstWriteContent, clock.Snapshot().FirstWriteContent)
}

func TestFirstWrittenContentFollowsClientProtocol(t *testing.T) {
	for _, tc := range []struct {
		f                 llm.APIFormat
		setup, body, kind string
	}{
		{llm.APIFormatOpenAIChatCompletion, `{"choices":[{"delta":{"role":"assistant"}}]}`, `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`, "chat.tool"},
		{llm.APIFormatAnthropicMessage, `{"type":"message_start","message":{"usage":{"input_tokens":3}}}`, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"x"}}`, "anthropic.thinking"},
		{llm.APIFormatGeminiContents, `{"candidates":[{"finishReason":"STOP"}]}`, `{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`, "gemini.text"},
		{llm.APIFormatOpenAIChatCompletion, `{"choices":[{"delta":{"function_call":{"arguments":{}}}}]}`, `{"choices":[{"delta":{"function_call":{"arguments":"{}"}}}]}`, "chat.tool"},
		{llm.APIFormatAnthropicMessage, `{"type":"content_block_start","content_block":{"type":"tool_use","input":{}}}`, `{"type":"content_block_start","content_block":{"type":"tool_use","input":{"x":1}}}`, "anthropic.tool"},
		{llm.APIFormatOpenAIResponse, `{"type":"response.output_text.done","text":{}}`, `{"type":"response.incomplete","response":{"output":[{"type":"message","content":[{"text":"partial"}]}]}}`, "response.incomplete"},
	} {
		t.Run(string(tc.f), func(t *testing.T) {
			ctx, c := WithRequestClock(WithStreamFormat(t.Context(), tc.f), time.Now())
			c.WroteStreamContent(StreamFormat(ctx), &httpclient.StreamEvent{Data: []byte(tc.setup)})
			require.Nil(t, c.Snapshot().FirstWriteContent)
			c.WroteStreamContent(StreamFormat(ctx), &httpclient.StreamEvent{Data: []byte(tc.body)})
			require.Equal(t, tc.kind, c.Snapshot().FirstWriteContent.Type)
			require.Nil(t, c.Snapshot().FirstWriteDelta)
		})
	}
}
