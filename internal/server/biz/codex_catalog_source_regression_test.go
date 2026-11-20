package biz

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func applyTestCatalog(t *testing.T, ctx context.Context, s *CodexSnapshotService, input CatalogImport) CatalogPreview {
	t.Helper()
	p, err := s.ImportCatalog(ctx, input, false)
	require.NoError(t, err)
	input.Preview = p.Revision
	_, err = s.ImportCatalog(ctx, input, true)
	require.NoError(t, err)
	return p
}

func TestImportedCatalogServesOlderClientsFromLocalRelease(t *testing.T) {
	ctx, db, models, channels, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)
	models.codexSnapshots = svc
	const slug = "gpt-6.1-sol"
	// A previous process archived a release newer than the bundled 0.167.0.
	archiver, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)
	archiver.httpClient.Transport = snapshotTransport(func(*http.Request) (*http.Response, error) {
		body := strings.ReplaceAll(catalogFixture, "test-model", slug)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	require.NotNil(t, archiver.Load(ctx, "0.169.0", "0.169.0"))
	// No channel definition for this model, so a client needs the selected baseline or a compatible release.
	ch := db.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("test").SetBaseURL("https://example.invalid").
		SetSupportedModels([]string{slug}).SetDefaultTestModel(slug).SetStatus(channel.StatusEnabled).
		SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", CatalogJSON: []byte(catalogFixture)}}).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SaveX(ctx)
	createConfiguredModel(t, ctx, db, slug, model.TypeChat, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: slug}})
	reloadEnabledChannels(t, ctx, db, channels)
	svc.httpClient.Transport = snapshotTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected remote official fetch")
		return nil, nil
	})
	body := strings.Replace(strings.ReplaceAll(catalogFixture, "test-model", slug), `{"models":`, `{"client_version":"0.171.0","models":`, 1)
	applyTestCatalog(t, ctx, svc, CatalogImport{Source: "manual", Catalog: json.RawMessage(body)})
	for _, tc := range []struct{ client, source, release string }{{"0.167.0", "official_snapshot", "0.167.0"}, {"0.170.0", "official_snapshot", "0.169.0"}, {"0.171.0", "imported_catalog", ""}} {
		raw, omitted, err := NewCodexCatalogService(models, channels).Models(ctx, tc.client)
		require.NoError(t, err, tc.client)
		require.Zero(t, omitted)
		entries, err := parseCodexCatalog(raw)
		require.NoError(t, err)
		entry := entries[slug]
		require.NotNil(t, entry)
		origin := entry["axonhub_catalog"].(map[string]any)
		require.Equal(t, tc.source, origin["source"])
		if tc.release != "" {
			require.Equal(t, tc.release, origin["version"])
		}
	}
}

func TestCatalogSubscriptionReportsEffectiveVersionChange(t *testing.T) {
	ctx, db, _, _, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)
	var fixture struct {
		Models  []map[string]any `json:"models"`
		Version string           `json:"client_version"`
	}
	require.NoError(t, json.Unmarshal([]byte(catalogFixture), &fixture))
	other := maps.Clone(fixture.Models[0])
	other["slug"] = "explicit"
	other["minimal_client_version"] = "0.167.0"
	fixture.Models = append(fixture.Models, other)
	fixture.Version = "0.171.0"
	svc.catalogTransport = snapshotTransport(func(req *http.Request) (*http.Response, error) {
		body, err := json.Marshal(fixture)
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	input := CatalogImport{Source: "url", Subscribe: true, URL: "https://catalog.example.invalid/models.json"}
	applyTestCatalog(t, ctx, svc, input)
	fixture.Version = "0.172.0"
	require.NoError(t, svc.CheckPublicCatalog(ctx))
	status, err := svc.PublicStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, status.Changed)
	p, err := svc.ImportCatalog(ctx, input, false)
	require.NoError(t, err)
	require.Len(t, p.Changes, 1)
	require.Equal(t, "test-model", p.Changes[0].Slug)
	require.Equal(t, []string{"minimal_client_version"}, p.Changes[0].Fields)
	require.Equal(t, &catalogVersionChange{From: "0.171.0", To: "0.172.0"}, p.Changes[0].MinimumVersion)
	require.Equal(t, "0.171.0", svc.cachedPublicState().archive.ClientVersion)
}

