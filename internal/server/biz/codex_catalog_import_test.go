package biz

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestCatalogImportSwitchesOnlyAfterMatchingPreview(t *testing.T) {
	ctx, db, _, _, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)
	const address = "https://catalog.example.org/models.json"
	payload := catalogFixture
	customCalls, officialCalls := 0, 0
	response := func(req *http.Request) (*http.Response, error) {
		require.Empty(t, req.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
	}
	svc.catalogTransport = snapshotTransport(func(req *http.Request) (*http.Response, error) {
		customCalls++
		require.Equal(t, address, req.URL.String())
		return response(req)
	})
	svc.httpClient.Transport = snapshotTransport(func(req *http.Request) (*http.Response, error) {
		officialCalls++
		require.Equal(t, publicCatalogURL, req.URL.String())
		return response(req)
	})
	status := func() PublicCatalogStatus { v, err := svc.PublicStatus(ctx); require.NoError(t, err); return v }
	apply := func(input CatalogImport) {
		v, err := svc.ImportCatalog(ctx, input, false)
		require.NoError(t, err)
		input.Preview = v.Revision
		_, err = svc.ImportCatalog(ctx, input, true)
		require.NoError(t, err)
	}
	input := CatalogImport{Source: "url", URL: address, Subscribe: true}
	preview, err := svc.ImportCatalog(ctx, input, false)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	require.Equal(t, "official", status().Source)
	input.Preview = preview.Revision
	payload = strings.Replace(catalogFixture, `"priority":1`, `"priority":2`, 1)
	_, err = svc.ImportCatalog(ctx, input, true)
	require.ErrorIs(t, err, ErrCatalogPreviewChanged)
	require.Equal(t, "official", status().Source)
	apply(input)
	require.Equal(t, "url", status().Source)
	require.Zero(t, status().Changed)
	payload = catalogFixture
	require.NoError(t, svc.CheckPublicCatalog(ctx))
	require.Equal(t, 1, status().Changed)
	active := snapshotCodexProfile(svc.Load(ctx, "0.171.0", ""), "test-model")
	require.NotNil(t, active)
	require.Equal(t, "imported_catalog", active.SourceKind)
	require.Equal(t, float64(2), active.Model["priority"])
	require.Zero(t, officialCalls)
	payload = "broken"
	require.Error(t, svc.CheckPublicCatalog(ctx))
	require.Equal(t, float64(2), snapshotCodexProfile(svc.Load(ctx, "0.171.0", ""), "test-model").Model["priority"])
	local := CatalogImport{Source: "manual", Catalog: json.RawMessage(strings.Replace(catalogFixture, `{"models"`, `{"identity":"omit-me","client_version":"0.171.0","models"`, 1))}
	apply(local)
	count := customCalls
	require.NoError(t, svc.checkPublicIfDue(ctx))
	require.NoError(t, svc.CheckPublicCatalog(ctx))
	require.Equal(t, count, customCalls)
	require.Zero(t, officialCalls)
	require.NotNil(t, snapshotCodexProfile(svc.Load(ctx, "0.170.0", ""), "test-model"))
	require.NotContains(t, string(svc.public.archive.Body), "omit-me")
	restarted, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, db)
	require.NoError(t, err)
	require.NoError(t, restarted.startPublicCatalog(ctx, nil))
	require.Equal(t, "manual", restarted.public.archive.source())
	payload = catalogFixture
	apply(CatalogImport{Source: "url", URL: address})
	require.Equal(t, "manual", status().Source)
	require.Empty(t, status().URL)
	count = customCalls
	require.NoError(t, svc.CheckPublicCatalog(ctx))
	require.Equal(t, count, customCalls)
	apply(CatalogImport{Source: "official"})
	require.Equal(t, "official", status().Source)
	require.NoError(t, svc.CheckPublicCatalog(ctx))
	require.Equal(t, 3, officialCalls)
}

