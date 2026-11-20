package biz

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestCodexPublicRepositoryFile(t *testing.T) {
	if os.Getenv("AXONHUB_TEST_PUBLIC_CODEX_CATALOG") != "1" {
		t.Skip("requires public repository access")
	}
	ctx, client, _, _, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, client)
	require.NoError(t, err)
	require.NoError(t, svc.CheckPublicCatalog(ctx))
	state, err := svc.PublicStatus(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, state.Entries)
	for _, entry := range state.Entries {
		if strings.HasPrefix(entry.Slug, "gpt-6.2-") {
			t.Logf("%s context=%v/%v levels=%v default=%v", entry.Slug, entry.Context, entry.Maximum, entry.Efforts, entry.DefaultEffort)
		}
	}
}

func TestCodexPublicCatalogSync(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, client)
	require.NoError(t, err)
	const slug = "gpt-6.2-astra"
	sparse := reasoningProfileFixture(t, slug, "none")
	sparse["default_reasoning_level"] = "none"
	inline, err := json.Marshal(map[string]any{"models": []any{sparse}})
	require.NoError(t, err)
	ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("catalog-test").
		SetSupportedModels([]string{slug}).SetDefaultTestModel(slug).SetBaseURL("https://example.invalid").
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetStatus(channel.StatusEnabled).
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: inline}}).SaveX(ctx)
	createConfiguredModel(t, ctx, client, slug, model.TypeChat, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	reloadEnabledChannels(t, ctx, client, channels)
	profile := reasoningProfileFixture(t, slug, "low", "medium", "high", "xhigh", "max", "ultra")
	profile["minimal_client_version"], profile["context_window"], profile["max_context_window"], profile["default_reasoning_level"] = "0.167.0", 272000, 872000, "low"
	payload, err := json.Marshal(map[string]any{"models": []any{profile}})
	require.NoError(t, err)
	calls, status := 0, http.StatusOK
	svc.httpClient.Transport = snapshotTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, publicCatalogURL, req.URL.String())
		require.Empty(t, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("ChatGPT-Account-ID"))
		if status == http.StatusNotModified {
			require.Equal(t, `"one"`, req.Header.Get("If-None-Match"))
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Etag": {`"one"`}}, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
	})
	require.NoError(t, svc.SetPublicSchedule(ctx, PublicCatalogSchedule{Automatic: false, Minutes: 30}))
	require.NoError(t, svc.checkPublicIfDue(ctx))
	require.Zero(t, calls)
	require.NoError(t, svc.SetPublicSchedule(ctx, PublicCatalogSchedule{Automatic: true, Minutes: 30}))
	require.NoError(t, svc.checkPublicIfDue(ctx))
	require.NoError(t, svc.checkPublicIfDue(ctx))
	require.Equal(t, 1, calls)
	state, err := svc.PublicStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, state.Changed)
	require.Len(t, state.Entries[0].Efforts, 6)
	require.Equal(t, "low", state.Entries[0].DefaultEffort)
	archiveRow := client.System.Query().Where(system.KeyEQ(publicArchiveKey)).OnlyX(ctx)
	status = http.StatusNotModified
	require.NoError(t, svc.CheckPublicCatalog(ctx))
	state, err = svc.PublicStatus(ctx)
	require.NoError(t, err)
	require.Zero(t, state.Changed)
	restore := func() *CodexSnapshotService {
		t.Helper()
		restarted, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
		require.NoError(t, err)
		require.NoError(t, restarted.startPublicCatalog(ctx, nil))
		saved, err := restarted.PublicStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, state.CheckedAt, saved.CheckedAt)
		require.Equal(t, state.LastError, saved.LastError)
		require.Equal(t, state.Changed, saved.Changed)
		require.Equal(t, state.Entries, saved.Entries)
		return restarted
	}
	restore()
	status, payload = http.StatusOK, []byte(`{"models":[]}`)
	require.Error(t, svc.CheckPublicCatalog(ctx))
	state, err = svc.PublicStatus(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, state.LastError)
	afterFailure := client.System.Query().Where(system.KeyEQ(publicArchiveKey)).OnlyX(ctx)
	require.Equal(t, archiveRow.UpdatedAt, afterFailure.UpdatedAt)
	require.Equal(t, archiveRow.Value, afterFailure.Value)
	restarted := restore()
	require.ErrorIs(t, restarted.CheckPublicCatalog(ctx), ErrPublicCatalogDisabled)
	models.codexSnapshots = restarted
	catalog := NewCodexCatalogService(models, channels)
	read := func(version string) map[string]any {
		t.Helper()
		body, omitted, err := catalog.Models(ctx, version)
		require.NoError(t, err)
		require.Zero(t, omitted)
		entries, err := parseCodexCatalog(body)
		require.NoError(t, err)
		return entries[slug]
	}
	require.Equal(t, "none", read("0.166.0")["default_reasoning_level"])
	entry := read("0.167.0")
	require.Len(t, entry["supported_reasoning_levels"], 6)
	require.Equal(t, "low", entry["default_reasoning_level"])
	require.Equal(t, float64(272000), entry["context_window"])
	require.Equal(t, float64(872000), entry["max_context_window"])
	configured := client.Model.Query().OnlyX(ctx)
	for _, check := range []struct {
		settings                 *objects.ModelContextSettings
		wantDefault, wantMaximum float64
	}{
		{&objects.ModelContextSettings{MaxContextWindow: lo.ToPtr(600000)}, 272000, 600000},
		{&objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(372000)}, 372000, 872000},
	} {
		configured.Settings.ContextSettings = check.settings
		_, err := models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{Settings: configured.Settings})
		require.NoError(t, err)
		entry := read("0.167.0")
		require.Equal(t, check.wantDefault, entry["context_window"])
		require.Equal(t, check.wantMaximum, entry["max_context_window"])
	}
	preview, err := models.PreviewCodexCatalog(ctx, configured.ID, "0.167.0")
	require.NoError(t, err)
	definition, err := decodeCodexDefinition(preview)
	require.NoError(t, err)
	require.Equal(t, "official_public", definition.Models[slug].SourceKind)
	require.Nil(t, snapshotCodexProfile(restarted.Load(ctx, "0.167.0", "0.167.0"), slug))
}

