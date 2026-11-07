package biz

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func reasoningProfileFixture(t *testing.T, id string, efforts ...string) map[string]any {
	t.Helper()
	parsed, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	profile := parsed["test-model"]
	profile["slug"] = id
	levels := make([]any, 0, len(efforts))
	for _, effort := range efforts {
		levels = append(levels, map[string]any{"effort": effort, "description": effort})
	}
	profile["supported_reasoning_levels"] = levels
	profile["default_reasoning_level"] = nil
	return profile
}

func TestCodexReasoningProfileSourceSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		firstID  string
		secondID string
		first    []string
		second   []string
		want     int
	}{
		{"full source follows default only", "target", "target", []string{"none"}, []string{"low", "medium", "high", "xhigh", "max", "ultra"}, 2},
		{"full source already first", "target", "target", []string{"low", "medium", "high"}, []string{"none"}, 1},
		{"empty source", "target", "target", nil, []string{"high"}, 2},
		{"equal coverage keeps precedence", "target", "target", []string{"low", "high"}, []string{"medium", "max"}, 1},
		{"none is not extra reasoning", "target", "target", []string{"high"}, []string{"none", "high"}, 1},
		{"duplicate entries do not add coverage", "target", "target", []string{"low", "high"}, []string{"high", "high", "high"}, 1},
		{"same opaque upstream alias", "codex-auto-review", "codex-auto-review", []string{"none"}, []string{"low", "high"}, 2},
		{"different actual models are not combined", "old-luna", "new-luna", []string{"high"}, []string{"low", "high"}, 1},
		{"exact target beats richer alias", "alias", "target", []string{"low", "high"}, []string{"none"}, 2},
		{"later alias cannot replace exact target", "target", "alias", []string{"none"}, []string{"low", "high"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := reasoningProfileFixture(t, tc.firstID, tc.first...)
			second := reasoningProfileFixture(t, tc.secondID, tc.second...)
			connections := []*ModelChannelConnection{
				{Channel: &ent.Channel{ID: 1}, Priority: 0, Models: []ChannelModelEntry{{ActualModel: tc.firstID}}},
				{Channel: &ent.Channel{ID: 2}, Priority: 10, Models: []ChannelModelEntry{{ActualModel: tc.secondID}}},
			}
			sources := map[int]map[string]map[string]any{1: {tc.firstID: first}, 2: {tc.secondID: second}}
			before, err := json.Marshal(sources)
			require.NoError(t, err)
			for _, order := range [][]*ModelChannelConnection{connections, {connections[1], connections[0]}} {
				selected, _ := previewCodexModelProfile("target", order, sources)
				require.Equal(t, tc.want, selected.SourceChannelID)
			}
			after, err := json.Marshal(sources)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after))
		})
	}
}