func TestImportedCatalogLeavesFixedDefinitionsAlone(t *testing.T) {
	ctx, db, models, channels, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, db)
	require.NoError(t, err)
	models.codexSnapshots = svc
	const slug = "test-model"
	ch := db.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("catalog").SetBaseURL("https://example.invalid").
		SetSupportedModels([]string{slug}).SetDefaultTestModel(slug).SetStatus(channel.StatusEnabled).
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(catalogFixture)}}).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SaveX(ctx)
	createConfiguredModel(t, ctx, db, slug, model.TypeChat, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: slug}})
	configured := db.Model.Query().OnlyX(ctx)
	reloadEnabledChannels(t, ctx, db, channels)
	apply := func(name string) {
		input := CatalogImport{Source: "manual", Catalog: json.RawMessage(strings.Replace(catalogFixture, `"display_name":"Test"`, `"display_name":"`+name+`"`, 1))}
		p, err := svc.ImportCatalog(ctx, input, false)
		require.NoError(t, err)
		input.Preview = p.Revision
		_, err = svc.ImportCatalog(ctx, input, true)
		require.NoError(t, err)
	}
	apply("First")
	fixed, err := models.PreviewCodexCatalog(ctx, configured.ID, "0.171.0")
	require.NoError(t, err)
	definition, err := decodeCodexDefinition(fixed)
	require.NoError(t, err)
	require.Equal(t, "imported_catalog", definition.Models[slug].SourceKind)
	// Edit the same per-model JSON object that the admin preview exposes.
	require.Equal(t, []any{"text"}, definition.Models[slug].CatalogDefaults["input_modalities"])
	definition.Models[slug].Model["input_modalities"] = []any{"text", "audio"}
	edited, err := json.Marshal(definition)
	require.NoError(t, err)
	configured.Settings.CodexCatalog = edited
	_, err = models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{Settings: configured.Settings, ModelCard: &objects.ModelCard{
		Modalities: objects.ModelCardModalities{Input: []string{"text", "image"}},
		Limit:      objects.ModelCardLimit{Context: 87000},
	}})
	require.NoError(t, err)
	apply("Second")
	read := func() map[string]any {
		raw, omitted, err := NewCodexCatalogService(models, channels).Models(ctx, "0.171.0")
		require.NoError(t, err)
		require.Zero(t, omitted)
		catalog, err := parseCodexCatalog(raw)
		require.NoError(t, err)
		return catalog[slug]
	}
	got := read()
	require.Equal(t, "First", got["display_name"])
	require.Equal(t, []any{"text", "image"}, got["input_modalities"])
	require.Equal(t, float64(87000), got["max_context_window"])
	_, err = models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{ModelCard: &objects.ModelCard{}})
	require.NoError(t, err)
	require.Equal(t, []any{"text", "audio"}, read()["input_modalities"])
	configured.Settings.CodexCatalog = nil
	_, err = models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{Settings: configured.Settings})
	require.NoError(t, err)
	got = read()
	require.Equal(t, "Second", got["display_name"])
	require.Equal(t, []any{"text"}, got["input_modalities"])
}

func TestImportedCatalogHonorsModelAndChannelOverrides(t *testing.T) {
	ctx, db, _, _, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, db)
	require.NoError(t, err)
	input := CatalogImport{Source: "manual", Catalog: json.RawMessage(catalogFixture)}
	preview, err := svc.ImportCatalog(ctx, input, false)
	require.NoError(t, err)
	input.Preview = preview.Revision
	_, err = svc.ImportCatalog(ctx, input, true)
	require.NoError(t, err)
	snap := svc.Load(ctx, "0.171.0", "")
	const slug = "test-model"
	route := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Settings: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: slug, MaxContextWindow: lo.ToPtr(19000)}}}}
	m := ModelFacade{ID: slug, TargetID: slug, ConfiguredModel: &ent.Model{Settings: &objects.ModelSettings{ContextSettings: &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(16000)}}}, Connections: []*ModelChannelConnection{{Channel: route, Models: []ChannelModelEntry{{ActualModel: slug}}}}}
	got, err := automaticCodexModel(ctx, m, m.Connections, nil, catalogTestChannels(m), "0.171.0", snap, &codexDefinition{Mode: "inherit", Overrides: map[string]any{"display_name": "Site name", "priority": float64(42)}})
	require.NoError(t, err)
	require.Equal(t, "Site name", got["display_name"])
	require.Equal(t, float64(42), got["priority"])
	require.Equal(t, float64(16000), got["context_window"])
	require.Equal(t, float64(19000), got["max_context_window"])
	require.Equal(t, "Test", snap.public.models[slug]["display_name"])
}