func TestPublicCatalogCapsEachActualRoute(t *testing.T) {
	const target = "gpt-6.2-astra"
	profile := reasoningProfileFixture(t, target, "low", "high")
	profile["context_window"], profile["max_context_window"], profile["minimal_client_version"] = float64(272000), float64(872000), "0.166.0"
	snapshot := &codexSnapshot{public: &publicCatalogArchive{models: map[string]map[string]any{target: profile}}, clientVersion: "0.167.0"}
	route := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Settings: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: "actual-alias", MaxContextWindow: lo.ToPtr(128000)}}}}
	for _, example := range []struct {
		actual               string
		model                *objects.ModelContextSettings
		wantDefault, wantMax float64
	}{
		{"actual-alias", nil, 128000, 128000},
		{"actual-alias", &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(64000)}, 64000, 128000},
		{"actual-alias", &objects.ModelContextSettings{MaxContextWindow: lo.ToPtr(96000)}, 96000, 96000},
		{"other-alias", nil, 272000, 872000},
	} {
		m := ModelFacade{ID: target, TargetID: target, ConfiguredModel: &ent.Model{Settings: &objects.ModelSettings{ContextSettings: example.model}}, Connections: []*ModelChannelConnection{{Channel: route, Models: []ChannelModelEntry{{ActualModel: example.actual}}}}}
		got, err := automaticCodexModel(t.Context(), m, m.Connections, nil, catalogTestChannels(m), "0.167.0", snapshot, nil)
		require.NoError(t, err)
		require.Equal(t, example.wantDefault, got["context_window"])
		require.Equal(t, example.wantMax, got["max_context_window"])
	}
}

