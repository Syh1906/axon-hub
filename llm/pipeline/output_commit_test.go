package pipeline

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/streams"
)

func TestOutputCommitWaitsPastReasoningAndStartup(t *testing.T) {
	reasoning := &llm.Response{Choices: []llm.Choice{{Delta: &llm.Message{ReasoningContent: lo.ToPtr("thinking"), ReasoningSignature: lo.ToPtr("opaque")}}}}
	text := &llm.Response{Choices: []llm.Choice{{Delta: &llm.Message{Content: llm.MessageContent{Content: lo.ToPtr("title")}}}}}
	require.False(t, hasResponseOutput(&llm.Response{}))
	require.False(t, hasResponseOutput(reasoning))
	require.True(t, hasResponseOutput(text))
	p := &pipeline{streamOutputCommit: true}
	stream, err := p.preReadLlmStream(t.Context(), streams.SliceStream([]*llm.Response{{}, reasoning, text, llm.DoneResponse}), nil)
	require.NoError(t, err)
	var events []*llm.Response
	for stream.Next() {
		events = append(events, stream.Current())
	}
	require.NoError(t, stream.Close())
	require.Len(t, events, 4, "buffered reasoning and startup events are preserved for the winning attempt")
	_, err = p.preReadLlmStream(t.Context(), streams.SliceStream([]*llm.Response{reasoning, llm.DoneResponse}), nil)
	require.ErrorIs(t, err, ErrEmptyResponse, "reasoning-only completion cannot win a title race")
}
