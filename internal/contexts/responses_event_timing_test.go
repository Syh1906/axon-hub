package contexts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestSemanticProgressIsIndependentOfDeliveredContent(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket} {
		_, clock := WithRequestClock(t.Context(), time.Now())
		attempt := clock.AddAttempt(1, 2, nil)
		for _, frame := range []string{`{"type":"response.created"}`, `{"type":"response.queued"}`,
			`{"type":"response.output_text.delta","delta":""}`, `{"type":"unknown"}`, `{"type":"error"}`,
			`{"type":"response.completed","response":{"output":[],"usage":{"output_tokens":3}}}`} {
			attempt.ObserveOutputProgress(format, &httpclient.StreamEvent{Data: []byte(frame)})
		}
		require.Nil(t, clock.Snapshot().Attempts[0].Semantic)
		start := &httpclient.StreamEvent{Data: []byte(`{"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}`)}
		attempt.ObserveOutputProgress(format, start)
		clock.WroteResponsesEvent(start)
		require.Equal(t, "response.output_item.added.reasoning", clock.Snapshot().Attempts[0].Semantic.Type)
		require.Equal(t, clock.Snapshot().Attempts[0].Semantic.At, attempt.ProtocolFirstOutput())
		require.Nil(t, clock.Snapshot().FirstWriteContent)
		clock.WroteResponsesEvent(&httpclient.StreamEvent{Data: []byte(`{"type":"response.function_call_arguments.delta","delta":"{}"}`)})
		require.Nil(t, clock.Snapshot().FirstAnswer)
		clock.WroteResponsesEvent(&httpclient.StreamEvent{Data: []byte(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`)})
		require.NotNil(t, clock.Snapshot().FirstAnswer)
		require.Equal(t, "response.function_call_arguments.delta", clock.Snapshot().FirstWriteContent.Type)
	}
}

func TestProtocolFirstOutputSkipsEmptyTextStarts(t *testing.T) {
	for _, tc := range []struct {
		format      llm.APIFormat
		start, next string
	}{
		{llm.APIFormatOpenAIResponse, `{"type":"response.output_item.added","item":{"type":"message","content":[]}}`,
			`{"type":"response.output_text.delta","delta":"hello"}`},
		{llm.APIFormatAnthropicMessage, `{"type":"content_block_start","content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`},
		{llm.APIFormatOpenAIResponseWebSocket, `{"type":"response.output_item.added","item":{"type":"message","content":[]}}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","name":"lookup"}}`},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			_, clock := WithRequestClock(t.Context(), time.Now())
			attempt := clock.AddAttempt(1, 2, nil)
			attempt.ObserveOutputProgress(tc.format, &httpclient.StreamEvent{Data: []byte(tc.start)})
			start := clock.Snapshot().Attempts[0].Semantic
			require.NotNil(t, start)
			require.True(t, attempt.ProtocolFirstOutput().IsZero())
			attempt.ObserveOutputProgress(tc.format, &httpclient.StreamEvent{Data: []byte(tc.next)})
			require.False(t, attempt.ProtocolFirstOutput().IsZero())
			require.Same(t, start, clock.Snapshot().Attempts[0].Semantic)
		})
	}
}

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

func TestGeminiToolStartAndMixedAnswer(t *testing.T) {
	_, clock := WithRequestClock(t.Context(), time.Now())
	attempt := clock.AddAttempt(1, 2, nil)
	frame := func(parts string) *httpclient.StreamEvent {
		return &httpclient.StreamEvent{Data: []byte(`{"candidates":[{"content":{"parts":[` + parts + `]}}]}`)}
	}
	start := frame(`{"functionCall":{"name":"tool"}}`)
	attempt.ObserveOutputProgress(llm.APIFormatGeminiContents, start)
	clock.WroteStreamContent(llm.APIFormatGeminiContents, start)
	require.Equal(t, "gemini.function_call", clock.Snapshot().Attempts[0].Semantic.Type)
	require.Nil(t, clock.Snapshot().FirstWriteContent)
	args := frame(`{"functionCall":{"name":"tool","args":{"n":1}}}`)
	clock.WroteStreamContent(llm.APIFormatGeminiContents, args)
	require.Equal(t, "gemini.function_call", clock.Snapshot().FirstWriteContent.Type)
	_, other := WithRequestClock(t.Context(), time.Now())
	other.WroteStreamContent(llm.APIFormatGeminiContents, frame(`{"thought":true,"text":"think"},{"text":"answer"}`))
	require.Equal(t, "gemini.thought", other.Snapshot().FirstWriteContent.Type)
	require.NotNil(t, other.Snapshot().FirstAnswer)
}

func TestAnswerDeliveryCountsOnlyNewTextAndMatchedTokenBatches(t *testing.T) {
	_, clock := WithRequestClock(t.Context(), time.Now())
	write := func(frame string) { clock.WroteResponsesEvent(&httpclient.StreamEvent{Data: []byte(frame)}) }
	write(`{"type":"response.reasoning_text.delta","delta":"thinking"}`)
	write(`{"type":"response.output_text.delta","delta":"ab","logprobs":[{"token":"a"},{"token":"b"}]}`)
	write(`{"type":"response.output_text.done","text":"ab"}`)
	write(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"ab"}]}]}}`)
	snapshot := clock.Snapshot()
	require.True(t, snapshot.SingleAnswerBatch)
	require.Nil(t, snapshot.AnswerTokensPerSecond)

	clockRate := answerDelivery{}
	at := time.Now()
	clockRate.observe(gjson.Parse(`"ab"`), gjson.Parse(`[{"token":"a"},{"token":"b"}]`), 0, 0, false, at)
	clockRate.observe(gjson.Parse(`"cde"`), gjson.Parse(`[{"token":"c"},{"token":"d"},{"token":"e"}]`), 0, 0, false, at.Add(2*time.Second))
	single, rate := clockRate.result()
	require.False(t, single)
	require.NotNil(t, rate)
	require.Equal(t, 1.5, *rate)
	clockRate.observe(gjson.Parse(`"f"`), gjson.Parse(`[{"token":"wrong"}]`), 0, 0, false, at.Add(3*time.Second))
	_, rate = clockRate.result()
	require.Nil(t, rate)

	for _, tc := range []struct {
		format llm.APIFormat
		frame  string
	}{
		{llm.APIFormatAnthropicMessage, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
		{llm.APIFormatOpenAIChatCompletion, `{"choices":[{"index":0,"delta":{"content":"hi"}}]}`},
		{llm.APIFormatGeminiContents, `{"candidates":[{"content":{"parts":[{"thought":true,"text":"thinking"},{"text":"hi"}]}}]}`},
	} {
		_, c := WithRequestClock(t.Context(), at)
		c.WroteStreamContent(tc.format, &httpclient.StreamEvent{Data: []byte(tc.frame)})
		require.True(t, c.Snapshot().SingleAnswerBatch)
		c.WroteStreamContent(tc.format, &httpclient.StreamEvent{Data: []byte(tc.frame)})
		require.False(t, c.Snapshot().SingleAnswerBatch)
		require.Nil(t, c.Snapshot().AnswerTokensPerSecond)
	}
}