func TestOfficialCodexProfileMergesOnlyDeclaredRouteCapabilities(t *testing.T) {
	const slug = "test-model"
	base, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	b := base[slug]
	b["supports_search_tool"] = true
	b["node_repl_auto_review_required"] = true
	b["input_modalities"] = []any{"text", "image", "audio"}
	b["experimental_supported_tools"] = []any{"example-tool"}
	b["additional_speed_tiers"] = []any{"fast"}
	b["service_tiers"] = []any{map[string]any{"id": "priority", "name": "Base label", "description": "Base description"}}
	b["default_service_tier"] = "priority"
	b["minimal_client_version"] = "0.171.0"
	snapshot := &codexSnapshot{clientVersion: "0.171.0", public: &publicCatalogArchive{Source: "official", models: base}}
	ch := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/responses"}}}
	keyRoute := &ModelChannelConnection{Channel: ch, Models: []ChannelModelEntry{{ActualModel: slug}}}
	m := ModelFacade{ID: slug, TargetID: slug, ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{keyRoute}}
	local := strings.ReplaceAll(catalogFixture, `"experimental_supported_tools":[],"input_modalities":["text"],`, "")
	local = strings.Replace(local, `"upgrade":null`, `"upgrade":null,"node_repl_auto_review_required":false,"additional_speed_tiers":["ultrafast"]`, 1)
	sources, err := parseRemoteCodexCatalog([]byte(local))
	require.NoError(t, err)
	require.NotContains(t, sources[slug], "supports_search_tool")
	require.NotContains(t, sources[slug], "experimental_supported_tools")
	byChannel := map[int]map[string]map[string]any{1: sources}
	read := func() map[string]any {
		got, err := automaticCodexModel(t.Context(), m, m.Connections, byChannel, catalogTestChannels(m), "0.171.0", snapshot, nil)
		require.NoError(t, err)
		return got
	}
	ch.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true, Ultrafast: true}}
	got := read()
	for _, k := range []string{"display_name", "priority", "input_modalities", "experimental_supported_tools", "node_repl_auto_review_required", "supports_search_tool", "service_tiers", "additional_speed_tiers", "default_service_tier"} {
		require.Equal(t, b[k], got[k], k)
	}
	sources[slug]["input_modalities"] = []any{"text"}
	sources[slug]["experimental_supported_tools"] = []any{}
	sources[slug]["supports_search_tool"] = false
	restricted, err := automaticCodexModel(t.Context(), m, m.Connections, byChannel, catalogTestChannels(m), "0.171.0", snapshot,
		&codexDefinition{Mode: "inherit", Overrides: map[string]any{"node_repl_auto_review_required": false, "supports_search_tool": true}})
	require.NoError(t, err)
	require.Equal(t, true, restricted["node_repl_auto_review_required"])
	require.Equal(t, true, restricted["supports_search_tool"], "an explicit model override takes precedence over the route report")
	ch.Settings = nil
	got = read()
	require.Equal(t, []any{"text"}, got["input_modalities"])
	require.Empty(t, got["experimental_supported_tools"])
	require.Equal(t, false, got["supports_search_tool"])
	require.Equal(t, true, got["node_repl_auto_review_required"])
	require.Empty(t, got["additional_speed_tiers"], "a channel-only Ultrafast tier cannot add a base tier")
	require.Nil(t, got["default_service_tier"])
	sources[slug]["additional_speed_tiers"] = []any{"fast", "ultrafast"}
	require.Equal(t, b["service_tiers"], read()["service_tiers"], "inherited route actually declares Fast")
	b["default_service_tier"] = "fast"
	require.Equal(t, "priority", read()["default_service_tier"], "the default names an actual service tier ID")
	ch.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierStandard}
	require.Empty(t, read()["service_tiers"])
}