func TestCatalogReplicaReadsCommittedSourceFromPrimary(t *testing.T) {
	ctx, db, _, _, _ := setupListEnabledModelsTest(t)
	writer, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)
	applyTestCatalog(t, ctx, writer, CatalogImport{Source: "manual", Catalog: json.RawMessage(catalogFixture)})
	reader, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)

	// The Ent router sends unmarked queries to the possibly lagging read replica.
	db.System.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			require.True(t, contexts.IsPrimaryRead(ctx), "schedule, generation and archive reads must observe the writer")
			return next.Query(ctx, query)
		})
	}))
	status, err := reader.PublicStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "manual", status.Source)
	require.NotNil(t, reader.Load(ctx, "0.170.0", ""))
}

func TestCatalogReplicasRejectStaleOfficialRefresh(t *testing.T) {
	for _, initial := range []string{"empty", "official"} {
		t.Run(initial, func(t *testing.T) {
			ctx, db, _, _, _ := setupListEnabledModelsTest(t)
			a, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
			require.NoError(t, err)
			b, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
			require.NoError(t, err)
			reply := func() *http.Response {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(catalogFixture))}
			}
			if initial == "official" {
				b.httpClient.Transport = snapshotTransport(func(*http.Request) (*http.Response, error) { return reply(), nil })
				require.NoError(t, b.CheckPublicCatalog(ctx))
			}
			calls := 0
			b.httpClient.Transport = snapshotTransport(func(*http.Request) (*http.Response, error) {
				calls++
				// A commits a source switch while B's old network request is still in flight.
				applyTestCatalog(t, ctx, a, CatalogImport{Source: "manual", Catalog: json.RawMessage(catalogFixture)})
				return reply(), nil
			})
			require.ErrorIs(t, b.CheckPublicCatalog(ctx), ErrCatalogPreviewChanged)
			require.NoError(t, b.checkPublicIfDue(ctx))
			require.NoError(t, b.CheckPublicCatalog(ctx))
			require.Equal(t, 1, calls)
			for _, svc := range []*CodexSnapshotService{a, b} {
				status, err := svc.PublicStatus(ctx)
				require.NoError(t, err)
				require.Equal(t, "manual", status.Source)
				require.Equal(t, "imported_catalog", snapshotCodexProfile(svc.Load(ctx, "0.167.0", ""), "test-model").SourceKind)
			}
		})
	}
}

func TestCatalogStatusRespectsServerPolicy(t *testing.T) {
	ctx, db, _, _, _ := setupListEnabledModelsTest(t)
	s, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
	require.NoError(t, err)
	applyTestCatalog(t, ctx, s, CatalogImport{Source: "manual", Catalog: json.RawMessage(catalogFixture)})
	for _, config := range []CodexCatalogConfig{{Disabled: true}, {Version: "0.167.0"}, {Offline: true}} {
		restarted, err := NewCodexSnapshotService(config, db)
		require.NoError(t, err)
		status, err := restarted.PublicStatus(ctx)
		require.NoError(t, err)
		require.True(t, status.Blocked)
		if config.Offline {
			require.Equal(t, "manual", status.Source)
			require.Len(t, status.Entries, 1)
		} else {
			require.False(t, status.CanImportLocal)
			require.NotEqual(t, "manual", status.Source)
			require.Empty(t, status.Entries)
		}
	}
}

func TestCatalogCanReplaceCorruptArchive(t *testing.T) {
	for _, tc := range []struct {
		name, invalid  string
		reloadOfficial bool
	}{
		{"unknown-source", "broken", false},
		{"official-checksum", `{"sha256":"wrong","etag":"obsolete"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, _, _, _ := setupListEnabledModelsTest(t)
			svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
			require.NoError(t, err)
			input := CatalogImport{Source: "manual", Catalog: json.RawMessage(catalogFixture)}
			applyTestCatalog(t, ctx, svc, input)
			db.System.Update().Where(system.KeyEQ(publicArchiveKey)).SetValue(tc.invalid).ExecX(ctx)
			restarted, err := NewCodexSnapshotService(CodexCatalogConfig{}, db)
			require.NoError(t, err)
			status, err := restarted.PublicStatus(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, status.LastError)
			require.Empty(t, status.Entries)
			if tc.reloadOfficial {
				restarted.httpClient.Transport = snapshotTransport(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, publicCatalogURL, req.URL.String())
					require.Empty(t, req.Header.Get("If-None-Match"))
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(catalogFixture))}, nil
				})
				require.NoError(t, restarted.CheckPublicCatalog(ctx))
			} else {
				require.Equal(t, "unavailable", status.Source)
				applyTestCatalog(t, ctx, restarted, input)
			}
			status, err = restarted.PublicStatus(ctx)
			require.NoError(t, err)
			require.Empty(t, status.LastError)
			require.Len(t, status.Entries, 1)
		})
	}
}
