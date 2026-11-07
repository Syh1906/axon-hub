package biz

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestCodexOfficialCatalogWithoutProviderMetadata(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var err error
	models.codexSnapshots, err = NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
	require.NoError(t, err)
	ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("inference-only").
		SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetSupportedModels([]string{"gpt-6.1-terra", "gpt-6-luna", "deepseek-test"}).SetDefaultTestModel("gpt-6-luna").
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(`{"models":[]}`)}}).
		SetStatus(channel.StatusEnabled).SaveX(ctx)
	for _, id := range []string{"gpt-6.1-terra", "gpt-6-luna", "deepseek-test"} {
		createConfiguredModel(t, ctx, client, id, model.TypeChat, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	}
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	body, omitted, err := svc.Models(ctx, "0.167.0")
	require.NoError(t, err)
	require.Equal(t, 1, omitted, "unknown third-party models need their own definition")
	catalog, err := parseCodexCatalog(body)
	require.NoError(t, err)
	require.Len(t, catalog, 2, "official metadata must not advertise unconfigured models")
	require.Len(t, catalog["gpt-6.1-terra"]["supported_reasoning_levels"], 6)
	require.Len(t, catalog["gpt-6-luna"]["supported_reasoning_levels"], 5)
	for _, m := range client.Model.Query().AllX(ctx) {
		if m.ModelID == "deepseek-test" {
			profile := reasoningProfileFixture(t, "deepseek-test", "low", "high")
			d := codexDefinition{ClientVersion: "0.166.0", Model: profile}
			m.Settings.CodexCatalog, err = json.Marshal(d)
			require.NoError(t, err)
		} else {
			preview, err := models.PreviewCodexCatalog(ctx, m.ID, "0.167.0")
			require.NoError(t, err)
			d, err := decodeCodexDefinition(preview)
			require.NoError(t, err)
			require.Equal(t, "official_snapshot", d.Models[m.ModelID].SourceKind)
			require.Equal(t, "0.167.0", d.SnapshotVersion)
			m.Settings.CodexCatalog = []byte(`{"mode":"inherit","snapshotVersion":"0.166.0","overrides":{"description":"我的说明","default_reasoning_level":"high"}}`)
		}
		require.NoError(t, validateModelSettings(m.Settings))
		client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
	}
	body, omitted, err = svc.Models(ctx, "0.167.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	catalog, err = parseCodexCatalog(body)
	require.NoError(t, err)
	require.Len(t, catalog, 3)
	require.Equal(t, "我的说明", catalog["gpt-6-luna"]["description"])
	require.Equal(t, "high", catalog["gpt-6-luna"]["default_reasoning_level"])
	require.Equal(t, "0.166.0", catalog["gpt-6-luna"]["axonhub_catalog"].(map[string]any)["version"])
	require.Len(t, catalog["deepseek-test"]["supported_reasoning_levels"], 2)

	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ModelIDs: []string{"my-luna"}, ModelMappings: []objects.ModelMapping{{From: "my-luna", To: "gpt-6-luna"}}}}}}
	body, _, err = svc.Models(contexts.WithAPIKey(ctx, key), "0.167.0")
	require.NoError(t, err)
	catalog, err = parseCodexCatalog(body)
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	require.Equal(t, "my-luna", catalog["my-luna"]["slug"])
	client.Channel.UpdateOneID(ch.ID).SetStatus(channel.StatusDisabled).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	body, _, err = svc.Models(ctx, "0.167.0")
	require.NoError(t, err)
	require.JSONEq(t, `{"models":[]}`, string(body), "a snapshot cannot grant a route")
}

