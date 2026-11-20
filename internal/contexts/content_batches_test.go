package contexts

import (
	"testing"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
)

func TestContentBatchEvidence(t *testing.T) {
	test := func(format llm.APIFormat, frames ...string) (*int, *int64) {
		var b ContentBatches
		for i, data := range frames {
			b.Observe(format, &httpclient.StreamEvent{Data: []byte(data)}, time.UnixMilli(int64(i)*10))
		}
		return b.Result()
	}
	t.Run("one argument delta and repeated completion events", func(t *testing.T) {
		n, span := test(llm.APIFormatOpenAIResponse,
			`{"type":"response.created"}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","delta":"{}"}`,
			`{"type":"response.function_call_arguments.done","arguments":"{}"}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","arguments":"{}"}}`,
			`{"type":"response.completed","response":{"output":[{"type":"function_call","arguments":"{}"}]}}`)
		require.Equal(t, 1, *n)
		require.Zero(t, *span)
	})
	t.Run("reasoning and terminal-only answer", func(t *testing.T) {
		n, span := test(llm.APIFormatOpenAIResponseWebSocket,
			`{"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
			`{"type":"response.reasoning_summary_text.done","text":"thinking"}`,
			`{"type":"response.completed","response":{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]},{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}}`)
		require.Equal(t, 2, *n)
		require.EqualValues(t, 20, *span)
	})
	t.Run("chat fields in one frame", func(t *testing.T) {
		n, _ := test(llm.APIFormatOpenAIChatCompletion,
			`{"choices":[{"delta":{"content":"a","tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
			`{"choices":[{"message":{"content":"a","tool_calls":[{"function":{"arguments":"{}"}}]}}]}`)
		require.Equal(t, 1, *n)
	})
	for _, reasoning := range []string{
		`{"reasoning_details":[{"type":"reasoning.text","text":"plan"}]}`,
		`{"reasoning_text":"plan"}`,
	} {
		n, span := test(llm.APIFormatOpenAIChatCompletion,
			`{"choices":[{"delta":`+reasoning+`}]}`,
			`{"choices":[{"delta":{"content":"answer"}}]}`)
		require.NotNil(t, n)
		require.Equal(t, 2, *n)
		require.EqualValues(t, 10, *span)
	}
	t.Run("reasoning aliases in a repeated final chat message", func(t *testing.T) {
		n, span := test(llm.APIFormatOpenAIChatCompletion,
			`{"choices":[{"delta":{"reasoning":"plan","reasoning_details":[{"text":"pl"},{"text":"an"}]}}]}`,
			`{"choices":[{"message":{"reasoning_text":"plan"}}]}`)
		require.NotNil(t, n)
		require.Equal(t, 1, *n)
		require.Zero(t, *span)
	})
	t.Run("Responses events on Copilot chat endpoint", func(t *testing.T) {
		n, span := test(llm.APIFormatOpenAIChatCompletion,
			`{"type":"response.reasoning_summary_text.delta","delta":"plan"}`,
			`{"type":"response.output_text.delta","delta":"answer"}`)
		require.NotNil(t, n)
		require.Equal(t, 2, *n)
		require.EqualValues(t, 10, *span)
	})
	t.Run("no content", func(t *testing.T) {
		n, span := test(llm.APIFormatAnthropicMessage, `{"type":"message_stop"}`)
		require.Nil(t, n)
		require.Nil(t, span)
	})
}