func TestImportedCatalogUsesModelPageAndAllVisibleRoutes(t *testing.T) {
	const slug = "test-model"
	profile := reasoningProfileFixture(t, slug, "low", "high")
	profile["input_modalities"] = []any{"text", "image", "audio"}
	profile["supports_search_tool"] = true
	profile["max_context_window"] = float64(96000)
	profile["additional_speed_tiers"] = []any{"fast", "ultrafast"}
	report := reasoningProfileFixture(t, slug, "low")
	report["input_modalities"] = []any{"text"}
	report["supports_search_tool"] = false
	report["context_window"] = float64(1000)
	ch1 := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Settings: &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierStandard}}
	ch2 := &ent.Channel{ID: 2, Type: channel.TypeOpenaiResponses}
	model := ModelFacade{ID: "key-alias", TargetID: slug, ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{
		{Channel: ch1, Models: []ChannelModelEntry{{ActualModel: slug}}},
		{Channel: ch2, Models: []ChannelModelEntry{{ActualModel: slug}}},
	}}
	sources := map[int]map[string]map[string]any{1: {slug: report}, 2: {slug: report}}
	for _, source := range []string{"url", "manual"} {
		t.Run(source, func(t *testing.T) {
			m := model
			m.ConfiguredModel = &ent.Model{}
			snapshot := &codexSnapshot{clientVersion: "0.171.0", public: &publicCatalogArchive{Source: source, models: map[string]map[string]any{slug: profile}}}
			read := func(inherited *codexDefinition) map[string]any {
				t.Helper()
				got, err := automaticCodexModel(t.Context(), m, model.Connections, sources, catalogTestChannels(model), "0.171.0", snapshot, inherited)
				require.NoError(t, err)
				return got
			}
			got := read(nil)
			for _, field := range []string{"input_modalities", "supports_search_tool", "context_window", "additional_speed_tiers", "supported_reasoning_levels"} {
				require.Equal(t, profile[field], got[field], field)
			}
			require.Equal(t, m.ID, got["slug"])
			m.Connections = model.Connections[:1]
			require.Empty(t, read(nil)["additional_speed_tiers"])
			forced := read(&codexDefinition{Mode: "inherit", Overrides: map[string]any{"additional_speed_tiers": []any{"fast"}}})
			require.Equal(t, []any{"fast"}, forced["additional_speed_tiers"])
			m.Connections = model.Connections[1:]
			require.Equal(t, profile["additional_speed_tiers"], read(nil)["additional_speed_tiers"])
			ch2.Settings = &objects.ChannelSettings{BodyOverrideOperations: []objects.OverrideOperation{{
				Op: objects.OverrideOpSet, Path: "service_tier", Value: "default", Condition: "{{ .Metadata.slow }}",
			}}}
			require.Equal(t, profile["additional_speed_tiers"], read(nil)["additional_speed_tiers"])
			ch2.Settings.BodyOverrideOperations[0].Condition = ""
			require.Empty(t, read(nil)["additional_speed_tiers"])
			for _, condition := range []string{"{{true}}", `{{eq .RequestModel "test-model"}}`, `{{eq .Model "test-model"}}`} {
				ch2.Settings.BodyOverrideOperations[0].Condition = condition
				require.Empty(t, read(nil)["additional_speed_tiers"], condition)
			}
			ch2.Settings.BodyOverrideOperations[0].Condition = `{{eq .RequestModel "key-alias"}}`
			require.Equal(t, profile["additional_speed_tiers"], read(nil)["additional_speed_tiers"])
			ch2.Settings.BodyOverrideOperations[0].Condition = "{{false}}"
			require.Equal(t, profile["additional_speed_tiers"], read(nil)["additional_speed_tiers"])
			ch2.Settings.BodyOverrideOperations = []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: "service_tier", Value: `{{if .Metadata.slow}}default{{else}}priority{{end}}`}}
			require.Contains(t, read(nil)["additional_speed_tiers"], "fast")
			ch2.Settings.BodyOverrideOperations = []objects.OverrideOperation{{Op: objects.OverrideOpCopy, From: "custom_tier", To: "service_tier"}}
			require.Contains(t, read(nil)["additional_speed_tiers"], "fast")
			ch2.Settings = nil

			m.ConfiguredModel.ModelCard = &objects.ModelCard{
				Modalities:       objects.ModelCardModalities{Input: []string{"text", "image", "pdf"}},
				ReasoningEfforts: []string{"high"}, Limit: objects.ModelCardLimit{Context: 64000},
			}
			m.ConfiguredModel.Settings = &objects.ModelSettings{ContextSettings: &objects.ModelContextSettings{DefaultContextWindow: lo.ToPtr(42000)}}
			got = read(nil)
			require.Equal(t, []any{"text", "image"}, got["input_modalities"])
			require.Equal(t, profile["supported_reasoning_levels"].([]any)[1:], got["supported_reasoning_levels"])
			require.Equal(t, float64(42000), got["context_window"])
			require.Equal(t, float64(64000), got["max_context_window"])
			got = read(&codexDefinition{Mode: "inherit", Overrides: map[string]any{"input_modalities": []any{"audio"}, "max_context_window": float64(48000)}})
			require.Equal(t, []any{"text", "image"}, got["input_modalities"])
			require.Equal(t, float64(64000), got["max_context_window"])
			m.ConfiguredModel.ModelCard.Limit.Context = 0
			m.ConfiguredModel.Settings.ContextSettings.DefaultContextWindow = lo.ToPtr(120000)
			got = read(&codexDefinition{Mode: "inherit", Overrides: map[string]any{"max_context_window": float64(128000)}})
			require.Equal(t, float64(120000), got["context_window"])
			require.Equal(t, []any{"text", "image", "audio"}, profile["input_modalities"])
			m.ConfiguredModel.ModelCard.Reasoning = objects.ModelCardReasoning{SupportDeclared: true}
			got = read(nil)
			require.Empty(t, got["supported_reasoning_levels"])
			require.Nil(t, got["default_reasoning_level"])
		})
	}
}

