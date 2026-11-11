package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

type staticAnthropicCandidateSelector []*ChannelModelsCandidate

func (s staticAnthropicCandidateSelector) Select(context.Context, *llm.Request) ([]*ChannelModelsCandidate, error) {
	return s, nil
}

func TestAnthropicNativeToolsSelector_UnmappedToolsRequireCapableProvider(t *testing.T) {
	req, err := anthropic.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    []byte(`{"model":"synthetic","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"code_execution_20250825","name":"code_execution"}]}`),
	})
	require.NoError(t, err)
	unsupported := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeMoonshotAnthropic}}}
	vertex := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeAnthropicGcp}}}
	direct := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeAnthropic}}}
	selector := WithAnthropicNativeToolsSelector(staticAnthropicCandidateSelector{unsupported, vertex, direct})
	got, err := selector.Select(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, []*ChannelModelsCandidate{direct}, got)
	selector = WithAnthropicNativeToolsSelector(staticAnthropicCandidateSelector{unsupported, vertex})
	got, err = selector.Select(t.Context(), req)
	require.NoError(t, err)
	require.Empty(t, got)
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatGeminiContents} {
		mapped := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenai}}, APIFormat: format.String()}
		selector = WithAnthropicNativeToolsSelector(staticAnthropicCandidateSelector{unsupported, vertex, mapped})
		// A known web-search mapping is valid even when no native Anthropic
		// provider is present; unknown native tools must still stay excluded.
		search := &llm.Request{APIFormat: llm.APIFormatAnthropicMessage, Tools: []llm.Tool{{Type: llm.ToolTypeWebSearch}}}
		got, err = selector.Select(t.Context(), search)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Same(t, mapped, got[0])
		got, err = selector.Select(t.Context(), req)
		require.NoError(t, err)
		require.Empty(t, got)
	}
}

func TestAnthropicNativeToolsSelector_Select_PreservesDeepSeek(t *testing.T) {
	newCandidate := func(channelType channel.Type) *ChannelModelsCandidate {
		return &ChannelModelsCandidate{
			Channel: &biz.Channel{Channel: &ent.Channel{Type: channelType}},
		}
	}

	direct := newCandidate(channel.TypeAnthropic)
	deepseek := newCandidate(channel.TypeDeepseekAnthropic)
	unsupported := newCandidate(channel.TypeMoonshotAnthropic)
	selector := WithAnthropicNativeToolsSelector(staticAnthropicCandidateSelector{
		direct,
		deepseek,
		unsupported,
	})

	got, err := selector.Select(context.Background(), &llm.Request{
		APIFormat: llm.APIFormatAnthropicMessage,
		Tools:     []llm.Tool{{Type: llm.ToolTypeWebSearch}},
	})
	require.NoError(t, err)
	require.Equal(t, []*ChannelModelsCandidate{direct, deepseek}, got)
}
