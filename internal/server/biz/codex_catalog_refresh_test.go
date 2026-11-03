package biz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestCodexCatalogRefreshFitsClientDeadline(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var completeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog" {
			completeCalls.Add(1)
			_, _ = w.Write([]byte(catalogFixture))
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	var associations []*objects.ModelAssociation
	for i := range 7 {
		endpoints := []objects.ChannelEndpoint{{APIFormat: "openai/responses"}}
		if i == 6 {
			endpoints[0].CatalogURL = server.URL + "/catalog"
		}
		ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).
			SetName(string(rune('a') + rune(i))).SetBaseURL(server.URL).
			SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
			SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").
			SetEndpoints(endpoints).SetStatus(channel.StatusEnabled).SaveX(ctx)
		associations = append(associations, &objects.ModelAssociation{
			Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID},
		})
	}
	createConfiguredModel(t, ctx, client, "test-model", model.TypeChat, associations[0])
	m := client.Model.Query().OnlyX(ctx)
	m.Settings.Associations = associations
	client.Model.UpdateOneID(m.ID).SetSettings(m.Settings).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	started := time.Now()
	body, omitted, err := svc.Models(ctx, "0.165.0")
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Zero(t, omitted)
	require.Equal(t, int32(1), completeCalls.Load(), "a known catalog must not queue behind stalled plain-list probes")
	parsed, err := parseCodexCatalog(body)
	require.NoError(t, err)
	require.Contains(t, parsed, "test-model")
	require.Less(t, elapsed, 4*time.Second, "leave transport margin inside the client's five-second deadline")
}

func TestCodexCatalogPlainListCache(t *testing.T) {
	for _, tc := range []struct {
		name    string
		control string
		body    string
		status  int
		calls   int32
		wantErr bool
	}{
		{"ordinary list", "private, max-age=60", `{"data":[{"id":"test-model"}]}`, 200, 1, false},
		{"no store", "no-store", `{"data":[]}`, 200, 2, false},
		{"no cache", "no-cache", `{"data":[]}`, 200, 2, false},
		{"malformed", "max-age=60", `{"data":`, 200, 2, true},
		{"unauthorized", "max-age=60", `{"data":[]}`, 401, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Cache-Control", tc.control)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName(tc.name).
				SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
				SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").
				SetStatus(channel.StatusEnabled).SaveX(ctx)
			reloadEnabledChannels(t, ctx, client, channels)
			svc := NewCodexCatalogService(models, channels)
			loaded := channels.GetEnabledChannels()[0]
			require.Equal(t, ch.ID, loaded.ID)
			for range 2 {
				metadata, err := svc.channelCatalog(ctx, loaded, "0.165.0")
				require.Equal(t, tc.wantErr, err != nil)
				require.Empty(t, metadata)
			}
			require.Equal(t, tc.calls, calls.Load())
			_, err := svc.channelCatalog(ctx, loaded, "0.166.0")
			require.Equal(t, tc.wantErr, err != nil)
			require.Equal(t, tc.calls+1, calls.Load(), "the client version isolates cached plain-list responses too")
		})
	}
}

func TestCodexCatalogUnsupportedSourceDoesNotFetch(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client.Channel.Create().SetType(channel.TypeGemini).SetName("without-catalog").
		SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetSupportedModels([]string{"gemini-fixture"}).SetDefaultTestModel("gemini-fixture").
		SetStatus(channel.StatusEnabled).SaveX(ctx)
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	metadata, err := svc.channelCatalog(ctx, channels.GetEnabledChannels()[0], "0.166.0")
	require.NoError(t, err, "已知不提供目录的渠道不是一次上游调用失败")
	require.Empty(t, metadata)
	require.Zero(t, calls.Load())
}

func TestCodexCatalogInvalidEntriesPreserveValidModels(t *testing.T) {
	ctx, client, models, channels, _ := setupListEnabledModelsTest(t)
	var catalog struct {
		Models []json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal([]byte(catalogFixture), &catalog))
	catalog.Models = append(catalog.Models, json.RawMessage(`{"slug":"broken"}`))
	body, err := json.Marshal(catalog)
	require.NoError(t, err)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Cache-Control", "private, max-age=60")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	ch := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("partial-catalog").
		SetBaseURL(server.URL).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetSupportedModels([]string{"test-model", "broken"}).SetDefaultTestModel("test-model").
		SetStatus(channel.StatusEnabled).SaveX(ctx)
	for _, id := range []string{"test-model", "broken"} {
		createConfiguredModel(t, ctx, client, id, model.TypeChat,
			&objects.ModelAssociation{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: ch.ID}})
	}
	reloadEnabledChannels(t, ctx, client, channels)
	svc := NewCodexCatalogService(models, channels)
	for range 2 {
		metadata, err := svc.channelCatalog(ctx, channels.GetEnabledChannels()[0], "0.166.0")
		require.ErrorIs(t, err, errCodexInvalidEntries, "坏记录必须保留来源诊断，不能与正常缺资料混同")
		require.ErrorContains(t, err, "1 entries omitted")
		require.Contains(t, metadata, "test-model")
		require.NotContains(t, metadata, "broken")
	}
	filtered, omitted, err := svc.Models(ctx, "0.166.0")
	require.NoError(t, err, "单条坏记录不能阻塞其他有效模型")
	require.Equal(t, 1, omitted)
	metadata, err := parseCodexCatalog(filtered)
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.Contains(t, metadata, "test-model")
	require.Equal(t, int32(1), calls.Load(), "缓存必须同时保留有效记录和坏记录诊断")
}