func TestCodexInheritedThirdPartyAndUpdates(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	profile := reasoningProfileFixture(t, "deepseek-test", "low", "high")
	profile["base_instructions"] = "original third party instructions"
	body, err := json.Marshal(map[string]any{"models": []any{profile}})
	require.NoError(t, err)
	ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("third-party").SetBaseURL("http://127.0.0.1:1").
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"deepseek-test"}).SetDefaultTestModel("deepseek-test").
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: body}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
	createConfiguredModel(t, ctx, client, "my-deepseek", model.TypeChat, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "deepseek-test"}})
	m := client.Model.Query().OnlyX(ctx)
	m.Settings.CodexCatalog = []byte(`{"mode":"inherit","sourceModelId":"deepseek-test","overrides":{"description":"自定义名称","default_reasoning_level":"high","context_window":16000}}`)
	client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
	svc := NewCodexCatalogService(models, channels)
	for _, instructions := range []string{"original third party instructions", "updated third party instructions"} {
		profile["base_instructions"] = instructions
		body, err = json.Marshal(map[string]any{"models": []any{profile}})
		require.NoError(t, err)
		client.Channel.UpdateOneID(ch.ID).SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: body}}).SaveX(ctx)
		reloadEnabledChannels(t, ctx, client, channels)
		body, omitted, err := svc.Models(ctx, "0.167.0")
		require.NoError(t, err)
		require.Zero(t, omitted)
		catalog, err := parseCodexCatalog(body)
		require.NoError(t, err)
		require.Equal(t, instructions, catalog["my-deepseek"]["base_instructions"])
		require.Equal(t, "自定义名称", catalog["my-deepseek"]["description"])
		require.Equal(t, "high", catalog["my-deepseek"]["default_reasoning_level"])
		require.Equal(t, float64(16000), catalog["my-deepseek"]["context_window"], "explicit overrides must survive route metadata")
	}
	m.Settings.CodexCatalog = []byte(`{"mode":"inherit","sourceModelId":"missing-model"}`)
	client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
	_, _, err = svc.Models(ctx, "0.167.0")
	require.Error(t, err, "an explicit base must not silently become a different model")
}

func TestCodexProfileOverridesValidationAndImmutability(t *testing.T) {
	profile := reasoningProfileFixture(t, "test-model", "medium", "high")
	before, err := json.Marshal(profile)
	require.NoError(t, err)
	patched, err := applyCodexProfileOverrides(profile, map[string]any{"description": "custom", "truncation_policy": map[string]any{"limit": float64(2000)}, "upgrade": nil})
	require.NoError(t, err)
	require.Equal(t, "tokens", patched["truncation_policy"].(map[string]any)["mode"])
	require.Equal(t, float64(2000), patched["truncation_policy"].(map[string]any)["limit"])
	after, err := json.Marshal(profile)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	_, err = applyCodexProfileOverrides(profile, map[string]any{"shell_type": "bad"})
	require.Error(t, err)
	for _, raw := range []string{`{"mode":"inherit","overrides":{"slug":"wrong"}}`, `{"mode":"inherit","snapshotVersion":"main"}`, `{"mode":"unknown"}`, `{"mode":"inherit","model":{}}`} {
		_, err := decodeCodexDefinition([]byte(raw))
		require.Error(t, err, raw)
	}
	_, err = decodeCodexDefinition([]byte(`{"mode":"inherit","overrides":{"description":"ok"}}`))
	require.NoError(t, err)
	require.False(t, strings.Contains(string(before), "custom"))
}

func TestCodexOfficialSnapshotFallbackPrecedence(t *testing.T) {
	snapshots, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, nil)
	require.NoError(t, err)
	snapshot := snapshots.snapshots["0.167.0"]
	ch := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/responses"}}}
	connections := []*ModelChannelConnection{{Channel: ch, Models: []ChannelModelEntry{{ActualModel: "gpt-6.1-terra"}}}}
	m := ModelFacade{ID: "gpt-6.1-terra", TargetID: "gpt-6.1-terra", ConfiguredModel: &ent.Model{}, Connections: connections}
	for _, complete := range []bool{false, true} {
		levels := []string{"none"}
		if complete {
			levels = []string{"low", "medium", "high", "xhigh", "max", "ultra"}
		}
		profile := reasoningProfileFixture(t, m.ID, levels...)
		profile["base_instructions"] = "provider deployment instructions"
		result, err := automaticCodexModel(t.Context(), m, connections, map[int]map[string]map[string]any{1: {m.ID: profile}}, map[int]*Channel{1: {Channel: ch}}, "0.167.0", snapshot, nil)
		require.NoError(t, err)
		want := snapshot.Models[m.ID]["base_instructions"]
		if complete {
			want = profile["base_instructions"]
		}
		require.Equal(t, want, result["base_instructions"])
		require.Len(t, result["supported_reasoning_levels"], 6)
	}
	_, err = automaticCodexModel(t.Context(), m, connections, nil, map[int]*Channel{1: {Channel: ch}}, "0.167.0", snapshot, &codexDefinition{Mode: "inherit", SourceModelID: "not-official", SnapshotVersion: "0.167.0"})
	require.ErrorContains(t, err, "pinned official Codex profile unavailable")
}

