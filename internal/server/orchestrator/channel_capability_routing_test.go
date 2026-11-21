package orchestrator

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAdaptedCapabilityRouting(t *testing.T) {
	ch := &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenai, Settings: &objects.ChannelSettings{
		AdaptedCapabilities:      []string{"responses/image_generation", "responses/web_search", "openai/alpha_search"},
		DisabledEndpoints:        []string{"openai/responses", "openai/alpha_search"},
		AllowImageGenerationTool: lo.ToPtr(true), AllowWebSearchTool: lo.ToPtr(false),
	}, Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/alpha_search"}}}}
	selectFormat := func(req *llm.Request) string {
		f, _ := selectChannelAPIFormat(t.Context(), ch, nil, req, "fixture")
		return f
	}
	chat := &llm.Request{RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIResponse}
	require.NotEqual(t, "openai/responses", selectFormat(chat))
	chat.Tools = []llm.Tool{{Type: llm.ToolTypeImageGeneration}}
	require.Equal(t, "openai/responses", selectFormat(chat))
	chat.Tools = []llm.Tool{{Type: llm.ToolTypeWebSearch}}
	require.Empty(t, selectFormat(chat))
	search := &llm.Request{RequestType: llm.RequestTypeAlphaSearch, APIFormat: llm.APIFormatOpenAIAlphaSearch}
	require.Empty(t, selectFormat(search))
	ch.Settings.DisabledEndpoints = []string{"openai/responses"}
	require.Equal(t, "openai/alpha_search", selectFormat(search))
}

func TestDisabledProbeOnlyBlocksItsUpstreamFormat(t *testing.T) {
	ch := &biz.Channel{Channel: &ent.Channel{Type: channel.TypeGemini,
		Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/responses"}, {APIFormat: "gemini/embeddings"}},
		Settings:  &objects.ChannelSettings{DisabledEndpoints: []string{"openai/embeddings"}, AllowWebSearchTool: lo.ToPtr(false)}}}
	req := &llm.Request{APIFormat: llm.APIFormatOpenAIEmbedding, RequestType: llm.RequestTypeEmbedding}
	format, _ := selectChannelAPIFormat(t.Context(), ch, nil, req, "fixture")
	require.Equal(t, "gemini/embeddings", format)
	for _, tool := range []string{llm.ToolTypeWebSearch, llm.ToolTypeGoogleSearch} {
		req = &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, RequestType: llm.RequestTypeChat, Tools: []llm.Tool{{Type: tool}}}
		format, _ = selectChannelAPIFormat(t.Context(), ch, nil, req, "fixture")
		require.Equal(t, "gemini/contents", format)
		req.APIFormat = llm.APIFormatAnthropicMessage
		require.Equal(t, "web_search_tool_not_allowed", channelProtocolRejection(ch, req, llm.APIFormatOpenAIResponse))
	}
}