func TestFixedImportedProfileOnlyAppliesUnanimousLimits(t *testing.T) {
	const slug = "test-model"
	profile := reasoningProfileFixture(t, slug, "low", "high")
	profile["input_modalities"] = []any{"text", "image", "audio"}
	profile["context_window"], profile["max_context_window"] = float64(72000), float64(96000)
	ch1 := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Settings: &objects.ChannelSettings{ModelContexts: []objects.ChannelModelContext{{ModelID: slug, MaxContextWindow: lo.ToPtr(16000)}}}}
	ch2 := &ent.Channel{ID: 2, Type: channel.TypeOpenaiResponses}
	m := ModelFacade{ID: slug, TargetID: slug, ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{
		{Channel: ch1, Models: []ChannelModelEntry{{ActualModel: slug}}},
		{Channel: ch2, Models: []ChannelModelEntry{{ActualModel: slug}}},
	}}
	def := codexDefinition{SourceKind: "official_public", Models: map[string]codexModelProfile{slug: {SourceKind: importedCatalogKind, Model: profile, CatalogDefaults: codexRouteCapabilities(profile)}}, Routes: []codexDefinitionRoute{
		{ChannelID: 1, ModelID: slug, Limits: map[string]any{"input_modalities": []any{"text", "image"}}},
		{ChannelID: 2, ModelID: slug, Capabilities: map[string]any{"input_modalities": []any{"text"}, "node_repl_auto_review_required": true}},
	}}
	read := func() map[string]any {
		t.Helper()
		got, err := def.catalogModel(t.Context(), m, catalogTestChannels(m), true)
		require.NoError(t, err)
		return got
	}
	got := read()
	require.Equal(t, profile["input_modalities"], got["input_modalities"])
	require.Equal(t, profile["max_context_window"], got["max_context_window"])
	require.Equal(t, true, got["node_repl_auto_review_required"])
	def.Routes[1].Limits = map[string]any{"input_modalities": []any{"text"}, "max_context_window": float64(32000)}
	got = read()
	require.Equal(t, []any{"text", "image"}, got["input_modalities"])
	require.Equal(t, float64(32000), got["max_context_window"])
	require.Equal(t, float64(32000), got["context_window"])
	m.Connections = m.Connections[1:]
	require.Equal(t, []any{"text"}, read()["input_modalities"])
	perModel := def.Models[slug]
	perModel.Overrides = map[string]any{"input_modalities": profile["input_modalities"], "context_window": float64(65000)}
	def.Models[slug] = perModel
	got = read()
	require.Equal(t, profile["input_modalities"], got["input_modalities"], "explicit even when identical to the catalog defaults")
	require.Equal(t, float64(65000), got["context_window"])
	require.GreaterOrEqual(t, got["max_context_window"].(float64), got["context_window"].(float64))
	perModel.Overrides = nil
	def.Models[slug] = perModel
	delete(def.Routes[1].Limits, "input_modalities")
	m.ConfiguredModel.ModelCard = &objects.ModelCard{Modalities: objects.ModelCardModalities{Input: []string{"audio"}}}
	require.Equal(t, []any{"audio"}, read()["input_modalities"], "a saved imported profile uses the current model card")
	legacy := def.Models[slug]
	legacy.CatalogDefaults = nil
	def.Models[slug] = legacy
	require.Equal(t, []any{"audio"}, read()["input_modalities"], "current model card overrides a legacy fixed definition")
	m.ConfiguredModel.ModelCard = nil
	require.Equal(t, profile["input_modalities"], read()["input_modalities"], "old definitions retain their confirmed values when the outer form is empty")
}