func TestCodexInheritedToolOverridesBeforeCompatibility(t *testing.T) {
	for _, official := range []bool{false, true} {
		profile := reasoningProfileFixture(t, "test-model", "medium", "high")
		profile["apply_patch_tool_type"] = "freeform"
		profile["tool_mode"] = "code_mode_only"
		ch := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses,
			Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/responses"}, {APIFormat: "openai/chat_completions"}},
			Settings:  &objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "test-model", APIFormats: []string{"openai/chat_completions"}}}},
		}
		connections := []*ModelChannelConnection{{Channel: ch, Models: []ChannelModelEntry{{ActualModel: "test-model"}}}}
		m := ModelFacade{ID: "test-model", TargetID: "test-model", ConfiguredModel: &ent.Model{}, Connections: connections}
		sources := map[int]map[string]map[string]any{1: {"test-model": profile}}
		var snapshot *codexSnapshot
		if official {
			sources = nil
			snapshot = &codexSnapshot{Version: "0.167.0", Models: map[string]map[string]any{"test-model": profile}}
		}
		d := &codexDefinition{Mode: "inherit", Overrides: map[string]any{"apply_patch_tool_type": nil, "tool_mode": nil}}
		result, err := automaticCodexModel(t.Context(), m, connections, sources, map[int]*Channel{1: {Channel: ch}}, "0.167.0", snapshot, d)
		require.NoError(t, err, "explicit tool overrides must participate in profile compatibility")
		require.Nil(t, result["apply_patch_tool_type"])
		require.Nil(t, result["tool_mode"])
		require.Equal(t, "freeform", profile["apply_patch_tool_type"], "source metadata stays immutable")
		// Adding the requirement still cannot grant a Responses route.
		d.Overrides["apply_patch_tool_type"] = "freeform"
		_, err = automaticCodexModel(t.Context(), m, connections, sources, map[int]*Channel{1: {Channel: ch}}, "0.167.0", snapshot, d)
		require.ErrorContains(t, err, "no visible route")
	}
}

func TestCodexOfficialPreviewReportsSelectedSourceDifferences(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var err error
	models.codexSnapshots, err = NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
	require.NoError(t, err)
	profile := reasoningProfileFixture(t, "gpt-6.1-terra", "none")
	profile["base_instructions"] = "provider deployment instructions"
	body, err := json.Marshal(map[string]any{"models": []any{profile}})
	require.NoError(t, err)
	ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("sparse-provider").
		SetBaseURL("http://127.0.0.1:1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetSupportedModels([]string{"gpt-6.1-terra"}).SetDefaultTestModel("gpt-6.1-terra").
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: body}}).SetStatus(channel.StatusEnabled).SaveX(ctx)
	createConfiguredModel(t, ctx, client, "gpt-6.1-terra", model.TypeChat, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	reloadEnabledChannels(t, ctx, client, channels)
	preview, err := models.PreviewCodexCatalog(ctx, client.Model.Query().OnlyX(ctx).ID, "0.167.0")
	require.NoError(t, err)
	d, err := decodeCodexDefinition(preview)
	require.NoError(t, err)
	require.Equal(t, "official_snapshot", d.SourceKind)
	require.Len(t, d.SourceDifferences, 1, "differences must compare to the final official profile")
	require.Equal(t, ch.ID, d.SourceDifferences[0].ChannelID)
	require.Contains(t, d.SourceDifferences[0].Fields, "base_instructions")
	require.Contains(t, d.SourceDifferences[0].Fields, "apply_patch_tool_type")
	require.NotContains(t, d.SourceDifferences[0].Fields, "shell_type", "both profiles use unified_exec after normalization")
}
