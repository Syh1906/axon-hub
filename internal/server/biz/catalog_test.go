package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm/httpclient"
)

func newCatalogTestService(t *testing.T) (context.Context, *SystemService, *CatalogService) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	ctx := authz.WithTestBypass(context.Background())
	ctx = ent.NewContext(ctx, client)

	system := NewSystemService(SystemServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})

	return ctx, system, NewCatalogService(system, nil)
}

func useCatalogUpstream(t *testing.T, ctx context.Context, system *SystemService, svc *CatalogService, handler http.HandlerFunc) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	require.NoError(t, system.SetCatalogSettings(ctx, CatalogSettings{UpstreamURL: server.URL}))
	svc.httpClient = httpclient.NewHttpClientWithClient(server.Client())
}

func TestCatalogService_FallsBackThenFetchesAfterInvalidate(t *testing.T) {
	ctx, system, svc := newCatalogTestService(t)

	payload, err := svc.Snapshot(ctx, true)
	require.NoError(t, err)
	require.Equal(t, catalogSourceFallback, payload.Source)
	require.True(t, payload.Filtered)
	require.Contains(t, string(payload.Data), `"providers"`)

	upstream := catalogFile{Providers: map[string]catalogProvider{
		"openai": {ID: "openai", Models: []map[string]any{{"id": "gpt-hot"}}},
	}}
	body, err := json.Marshal(upstream)
	require.NoError(t, err)

	useCatalogUpstream(t, ctx, system, svc, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	held, err := svc.Snapshot(ctx, true)
	require.NoError(t, err)
	require.Equal(t, catalogSourceFallback, held.Source, "the held fallback is served until it is released")

	svc.Invalidate()
	fetched, err := svc.Snapshot(ctx, true)
	require.NoError(t, err)
	require.Equal(t, catalogSourceUpstream, fetched.Source)
	require.True(t, fetched.Filtered)
	require.Contains(t, string(fetched.Data), "gpt-hot")
	require.WithinDuration(t, time.Now().UTC(), *fetched.FetchedAt, 5*time.Second)
}

func TestCatalogService_CachesFallbackAndDeduplicatesFetch(t *testing.T) {
	ctx, system, svc := newCatalogTestService(t)

	var hits atomic.Int32
	useCatalogUpstream(t, ctx, system, svc, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(150 * time.Millisecond)
		http.Error(w, "unavailable", http.StatusBadGateway)
	})

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		sources []string
	)
	for range 8 {
		wg.Go(func() {
			snapshot, err := svc.Snapshot(ctx, true)
			require.NoError(t, err)
			mu.Lock()
			sources = append(sources, snapshot.Source)
			mu.Unlock()
		})
	}
	wg.Wait()

	require.Len(t, sources, 8)
	for _, source := range sources {
		require.Equal(t, catalogSourceFallback, source)
	}
	require.Equal(t, int32(1), hits.Load())

	second, err := svc.Snapshot(ctx, true)
	require.NoError(t, err)
	require.Equal(t, catalogSourceFallback, second.Source)
	require.Equal(t, int32(1), hits.Load())
}

func TestCatalogService_RetainsSuccessfulDataAndSharesVariants(t *testing.T) {
	ctx, system, svc := newCatalogTestService(t)
	var hits atomic.Int32
	var fail atomic.Bool
	body := `{"providers":{"openai":{"id":"openai","models":[{"id":"gpt-hot"}]}}}`
	useCatalogUpstream(t, ctx, system, svc, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	original, err := svc.Snapshot(ctx, false)
	require.NoError(t, err)
	filtered, err := svc.Snapshot(ctx, true)
	require.NoError(t, err)
	require.Contains(t, string(filtered.Data), "gpt-hot")
	require.Equal(t, int32(1), hits.Load(), "both variants share the source fetch")
	svc.mu.Lock()
	svc.freshUntil = time.Now().Add(-time.Second)
	svc.mu.Unlock()
	fail.Store(true)
	stale, err := svc.Snapshot(ctx, false)
	require.NoError(t, err)
	require.JSONEq(t, body, string(stale.Data))
	require.Equal(t, original.FetchedAt, stale.FetchedAt, "failed fetch does not pretend old data was refreshed")
	require.Equal(t, int32(2), hits.Load())
	_, err = svc.Snapshot(ctx, true)
	require.NoError(t, err)
	require.Equal(t, int32(2), hits.Load(), "failed refresh is not repeated for the other variant")
	fail.Store(false)
	_, err = svc.Refresh(ctx)
	require.NoError(t, err)
	require.Equal(t, int32(3), hits.Load(), "manual refresh bypasses the freshness interval")
}

func TestCatalogService_InvalidUpstreamServesHeldFallback(t *testing.T) {
	ctx, system, svc := newCatalogTestService(t)

	responses := []string{`{"providers":[]}`, `{"providers":{`}
	var hits atomic.Int32
	useCatalogUpstream(t, ctx, system, svc, func(w http.ResponseWriter, _ *http.Request) {
		i := int(hits.Add(1)) - 1
		if i >= len(responses) {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(responses[i]))
	})

	for range 2 {
		svc.Invalidate()
		snapshot, err := svc.Snapshot(ctx, false)
		require.NoError(t, err)
		require.Equal(t, catalogSourceFallback, snapshot.Source)
		require.Equal(t, string(catalogFallbackJSON), string(snapshot.Data))
	}

	_, err := svc.Snapshot(ctx, false)
	require.NoError(t, err)
	require.Equal(t, int32(2), hits.Load(), "fallback is held for the TTL instead of retrying upstream")
}

func TestCatalogService_CancelledCallerDoesNotCauseFallback(t *testing.T) {
	ctx, system, svc := newCatalogTestService(t)

	useCatalogUpstream(t, ctx, system, svc, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":{"openai":{"id":"openai","models":[{"id":"gpt-hot"}]}}}`))
	})

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	_, err := svc.Snapshot(cancelled, true)
	require.ErrorIs(t, err, context.Canceled)
	snapshot, err := svc.Snapshot(ctx, true)
	require.NoError(t, err)
	require.Equal(t, catalogSourceUpstream, snapshot.Source)
	require.Contains(t, string(snapshot.Data), "gpt-hot")
}

func TestCatalogRefreshInvalidModelShapePreservesLastSuccess(t *testing.T) {
	ctx, system, svc := newCatalogTestService(t)
	var invalid atomic.Bool
	useCatalogUpstream(t, ctx, system, svc, func(w http.ResponseWriter, _ *http.Request) {
		if invalid.Load() {
			_, _ = w.Write([]byte(`{"providers":{"openai":{"models":"invalid"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"providers":{"openai":{"id":"openai","models":[{"id":"last-good"}]}}}`))
	})
	before, err := svc.Snapshot(ctx, true)
	require.NoError(t, err)
	invalid.Store(true)
	after, err := svc.Refresh(ctx)
	require.NoError(t, err)
	require.Equal(t, before.Data, after.Data)
	require.Equal(t, before.FetchedAt, after.FetchedAt)
}