func TestImportedProfileRequiresACompatibleRoute(t *testing.T) {
	const slug = "test-model"
	profile := reasoningProfileFixture(t, slug, "high")
	profile["apply_patch_tool_type"] = "freeform"
	snapshot := &codexSnapshot{clientVersion: "0.171.0", public: &publicCatalogArchive{Source: "manual", models: map[string]map[string]any{slug: profile}}}
	ch := &ent.Channel{ID: 1, Type: channel.TypeOpenai, Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/chat_completions"}}}
	m := ModelFacade{ID: slug, TargetID: slug, ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{{Channel: ch, Models: []ChannelModelEntry{{ActualModel: slug}}}}}
	sources := map[int]map[string]map[string]any{1: {slug: reasoningProfileFixture(t, slug, "low")}}
	_, err := automaticCodexModel(t.Context(), m, m.Connections, sources, catalogTestChannels(m), "0.171.0", snapshot, nil)
	require.ErrorContains(t, err, "no visible route")
}

func TestImportedCatalogCountsOnlyReachableResponsesRoutes(t *testing.T) {
	ctx, db, models, channels, _ := setupListEnabledModelsTest(t)
	snapshots, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, db)
	require.NoError(t, err)
	models.codexSnapshots = snapshots
	const slug = "test-model"
	applyTestCatalog(t, ctx, snapshots, CatalogImport{Source: "manual", Catalog: json.RawMessage(strings.Replace(catalogFixture, `"input_modalities":["text"]`, `"input_modalities":["text","image"]`, 1))})
	var associations []*objects.ModelAssociation
	var channelIDs []int
	for _, name := range []string{"limited", "conditional"} {
		ch := db.Channel.Create().SetName(name).SetType(channel.TypeOpenaiResponses).SetSupportedModels([]string{slug}).
			SetDefaultTestModel(slug).SetBaseURL("https://example.invalid").SetStatus(channel.StatusEnabled).
			SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
			SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(catalogFixture)}, {APIFormat: "openai/chat_completions"}}).SaveX(ctx)
		channelIDs = append(channelIDs, ch.ID)
		associations = append(associations, &objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	}
	reloadEnabledChannels(t, ctx, db, channels)
	createConfiguredModel(t, ctx, db, slug, model.TypeChat, associations[0])
	m := db.Model.Query().OnlyX(ctx)
	m.Settings.Associations = associations
	m = db.Model.UpdateOne(m).SetSettings(m.Settings).SaveX(ctx)
	raw, err := models.PreviewCodexCatalog(ctx, m.ID, "0.171.0")
	require.NoError(t, err)
	d, err := decodeCodexDefinition(raw)
	require.NoError(t, err)
	for i, r := range d.Routes {
		if r.ChannelID == channelIDs[0] {
			d.Routes[i].Limits = map[string]any{"input_modalities": []any{"text"}}
		}
	}
	m.Settings.CodexCatalog, err = json.Marshal(d)
	require.NoError(t, err)
	read := func(format string) []any {
		t.Helper()
		m.Settings.Associations[1].When = &objects.ModelAssociationWhen{Enabled: true, Condition: &objects.Condition{
			Type: objects.ConditionTypeGroup, Logic: "and", Conditions: []objects.Condition{{
				Type: objects.ConditionTypeCondition, Field: "request_format", Operator: "eq", Value: format,
			}},
		}}
		_, err = models.UpdateModel(ctx, m.ID, &ent.UpdateModelInput{Settings: m.Settings})
		require.NoError(t, err)
		body, omitted, err := NewCodexCatalogService(models, channels).Models(ctx, "0.171.0")
		require.NoError(t, err)
		require.Zero(t, omitted)
		entries, err := parseCodexCatalog(body)
		require.NoError(t, err)
		return entries[slug]["input_modalities"].([]any)
	}
	require.Equal(t, []any{"text"}, read("anthropic/messages"))
	require.Equal(t, []any{"text", "image"}, read("openai/responses-ws"))
	db.Channel.UpdateOneID(channelIDs[1]).SetSettings(&objects.ChannelSettings{
		ModelProtocols: []objects.ModelProtocol{{Model: slug, APIFormats: []string{"openai/chat_completions"}}},
		ProtocolPolicy: &objects.ChannelProtocolPolicy{SameProtocolOnly: true},
	}).SaveX(ctx)
	reloadEnabledChannels(t, ctx, db, channels)
	require.Equal(t, []any{"text"}, read("openai/responses"))
}

