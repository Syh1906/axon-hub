package biz

import (
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
	require.Nil(t, snapshotCodexProfile(svc.Load(ctx, "0.170.0", ""), "test-model"))
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
	configured.Settings.CodexCatalog = fixed
	_, err = models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{Settings: configured.Settings})
	require.NoError(t, err)
	apply("Second")
	read := func() string {
		raw, omitted, err := NewCodexCatalogService(models, channels).Models(ctx, "0.171.0")
		require.NoError(t, err)
		require.Zero(t, omitted)
		catalog, err := parseCodexCatalog(raw)
		require.NoError(t, err)
		return catalog[slug]["display_name"].(string)
	}
	require.Equal(t, "First", read())
	configured.Settings.CodexCatalog = nil
	_, err = models.UpdateModel(ctx, configured.ID, &ent.UpdateModelInput{Settings: configured.Settings})
	require.NoError(t, err)
	require.Equal(t, "Second", read())
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

func TestImportedCodexProfileMergesOnlyDeclaredRouteCapabilities(t *testing.T) {
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
	snapshot := &codexSnapshot{clientVersion: "0.171.0", public: &publicCatalogArchive{Source: "url", models: base}}
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
	require.Equal(t, false, restricted["supports_search_tool"], "the visible route explicitly disables search")
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

func TestRepositoryCatalogServesKeyScopedModelAndSpeed(t *testing.T) {
	ctx, db, models, channels, _ := setupListEnabledModelsTest(t)
	file, err := os.ReadFile("../../../catalogs/codex/models.json")
	require.NoError(t, err)
	var source struct {
		ClientVersion string           `json:"client_version"`
		Models        []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(file, &source))
	require.GreaterOrEqual(t, len(source.Models), 2)
	base := source.Models[0]
	slug := base["slug"].(string)
	other := source.Models[1]["slug"].(string)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, db)
	require.NoError(t, err)
	models.codexSnapshots = svc
	in := CatalogImport{Source: "manual", Catalog: file}
	preview, err := svc.ImportCatalog(ctx, in, false)
	require.NoError(t, err)
	in.Preview = preview.Revision
	_, err = svc.ImportCatalog(ctx, in, true)
	require.NoError(t, err)
	ch := db.Channel.Create().SetName("fast").SetType(channel.TypeOpenaiResponses).SetSupportedModels([]string{slug, other}).
		SetDefaultTestModel(slug).SetBaseURL("https://example.invalid").SetStatus(channel.StatusEnabled).
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(`{"models":[]}`)}}).
		SetSettings(&objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true}}).
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SaveX(ctx)
	for _, id := range []string{slug, other} {
		createConfiguredModel(t, ctx, db, id, model.TypeChat, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: id}})
	}
	reloadEnabledChannels(t, ctx, db, channels)
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "selected", Profiles: []objects.APIKeyProfile{{Name: "selected", ModelIDs: []string{slug}, ChannelIDs: []int{ch.ID}}}}}
	raw, omitted, err := NewCodexCatalogService(models, channels).Models(contexts.WithAPIKey(ctx, key), source.ClientVersion)
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
	t.Run("only channel denies Responses clients", func(t *testing.T) {
		ch.Settings.ProtocolPolicy = &objects.ChannelProtocolPolicy{AllowedProtocols: []string{"openai/chat_completions"}}
		db.Channel.UpdateOneID(ch.ID).SetSettings(ch.Settings).SaveX(ctx)
		reloadEnabledChannels(t, ctx, db, channels)
		raw, omitted, err := NewCodexCatalogService(models, channels).Models(contexts.WithAPIKey(ctx, key), source.ClientVersion)
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
