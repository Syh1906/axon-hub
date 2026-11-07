package biz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

type snapshotTransport func(*http.Request) (*http.Response, error)

func (f snapshotTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCodexSnapshotsBundledAndArchive(t *testing.T) {
	ctx, client, _, _, _ := setupListEnabledModelsTest(t)
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, client)
	require.NoError(t, err)
	for _, version := range []string{"0.166.0", "0.167.0"} {
		snapshot := svc.Load(ctx, version, "")
		require.NotNil(t, snapshot)
		require.Len(t, snapshot.Models, 12)
		require.Contains(t, snapshot.Models, "gpt-6.1-terra")
		require.Contains(t, snapshot.Models, "gpt-6-luna")
	}
	var calls atomic.Int32
	var bad atomic.Bool
	svc.httpClient.Transport = snapshotTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, "raw.githubusercontent.com", r.URL.Host)
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("ChatGPT-Account-ID"))
		body := strings.ReplaceAll(catalogFixture, "32000", "64000")
		if bad.Load() {
			body = `{"models":[{"slug":"bad"}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	snapshot := svc.Load(ctx, "0.168.0", "")
	require.Equal(t, "0.168.0", snapshot.Version)
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, float64(64000), snapshot.Models["test-model"]["context_window"])
	require.NotNil(t, svc.Load(ctx, "0.168.0", ""))
	require.Equal(t, int32(1), calls.Load(), "release archives are immutable")
	require.Equal(t, 1, client.System.Query().Where(system.KeyEQ(codexSnapshotKeyPrefix+"0.168.0")).CountX(ctx))

	// Reconstruct the service: persisted original bytes must retain their digest.
	restarted, err := NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
	require.NoError(t, err)
	recovered := restarted.Load(ctx, "0.168.0", "")
	require.Equal(t, snapshot.SHA256, recovered.SHA256)
	require.Equal(t, snapshot.Body, recovered.Body)
	require.Equal(t, "0.168.0", restarted.Load(ctx, "0.169.0", "").Version)
	require.Equal(t, "0.166.0", restarted.Load(ctx, "0.169.0", "0.166.0").Version)
	require.Nil(t, restarted.Load(ctx, "0.165.0", ""), "do not send a newer schema to an older client")
	require.Nil(t, restarted.Load(ctx, "0.166.0", "0.167.0"))
	require.Nil(t, restarted.Load(ctx, "0.169.0", "0.165.0"), "an unavailable pin must not float")

	bad.Store(true)
	require.Equal(t, "0.168.0", svc.Load(ctx, "0.169.0", "").Version)
	require.Zero(t, client.System.Query().Where(system.KeyEQ(codexSnapshotKeyPrefix+"0.169.0")).CountX(ctx))
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, "0.168.0", svc.Load(ctx, "0.169.0", "").Version)
	require.Equal(t, int32(2), calls.Load(), "a missing or bad version is not fetched on every request")

	row := client.System.Query().Where(system.KeyEQ(codexSnapshotKeyPrefix + "0.168.0")).OnlyX(ctx)
	var corrupted codexSnapshot
	require.NoError(t, json.Unmarshal([]byte(row.Value), &corrupted))
	corrupted.SHA256 = "bad"
	body, err := json.Marshal(corrupted)
	require.NoError(t, err)
	client.System.UpdateOne(row).SetValue(string(body)).SaveX(ctx)
	restarted, err = NewCodexSnapshotService(CodexCatalogConfig{Offline: true}, client)
	require.NoError(t, err)
	require.Equal(t, "0.167.0", restarted.Load(ctx, "0.169.0", "").Version, "corrupt history falls back to a verified bundled release")
}

func TestCodexSnapshotFetchRejectsBadResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"not found", "", 404}, {"redirect", "", 302}, {"server error", "", 500},
		{"too large", strings.Repeat("x", codexCatalogMaxBytes+1), 200},
		{"plain list", `{"data":[{"id":"gpt-6-luna"}]}`, 200},
		{"empty", `{"models":[]}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, nil)
			require.NoError(t, err)
			svc.httpClient.Transport = snapshotTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": []string{"https://other.example"}}}, nil
			})
			snapshot, err := svc.fetch(context.Background(), "0.168.0")
			require.Nil(t, snapshot)
			if tc.status == 404 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	_, err := NewCodexSnapshotService(CodexCatalogConfig{Version: "../../main"}, nil)
	require.Error(t, err)
}

func TestCodexSnapshotRealHTTPIsolation(t *testing.T) {
	ctx, client, _, _, _ := setupListEnabledModelsTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(catalogFixture))
	}))
	defer server.Close()
	svc, err := NewCodexSnapshotService(CodexCatalogConfig{}, client)
	require.NoError(t, err)
	transport := server.Client().Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	svc.httpClient.Transport = snapshotTransport(func(r *http.Request) (*http.Response, error) {
		copyRequest := r.Clone(r.Context())
		local, err := http.NewRequestWithContext(r.Context(), http.MethodGet, server.URL+r.URL.Path, nil)
		require.NoError(t, err)
		copyRequest.URL = local.URL
		return transport.RoundTrip(copyRequest)
	})
	require.Equal(t, "0.168.0", svc.Load(ctx, "0.168.0", "").Version)
}

func TestCodexSnapshotAndProviderFetchIndependently(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var err error
	models.codexSnapshots, err = NewCodexSnapshotService(CodexCatalogConfig{}, client)
	require.NoError(t, err)
	providerStarted, snapshotStarted := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(providerStarted)
		select {
		case <-snapshotStarted:
			_, _ = w.Write([]byte(catalogFixture))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	models.codexSnapshots.httpClient.Transport = snapshotTransport(func(r *http.Request) (*http.Response, error) {
		close(snapshotStarted)
		select {
		case <-providerStarted:
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(catalogFixture)), Header: make(http.Header)}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	ch := client.Channel.Create().SetType("openai_responses").SetName("provider").SetBaseURL(server.URL).
		SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").SetStatus("enabled").SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	sources, snapshots := NewCodexCatalogService(models, channels).fetchCatalogInputs(ctx,
		[]*ModelChannelConnection{{Channel: ch, Models: []ChannelModelEntry{{ActualModel: "test-model"}}}},
		map[int]*Channel{ch.ID: channels.GetEnabledChannels()[0]}, "0.168.0", map[string]bool{"": true})
	require.Contains(t, sources[ch.ID], "test-model")
	require.NotNil(t, snapshots[""])
	require.Equal(t, "0.168.0", snapshots[""].Version)
}
