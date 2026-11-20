package biz

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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
