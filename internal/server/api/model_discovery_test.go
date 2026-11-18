package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestDiscoveryUsesOnlyClientVisibleEffortMappings(t *testing.T) {
	ctx := t.Context()
	card := &objects.ModelCard{}
	// This is the persisted shape produced before the declaration flag existed.
	require.NoError(t, json.Unmarshal([]byte(`{"reasoning":{"supported":false},"reasoningEfforts":["low","high"]}`), card))
	native := &biz.ModelChannelConnection{Channel: &ent.Channel{ID: 1, Status: channel.StatusEnabled, Type: channel.TypeAnthropic}, Models: []biz.ChannelModelEntry{{ActualModel: "native"}}}
	restricted := &biz.ModelChannelConnection{Models: []biz.ChannelModelEntry{{ActualModel: "converted"}}, Channel: &ent.Channel{ID: 3, Status: channel.StatusEnabled, Type: channel.TypeOpenai, Settings: &objects.ChannelSettings{
		ProtocolPolicy:   &objects.ChannelProtocolPolicy{AllowedProtocols: []string{"openai/responses"}},
		TransformOptions: objects.TransformOptions{ReasoningEffortMapping: []llm.ReasoningEffortMapping{{From: "high", To: "unlisted"}}},
	}}}
	m := biz.ModelFacade{ID: "alias", TargetID: "target", ConfiguredModel: &ent.Model{Type: model.TypeChat, ModelCard: card}, Connections: []*biz.ModelChannelConnection{native, restricted}}
	result := convertModelToOpenAIExtended(ctx, m, nil)
	require.Nil(t, result.Reasoning)
	require.Empty(t, result.SupportedReasoningLevels)
	card.Reasoning.SupportDeclared = true
	result = convertModelToOpenAIExtended(ctx, m, nil)
	require.NotNil(t, result.Reasoning)
	require.False(t, *result.Reasoning)
	card.Reasoning.Supported = true
	require.Equal(t, []string{"low"}, modelDiscoveryRoutes(ctx, m, llm.APIFormatOpenAIResponse).reasoningLevels(card))
	require.Equal(t, []string{"low", "high"}, modelDiscoveryRoutes(ctx, m, llm.APIFormatAnthropicMessage).reasoningLevels(card))
	require.Equal(t, []string{"low", "high"}, card.ReasoningEfforts)
	m.Connections = []*biz.ModelChannelConnection{restricted}
	require.Nil(t, anthropicModel(ctx, m).Capabilities)
	require.Empty(t, modelDiscoveryRoutes(ctx, m, llm.APIFormatAnthropicMessage).channels)
}