func TestCodexCatalogReasoningProfileAcrossSources(t *testing.T) {
	for _, target := range []string{"gpt-6.1-terra", "gpt-6-luna"} {
		t.Run(target, func(t *testing.T) {
			ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
			full := reasoningProfileFixture(t, target, "low", "medium", "high", "xhigh", "max", "ultra")
			full["default_reasoning_level"] = "high"
			full["multi_agent_reasoning_effort"] = "xhigh"
			full["supports_reasoning_effort_updates"] = true
			full["base_instructions"] = "complete model instructions"
			minimal := reasoningProfileFixture(t, target, "none")
			minimal["default_reasoning_level"] = "none"
			minimal["supports_reasoning_effort_updates"] = false
			minimal["context_window"] = float64(16000)
			var ids []int
			var associations []*objects.ModelAssociation
			for i, profile := range []map[string]any{minimal, full} {
				body, err := json.Marshal(map[string]any{"models": []any{profile}})
				require.NoError(t, err)
				name := []string{"minimal-first", "full-second"}[i]
				ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName(name).
					SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
					SetSupportedModels([]string{target}).SetDefaultTestModel(target).
					SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: body}}).
					SetStatus(channel.StatusEnabled).SaveX(ctx)
				ids = append(ids, ch.ID)
				associations = append(associations, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
			}
			createConfiguredModel(t, ctx, client, target, model.TypeChat, associations[0])
			m := client.Model.Query().OnlyX(ctx)
			m.Settings.Associations = associations
			client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
			reloadEnabledChannels(t, ctx, client, channels)
			svc := NewCodexCatalogService(models, channels)

			preview, err := models.PreviewCodexCatalog(ctx, m.ID, "0.166.0")
			require.NoError(t, err)
			definition, err := decodeCodexDefinition(preview)
			require.NoError(t, err)
			require.Equal(t, ids[1], definition.Models[target].SourceChannelID)
			require.Equal(t, minimal["supported_reasoning_levels"], definition.Routes[0].Capabilities["supported_reasoning_levels"], "preview retains original source evidence")
			for _, manual := range []bool{false, true} {
				if manual {
					m.Settings.CodexCatalog = preview
					client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
				}
				for _, allowed := range [][]int{ids, {ids[0]}, {ids[1]}} {
					key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ChannelIDs: allowed, ModelIDs: []string{"client-alias"}, ModelMappings: []objects.ModelMapping{{From: "client-alias", To: target}}}}}}
					keyCtx := contexts.WithAPIKey(ctx, key)
					body, omitted, err := svc.Models(keyCtx, "0.166.0")
					require.NoError(t, err)
					require.Zero(t, omitted)
					catalog, err := parseCodexCatalog(body)
					require.NoError(t, err)
					got := catalog["client-alias"]
					require.Equal(t, full["supported_reasoning_levels"], got["supported_reasoning_levels"])
					require.Equal(t, "high", got["default_reasoning_level"])
					require.Equal(t, true, got["supports_reasoning_effort_updates"])
					require.Equal(t, "xhigh", got["multi_agent_reasoning_effort"])
					require.Equal(t, "complete model instructions", got["base_instructions"])
					wantContext := float64(16000)
					if len(allowed) == 1 && allowed[0] == ids[1] {
						wantContext = 32000
					}
					require.Equal(t, wantContext, got["context_window"], "other route capabilities still constrain the catalog")
					visible, err := models.ListEnabledModels(keyCtx)
					require.NoError(t, err)
					require.Len(t, visible, 1)
					require.Len(t, visible[0].Connections, len(allowed), "shared metadata does not grant routes")
				}
			}
			// Explicit manual definitions remain authoritative even when source
			// snapshots include richer choices.
			definition.Models[target] = codexModelProfile{SourceChannelID: ids[0], SourceModelID: target, Model: minimal}
			definition.Model = minimal
			raw, err := json.Marshal(definition)
			require.NoError(t, err)
			m.Settings.CodexCatalog = raw
			client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
			body, _, err := svc.Models(ctx, "0.166.0")
			require.NoError(t, err)
			catalog, err := parseCodexCatalog(body)
			require.NoError(t, err)
			require.Equal(t, minimal["supported_reasoning_levels"], catalog[target]["supported_reasoning_levels"])

			// Automatic discovery must not use disabled sources or invent levels
			// when every remaining source only declares the default behavior.
			m.Settings.CodexCatalog = nil
			client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
			client.Channel.UpdateOneID(ids[1]).SetStatus(channel.StatusDisabled).SaveX(ctx)
			reloadEnabledChannels(t, ctx, client, channels)
			body, _, err = svc.Models(ctx, "0.166.0")
			require.NoError(t, err)
			catalog, err = parseCodexCatalog(body)
			require.NoError(t, err)
			require.Equal(t, minimal["supported_reasoning_levels"], catalog[target]["supported_reasoning_levels"])
			require.Equal(t, "none", catalog[target]["default_reasoning_level"])
		})
	}
}

func TestCodexReasoningProfileDeclaredUltraWithoutSelectableUnderlyingEffort(t *testing.T) {
	profile := reasoningProfileFixture(t, "test-model", "medium", "ultra")
	profile["multi_agent_reasoning_effort"] = "xhigh"
	definition := codexDefinition{Model: profile}
	m := ModelFacade{ID: "test-model", ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{
		{Channel: &ent.Channel{ID: 1}, Models: []ChannelModelEntry{{ActualModel: "test-model"}}},
	}}
	result, err := definition.catalogModel(m, false)
	require.NoError(t, err)
	require.Equal(t, profile["supported_reasoning_levels"], result["supported_reasoning_levels"], "an unchanged profile is not an explicit limit")
}