func TestOfficialCodexDirectoryModelFieldPriority(t *testing.T) {
	const slug, version = "priority-test", "0.167.0"
	for _, kind := range []string{"official_public", "official_snapshot", "channel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
			snap, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
			require.NoError(t, err)
			models.codexSnapshots = snap
			source := reasoningProfileFixture(t, slug, "low", "high")
			source["minimal_client_version"] = version
			source["input_modalities"] = []any{"text", "image", "audio"}
			source["context_window"], source["max_context_window"] = float64(32000), float64(96000)
			source["supports_search_tool"] = false
			source["node_repl_auto_review_required"] = true
			entries := map[string]map[string]any{slug: source}
			switch kind {
			case "official_public":
				snap.public.archive = &publicCatalogArchive{Source: "official", SHA256: strings.Repeat("a", 64), models: entries}
			case "official_snapshot":
				snap.snapshots[version] = &codexSnapshot{Version: version, Models: entries}
			}
			upstream := reasoningProfileFixture(t, slug, "low")
			upstream["node_repl_auto_review_required"] = true
			report, err := json.Marshal(map[string]any{"models": []any{upstream}})
			require.NoError(t, err)
			ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("text-only-report").
				SetBaseURL("https://example.invalid").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
				SetSupportedModels([]string{slug}).SetDefaultTestModel(slug).SetStatus(channel.StatusEnabled).
				SetSettings(&objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: slug, MaxContextWindow: lo.ToPtr(8000)}}}).
				SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: report}, {APIFormat: "openai/chat_completions"}}).SaveX(ctx)
			createConfiguredModel(t, ctx, client, slug, model.TypeChat, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
			reloadEnabledChannels(t, ctx, client, channels)
			modelRow := client.Model.Query().OnlyX(ctx)
			save := func(def json.RawMessage, card *objects.ModelCard, windows *objects.ModelContextSettings) {
				t.Helper()
				modelRow.Settings.CodexCatalog = objects.JSONRawMessage(def)
				modelRow.Settings.ContextSettings = windows
				_, err := models.UpdateModel(ctx, modelRow.ID, &ent.UpdateModelInput{ModelCard: card, Settings: modelRow.Settings})
				require.NoError(t, err)
			}
			read := func() map[string]any {
				t.Helper()
				raw, omitted, err := NewCodexCatalogService(models, channels).Models(ctx, version)
				require.NoError(t, err)
				require.Zero(t, omitted)
				got, err := parseCodexCatalog(raw)
				require.NoError(t, err)
				require.Len(t, got, 1)
				return got[slug]
			}
			got := read()
			if kind != "channel" {
				require.Equal(t, kind, got["axonhub_catalog"].(map[string]any)["source"])
			} else {
				require.NotContains(t, got, "axonhub_catalog")
			}
			require.Equal(t, []any{"text"}, got["input_modalities"], "no declared model fields: retain the channel merge")
			require.Equal(t, float64(8000), got["max_context_window"])
			save(nil, &objects.ModelCard{Vision: true}, nil)
			require.Equal(t, []any{"text", "image"}, read()["input_modalities"], "vision alone does not declare audio")
			preview, err := models.PreviewCodexCatalog(ctx, modelRow.ID, version)
			require.NoError(t, err)
			fixed, err := decodeCodexDefinition(preview)
			require.NoError(t, err)
			require.NotEmpty(t, fixed.Models[slug].CatalogDefaults)
			patch := map[string]any{
				"input_modalities":               []any{"audio"},
				"context_window":                 float64(40000),
				"max_context_window":             float64(48000),
				"supports_search_tool":           true,
				"node_repl_auto_review_required": false,
			}
			entry := fixed.Models[slug]
			entry.Model["experimental_supported_tools"] = []any{"fixed-tool"}
			entry.Overrides = patch
			fixed.Models[slug] = entry
			fixedJSON, err := json.Marshal(fixed)
			require.NoError(t, err)
			inheritJSON, err := json.Marshal(map[string]any{"mode": "inherit", "overrides": patch})
			require.NoError(t, err)
			for _, tc := range []struct {
				def   json.RawMessage
				fixed bool
			}{{inheritJSON, false}, {fixedJSON, true}} {
				def := tc.def
				save(def, &objects.ModelCard{Modalities: objects.ModelCardModalities{Input: []string{"text", "image", "pdf"}}, Limit: objects.ModelCardLimit{Context: 56000}},
					&objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(50000), MaxContextWindow: lo.ToPtr(64000)})
				got = read()
				require.Equal(t, []any{"text", "image"}, got["input_modalities"])
				require.Equal(t, float64(50000), got["context_window"])
				require.Equal(t, float64(64000), got["max_context_window"])
				save(def, &objects.ModelCard{}, nil)
				got = read()
				require.Equal(t, []any{"audio"}, got["input_modalities"])
				require.Equal(t, float64(40000), got["context_window"])
				require.Equal(t, float64(48000), got["max_context_window"])
				require.Equal(t, true, got["supports_search_tool"])
				require.Equal(t, true, got["node_repl_auto_review_required"])
				if tc.fixed {
					require.Equal(t, []any{"fixed-tool"}, got["experimental_supported_tools"])
				}
			}
			client.Channel.UpdateOneID(ch.ID).SetSettings(&objects.ChannelSettings{
				ModelProtocols: []objects.ModelProtocol{{Model: slug, APIFormats: []string{"openai/chat_completions"}}},
				ProtocolPolicy: &objects.ChannelProtocolPolicy{SameProtocolOnly: true},
			}).SaveX(ctx)
			reloadEnabledChannels(t, ctx, client, channels)
			_, omitted, err := NewCodexCatalogService(models, channels).Models(ctx, version)
			require.Error(t, err)
			require.Equal(t, 1, omitted, "explicit model declarations cannot grant a route")
		})
	}
}