func TestRepositoryCatalogServesKeyScopedModelAndSpeed(t *testing.T) {
	ctx, db, models, channels, _ := setupListEnabledModelsTest(t)
	file, err := os.ReadFile("../../../catalogs/codex/models.json")
	require.NoError(t, err)
	const slug, other = "gpt-6.2-sol", "gpt-6.2-astra"
	const clientVersion = "0.166.0"
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)
	models.codexSnapshots = svc
	svc.catalogTransport = snapshotTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(file))}, nil
	})
	applyTestCatalog(t, ctx, svc, CatalogImport{Source: "url", Subscribe: true, URL: "https://catalog.example.invalid/models.json"})
	source := svc.cachedPublicState().archive
	// Older AxonHub releases use the capture version as a minimum for every model.
	require.Empty(t, source.ClientVersion)
	base := source.models[slug]
	require.Equal(t, []any{"text", "image"}, base["input_modalities"])
	ch := db.Channel.Create().SetName("fast").SetType(channel.TypeOpenaiResponses).SetSupportedModels([]string{slug, other}).
		SetDefaultTestModel(slug).SetBaseURL("https://example.invalid").SetStatus(channel.StatusEnabled).
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(strings.ReplaceAll(catalogFixture, "test-model", slug))}}).
		SetSettings(&objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true}}).
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SaveX(ctx)
	for _, id := range []string{slug, other} {
		createConfiguredModel(t, ctx, db, id, model.TypeChat, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: id}})
	}
	reloadEnabledChannels(t, ctx, db, channels)
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "selected", Profiles: []objects.APIKeyProfile{{Name: "selected", ModelIDs: []string{slug}, ChannelIDs: []int{ch.ID}}}}}
	raw, omitted, err := NewCodexCatalogService(models, channels).Models(contexts.WithAPIKey(ctx, key), clientVersion)
	require.NoError(t, err)
	require.Zero(t, omitted)
	var wire struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	require.Len(t, wire.Models, 1)
	got := wire.Models[0]
	for _, field := range []string{"slug", "display_name", "priority", "model_messages", "shell_type", "tool_mode", "multi_agent_version", "multi_agent_reasoning_effort", "experimental_supported_tools", "input_modalities", "supports_search_tool", "node_repl_auto_review_required", "supported_reasoning_levels"} {
		require.Equal(t, base[field], got[field], field)
	}
	for _, field := range []string{"service_tiers", "additional_speed_tiers"} {
		expected := []any{}
		candidates, _ := base[field].([]any)
		for _, item := range candidates {
			if codexTierID(item) != "ultrafast" {
				expected = append(expected, item)
			}
		}
		values, _ := got[field].([]any)
		require.Equal(t, expected, append([]any{}, values...), field)
	}
	t.Run("older client gets Astra even when the channel only describes Sol", func(t *testing.T) {
		astraKey := &ent.APIKey{Profiles: &objects.APIKeyProfiles{
			ActiveProfile: "astra",
			Profiles:      []objects.APIKeyProfile{{Name: "astra", ModelIDs: []string{other}, ChannelIDs: []int{ch.ID}}},
		}}
		raw, omitted, err := NewCodexCatalogService(models, channels).Models(contexts.WithAPIKey(ctx, astraKey), clientVersion)
		require.NoError(t, err)
		require.Zero(t, omitted)
		entries, err := parseCodexCatalog(raw)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Contains(t, entries, other)
		require.Equal(t, importedCatalogKind, entries[other]["axonhub_catalog"].(map[string]any)["source"])
	})
	t.Run("model page inputs override the imported repository entry", func(t *testing.T) {
		configured := db.Model.Query().Where(model.ModelIDEQ(slug)).OnlyX(ctx)
		_, err := models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{ModelCard: &objects.ModelCard{
			Modalities: objects.ModelCardModalities{Input: []string{"text", "pdf"}},
		}})
		require.NoError(t, err)
		body, omitted, err := NewCodexCatalogService(models, channels).Models(contexts.WithAPIKey(ctx, key), clientVersion)
		require.NoError(t, err)
		require.Zero(t, omitted)
		entries, err := parseCodexCatalog(body)
		require.NoError(t, err)
		require.Equal(t, []any{"text"}, entries[slug]["input_modalities"])
	})
	t.Run("only channel denies Responses clients", func(t *testing.T) {
		ch.Settings.ProtocolPolicy = &objects.ChannelProtocolPolicy{AllowedProtocols: []string{"openai/chat_completions"}}
		db.Channel.UpdateOneID(ch.ID).SetSettings(ch.Settings).SaveX(ctx)
		reloadEnabledChannels(t, ctx, db, channels)
		raw, omitted, err := NewCodexCatalogService(models, channels).Models(contexts.WithAPIKey(ctx, key), clientVersion)
		require.NoError(t, err)
		require.Zero(t, omitted)
		require.JSONEq(t, `{"models":[]}`, string(raw))
	})
}

func TestFixedCodexTierEvidenceStaysOnItsSourceRoute(t *testing.T) {
	const slug = "test-model"
	base, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	profile := base[slug]
	profile["additional_speed_tiers"] = []any{"fast", "ultrafast"}
	ch := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/responses"}}}
	m := ModelFacade{ID: "alias", TargetID: slug, ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{{Channel: ch, Models: []ChannelModelEntry{{ActualModel: slug}}}}}
	d := codexDefinition{Model: profile, SourceChannelID: 1, SourceModelID: slug}
	read := func() any {
		got, err := d.catalogModel(t.Context(), m, catalogTestChannels(m), true)
		require.NoError(t, err)
		return got["additional_speed_tiers"]
	}
	require.Equal(t, []any{"fast", "ultrafast"}, read())
	d.Routes = []codexDefinitionRoute{{ChannelID: 1, ModelID: slug, Limits: map[string]any{"additional_speed_tiers": []any{"fast"}}}}
	require.Equal(t, []any{"fast"}, read())
	d.Routes[0].Capabilities = map[string]any{"context_window": float64(16000)}
	require.Equal(t, []any{"fast"}, read())
	d.Routes[0].Capabilities["service_tiers"] = []any{}
	require.Empty(t, read(), "an explicitly empty route declaration is authoritative in inherit mode")
	delete(d.Routes[0].Capabilities, "service_tiers")
	d.SourceChannelID = 2
	require.Empty(t, read())
	d.SourceChannelID = 1
	d.SourceModelID = "another-model"
	require.Empty(t, read())
}