func TestCodexCatalogReasoningProfileKeepsCompatibleRoutes(t *testing.T) {
	for _, toolField := range []string{"apply_patch_tool_type", "tool_mode"} {
		t.Run(toolField, func(t *testing.T) {
			ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
			plain := reasoningProfileFixture(t, "test-model", "none")
			full := reasoningProfileFixture(t, "test-model", "low", "medium", "high")
			full["base_instructions"] = "Responses model instructions"
			if toolField == "apply_patch_tool_type" {
				full[toolField] = "freeform"
			} else {
				full[toolField] = "code_mode_only"
			}
			var ids []int
			var associations []*objects.ModelAssociation
			for i, profile := range []map[string]any{plain, full} {
				body, err := json.Marshal(map[string]any{"models": []any{profile}})
				require.NoError(t, err)
				name := []string{"chat-only", "responses"}[i]
				settings := &objects.ChannelSettings{}
				if i == 0 {
					settings.ModelProtocols = []objects.ModelProtocol{{Model: "test-model", APIFormats: []string{"openai/chat_completions"}}}
				}
				ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName(name).
					SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
					SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").
					SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: body}, {APIFormat: "openai/chat_completions"}}).
					SetSettings(settings).SetStatus(channel.StatusEnabled).SaveX(ctx)
				ids = append(ids, ch.ID)
				associations = append(associations, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
			}
			createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, associations[0])
			m := client.Model.Query().OnlyX(ctx)
			m.Settings.Associations = associations
			client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
			reloadEnabledChannels(t, ctx, client, channels)
			svc := NewCodexCatalogService(models, channels)
			for _, allowed := range [][]int{{ids[0]}, ids} {
				key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ChannelIDs: allowed}}}}
				body, omitted, err := svc.Models(contexts.WithAPIKey(ctx, key), "0.166.0")
				require.NoError(t, err, "a richer source must not erase the only compatible route")
				require.Zero(t, omitted)
				catalog, err := parseCodexCatalog(body)
				require.NoError(t, err)
				want := plain
				if len(allowed) == 2 {
					want = full
				}
				for _, field := range []string{"supported_reasoning_levels", "base_instructions", toolField} {
					require.Equal(t, want[field], catalog["test-model"][field], field)
				}
			}
		})
	}
}

func TestCodexCatalogReasoningProfilePreservesAliasCapabilities(t *testing.T) {
	profile := reasoningProfileFixture(t, "target", "medium")
	profile["context_window"] = float64(64000)
	alias := reasoningProfileFixture(t, "upstream-alias", "none")
	alias["apply_patch_tool_type"] = "freeform"
	alias["context_window"] = float64(16000)
	source := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses}
	chat := &ent.Channel{ID: 2, Type: channel.TypeOpenai, Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/chat_completions"}}}
	connections := []*ModelChannelConnection{
		{Channel: source, Models: []ChannelModelEntry{{ActualModel: "target"}}},
		{Channel: chat, Models: []ChannelModelEntry{{ActualModel: "upstream-alias"}}},
	}
	m := ModelFacade{ID: "target", TargetID: "target", ConfiguredModel: &ent.Model{}, Connections: connections[1:]}
	result, err := automaticCodexModel(t.Context(), m, connections,
		map[int]map[string]map[string]any{1: {"target": profile, "upstream-alias": alias}},
		map[int]*Channel{1: {Channel: source}, 2: {Channel: chat}}, "0.166.0")
	require.NoError(t, err)
	require.Equal(t, float64(16000), result["context_window"], "tool-incompatible client metadata still describes the actual model's context limit")
	require.Equal(t, profile["supported_reasoning_levels"], result["supported_reasoning_levels"])
	require.Equal(t, profile["base_instructions"], result["base_instructions"])
	require.Nil(t, result["apply_patch_tool_type"], "the usable client profile keeps its own tools")
}