func TestCodexExplicitDefaultsUseDeclaredOptions(t *testing.T) {
	ch := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Settings: &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierStandard}}
	m := ModelFacade{ID: "alias", TargetID: "test-model", ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{
		{Channel: ch, Models: []ChannelModelEntry{{ActualModel: "test-model"}}},
	}}
	profile := reasoningProfileFixture(t, m.TargetID, "low", "high")
	profile["service_tiers"] = []any{map[string]any{"id": "priority", "name": "Fast", "description": "Fast tier"}}
	d := codexDefinition{Models: map[string]codexModelProfile{m.TargetID: {
		SourceKind: "official_snapshot", SourceModelID: m.TargetID, Model: profile,
		Overrides: map[string]any{"default_reasoning_level": "high", "default_service_tier": "fast"},
	}}, Routes: []codexDefinitionRoute{{ChannelID: 1, ModelID: m.TargetID, Limits: map[string]any{
		"supported_reasoning_levels": []any{map[string]any{"effort": "low"}}, "service_tiers": []any{},
	}}}}
	read := func() map[string]any {
		t.Helper()
		got, err := d.catalogModel(t.Context(), m, catalogTestChannels(m), true)
		require.NoError(t, err)
		return got
	}
	got := read()
	require.Equal(t, "high", got["default_reasoning_level"])
	require.Equal(t, profile["supported_reasoning_levels"], got["supported_reasoning_levels"])
	require.Equal(t, "priority", got["default_service_tier"])
	require.Equal(t, profile["service_tiers"], got["service_tiers"])
	m.ConfiguredModel.ModelCard = &objects.ModelCard{ReasoningEfforts: []string{"low"}}
	got = read()
	require.Nil(t, got["default_reasoning_level"], "the outer model card excludes the handwritten default")
	require.Equal(t, profile["supported_reasoning_levels"].([]any)[:1], got["supported_reasoning_levels"])
}

func TestPublicCatalogPreviewAndSavedToolsRespectVisibleRoutes(t *testing.T) {
	const slug = "gpt-6.2-astra"
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	snapshots, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
	require.NoError(t, err)
	rich := reasoningProfileFixture(t, slug, "low", "high")
	rich["apply_patch_tool_type"], rich["minimal_client_version"] = "freeform", "0.166.0"
	snapshots.public.archive = &publicCatalogArchive{SHA256: strings.Repeat("a", 64), models: map[string]map[string]any{slug: rich}}
	models.codexSnapshots = snapshots
	plain := reasoningProfileFixture(t, slug, "none")
	body, err := json.Marshal(map[string]any{"models": []any{plain}})
	require.NoError(t, err)
	ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("mixed").SetBaseURL("https://example.invalid").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{slug}).SetDefaultTestModel(slug).SetStatus(channel.StatusEnabled).SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: body}, {APIFormat: "openai/chat_completions"}}).SaveX(ctx)
	createConfiguredModel(t, ctx, client, slug, model.TypeChat, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	reloadEnabledChannels(t, ctx, client, channels)
	configured := client.Model.Query().OnlyX(ctx)
	preview := func() objects.JSONRawMessage {
		t.Helper()
		raw, err := models.PreviewCodexCatalog(ctx, configured.ID, "0.167.0")
		require.NoError(t, err)
		return raw
	}
	save := func(raw objects.JSONRawMessage) {
		t.Helper()
		configured.Settings.CodexCatalog = raw
		_, err := models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{Settings: configured.Settings})
		require.NoError(t, err)
	}
	savedBeforeChange := preview()
	initial, err := decodeCodexDefinition(savedBeforeChange)
	require.NoError(t, err)
	require.Equal(t, "freeform", initial.Models[slug].Model["apply_patch_tool_type"])
	client.Channel.UpdateOneID(ch.ID).SetSettings(&objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: slug, APIFormats: []string{"openai/chat_completions"}}}}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	save(savedBeforeChange)
	_, omitted, err := svc.Models(ctx, "0.167.0")
	require.Error(t, err)
	require.Equal(t, 1, omitted)
	newPreview := preview()
	save(newPreview)
	published, omitted, err := svc.Models(ctx, "0.167.0")
	require.NoError(t, err)
	require.Zero(t, omitted)
	entries, err := parseCodexCatalog(published)
	require.NoError(t, err)
	require.Nil(t, entries[slug]["apply_patch_tool_type"])
	require.Equal(t, plain["supported_reasoning_levels"], entries[slug]["supported_reasoning_levels"])
}