func TestAnthropicDiscoveryRequiresCompleteNativeCapabilities(t *testing.T) {
	card := &objects.ModelCard{Reasoning: objects.ModelCardReasoning{Supported: true, SupportDeclared: true}, ReasoningEfforts: []string{"low"}, Modalities: objects.ModelCardModalities{Input: []string{"text"}}}
	m := biz.ModelFacade{ID: "alias", ConfiguredModel: &ent.Model{Type: model.TypeChat, ModelCard: card}, Connections: []*biz.ModelChannelConnection{{Channel: &ent.Channel{ID: 1, Status: channel.StatusEnabled, Type: channel.TypeAnthropic}, Models: []biz.ChannelModelEntry{{ActualModel: "native"}}}}}
	on := map[string]bool{"supported": true}
	raw := map[string]any{"batch": on, "citations": on, "code_execution": on, "image_input": on, "pdf_input": on, "structured_outputs": on,
		"context_management": map[string]any{"supported": true, "clear_thinking_20251015": on, "compact_20260112": on},
		"server_tools":       map[string]any{"supported": true, "code_execution": on, "web_search": on},
		"thinking":           map[string]any{"supported": true, "types": map[string]any{"adaptive": on, "enabled": on, "disabled": on, "between_tools": on}},
		"effort":             map[string]any{"supported": true, "low": on, "medium": on, "high": on, "max": on},
	}
	encode := func() {
		b, err := json.Marshal(raw)
		require.NoError(t, err)
		card.AnthropicCapabilities = b
	}
	encode()
	caps := anthropicModel(t.Context(), m).Capabilities
	require.NotNil(t, caps)
	require.Equal(t, false, caps["batch"].(map[string]any)["supported"])
	require.Equal(t, false, caps["image_input"].(map[string]any)["supported"])
	t.Run("ordinary Messages reconstruction cannot promise document or context edits", func(t *testing.T) {
		for _, name := range []string{"pdf_input", "citations", "context_management"} {
			require.Equal(t, false, caps[name].(map[string]any)["supported"], name)
		}
		management := caps["context_management"].(map[string]any)
		require.Equal(t, map[string]bool{"supported": false}, management["compact_20260112"])
		require.Equal(t, map[string]bool{"supported": false}, management["clear_thinking_20251015"])
		require.NotContains(t, management, "clear_tool_uses_20250919")
	})
	effort := caps["effort"].(map[string]any)
	require.Equal(t, map[string]bool{"supported": true}, effort["low"])
	require.Equal(t, map[string]bool{"supported": false}, effort["high"])
	t.Run("native tools follow the eligible channel types", func(t *testing.T) {
		require.Equal(t, true, caps["code_execution"].(map[string]any)["supported"])
		for _, connections := range [][]*biz.ModelChannelConnection{
			{{Channel: &ent.Channel{ID: 2, Status: channel.StatusEnabled, Type: channel.TypeAnthropicGcp}, Models: []biz.ChannelModelEntry{{ActualModel: "vertex"}}}},
			{m.Connections[0], {Channel: &ent.Channel{ID: 2, Status: channel.StatusEnabled, Type: channel.TypeAnthropicGcp}, Models: []biz.ChannelModelEntry{{ActualModel: "vertex"}}}},
		} {
			mixed := m
			mixed.Connections = connections
			got := anthropicModel(t.Context(), mixed).Capabilities
			require.Equal(t, false, got["code_execution"].(map[string]any)["supported"])
			tools := got["server_tools"].(map[string]any)
			require.Equal(t, false, tools["supported"])
			require.Equal(t, map[string]bool{"supported": false}, tools["web_search"])
			require.Equal(t, map[string]bool{"supported": false}, tools["code_execution"])
			require.Equal(t, true, got["structured_outputs"].(map[string]any)["supported"])
		}
	})
	t.Run("explicitly disabled thinking retains both native off modes", func(t *testing.T) {
		offCard := *card
		offCard.Reasoning.Supported = false
		off := m
		off.ConfiguredModel = &ent.Model{Type: model.TypeChat, ModelCard: &offCard}
		thinking := anthropicModel(t.Context(), off).Capabilities["thinking"].(map[string]any)
		require.Equal(t, false, thinking["supported"])
		modes := thinking["types"].(map[string]any)
		for _, key := range []string{"enabled", "adaptive", "disabled", "between_tools"} {
			require.Equal(t, map[string]bool{"supported": key == "disabled" || key == "between_tools"}, modes[key], key)
		}
	})
	t.Run("protocol overrides belong to individual retry models", func(t *testing.T) {
		ch := &ent.Channel{ID: 1, Status: channel.StatusEnabled, Type: channel.TypeAnthropic,
			Endpoints: []objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAIChatCompletion.String()}},
			Settings: &objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{
				{Model: "converted", APIFormats: []string{llm.APIFormatOpenAIChatCompletion.String()}},
			}},
		}
		entries := []biz.ChannelModelEntry{{ActualModel: "converted"}, {ActualModel: "native"}}
		model := m
		model.Connections = []*biz.ModelChannelConnection{{Channel: ch, Models: entries}}
		require.Nil(t, anthropicModel(t.Context(), model).Capabilities)
		ch.Settings.ProtocolPolicy = &objects.ChannelProtocolPolicy{SameProtocolOnly: true}
		require.NotNil(t, anthropicModel(t.Context(), model).Capabilities)
		require.Len(t, model.Connections[0].Models, 2)
	})
	t.Run("ollama chat does not forward reasoning levels", func(t *testing.T) {
		ollama := m
		ollama.Connections = []*biz.ModelChannelConnection{{Channel: &ent.Channel{ID: 4, Status: channel.StatusEnabled, Type: channel.TypeOllama}, Models: []biz.ChannelModelEntry{{ActualModel: "local"}}}}
		routes := modelDiscoveryRoutes(t.Context(), ollama, llm.APIFormatOpenAIResponse)
		require.Empty(t, routes.reasoningLevels(card))
	})
	m.Connections = append(m.Connections, &biz.ModelChannelConnection{Models: []biz.ChannelModelEntry{{ActualModel: "converted"}}, Channel: &ent.Channel{ID: 3, Status: channel.StatusEnabled, Type: channel.TypeOpenai}})
	require.Nil(t, anthropicModel(t.Context(), m).Capabilities)
	m.Connections = m.Connections[:1]
	card.ReasoningEfforts = nil
	m.Connections[0].Channel.Settings = &objects.ChannelSettings{TransformOptions: objects.TransformOptions{
		ReasoningEffortMapping: []llm.ReasoningEffortMapping{{From: "high", To: "unlisted"}},
	}}
	effort = anthropicModel(t.Context(), m).Capabilities["effort"].(map[string]any)
	require.Equal(t, map[string]bool{"supported": false}, effort["high"])
	require.Equal(t, map[string]bool{"supported": true}, effort["low"])
	delete(raw, "server_tools")
	encode()
	require.Nil(t, anthropicModel(t.Context(), m).Capabilities)
}