func TestCodexFixedRouteSearchOverride(t *testing.T) {
	base, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	profile := base["test-model"]
	profile["supports_search_tool"] = false
	profile["node_repl_auto_review_required"] = true
	ch := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses}
	m := ModelFacade{ID: "test-model", ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{{Channel: ch, Models: []ChannelModelEntry{{ActualModel: "test-model"}}}}}
	d := codexDefinition{Model: profile, Routes: []codexDefinitionRoute{{ChannelID: 1, ModelID: "test-model", Capabilities: map[string]any{"supports_search_tool": true, "node_repl_auto_review_required": false}}}}
	for _, fixed := range []bool{true, false} {
		got, err := d.catalogModel(t.Context(), m, catalogTestChannels(m), fixed)
		require.NoError(t, err)
		require.Equal(t, fixed, got["supports_search_tool"])
		require.Equal(t, true, got["node_repl_auto_review_required"])
	}
	d.Routes[0].Limits = map[string]any{"supports_search_tool": false}
	got, err := d.catalogModel(t.Context(), m, catalogTestChannels(m), true)
	require.NoError(t, err)
	require.Equal(t, false, got["supports_search_tool"])
}

func TestCodexTiersRespectOutboundBodyOverrides(t *testing.T) {
	base, err := parseCodexCatalog([]byte(catalogFixture))
	require.NoError(t, err)
	const slug = "test-model"
	profile := base[slug]
	profile["additional_speed_tiers"] = []any{"fast", "ultrafast"}
	profile["service_tiers"] = []any{map[string]any{"id": "priority", "name": "Fast", "description": "Fast"}, map[string]any{"id": "ultrafast", "name": "Ultra", "description": "Ultra"}}
	profile["default_service_tier"] = "priority"
	ch := &ent.Channel{ID: 1, Type: channel.TypeOpenaiResponses, Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/responses"}}}
	m := ModelFacade{ID: slug, ConfiguredModel: &ent.Model{}, Connections: []*ModelChannelConnection{{Channel: ch, Models: []ChannelModelEntry{{ActualModel: slug}}}}}
	d := codexDefinition{Model: profile, SourceChannelID: 1, SourceModelID: slug}
	set := func(v string) objects.OverrideOperation {
		return objects.OverrideOperation{Op: objects.OverrideOpSet, Path: "service_tier", Value: v}
	}
	for _, tc := range []struct {
		name   string
		ops    []objects.OverrideOperation
		legacy string
		mode   objects.ResponsesServiceTierOverride
		want   []any
	}{
		{"inherit set standard", []objects.OverrideOperation{set("default")}, "", "", []any{}},
		{"forced object key", []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: ":service_tier", Value: "default"}}, "", "", []any{}},
		{"nested field", []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: "service_tier.name", Value: "default"}}, "", "", []any{}},
		{"different key", []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: `service_tier\.name`, Value: "default"}}, "", "", []any{"fast", "ultrafast"}},
		{"conditional downgrade", []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: "service_tier", Value: "default", Condition: "{{ .Metadata.downgrade }}"}}, "", "", []any{}},
		{"inherit delete", []objects.OverrideOperation{{Op: objects.OverrideOpDelete, Path: "service_tier"}}, "", "", []any{}},
		{"inherit only fast", []objects.OverrideOperation{set("default"), set("priority")}, "", "", []any{"fast"}},
		{"legacy", nil, `{"service_tier":"default"}`, "", []any{}},
		{"explicit channel wins", []objects.OverrideOperation{set("default")}, "", objects.ResponsesServiceTierFast, []any{"fast"}},
		{"default if absent", []objects.OverrideOperation{{Op: objects.OverrideOpSetIfAbsent, Path: "service_tier", Value: "default"}}, "", "", []any{"fast", "ultrafast"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: tc.mode, BodyOverrideOperations: tc.ops, OverrideParameters: tc.legacy}
			got, err := d.catalogModel(t.Context(), m, catalogTestChannels(m), true)
			require.NoError(t, err)
			require.Equal(t, tc.want, got["additional_speed_tiers"])
			require.Len(t, got["service_tiers"], len(tc.want))
			if len(tc.want) == 0 {
				require.Nil(t, got["default_service_tier"])
			}
		})
	}
}
