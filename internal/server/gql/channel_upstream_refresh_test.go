package gql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelupstreamstatus"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/hook"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/internal/server/middleware"
)

func TestUpstreamRefreshGraphQLCommitsEachKeyOutsideRequestTransaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") == "Bearer fixture-slow" {
			<-r.Context().Done()
			return
		}
		switch r.URL.Path {
		case "/v1/usage":
			_, _ = w.Write([]byte(`{"isValid":true,"balance":12}`))
		case "/v1/sub2api/billing":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "sub2api.key_billing", "schema_version": 1, "billing_scope": "token", "effective_rate_multiplier": 0.5, "observed_at": time.Now().UTC().Format(time.RFC3339)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	db := enttest.NewEntClient(t, "sqlite3", "file:upstream-gql-refresh?mode=memory&cache=shared&_fk=0")
	defer db.Close()
	ctx := authz.WithTestBypass(context.Background())
	keys := []string{"fixture-first", "fixture-slow", "fixture-last"}
	ch := db.Channel.Create().SetName("refresh").SetType("openai").SetBaseURL(server.URL).
		SetCredentials(objects.ChannelCredentials{APIKeys: keys}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	var transactionalWrites atomic.Int32
	db.ChannelUpstreamStatus.Use(func(next ent.Mutator) ent.Mutator {
		return hook.ChannelUpstreamStatusFunc(func(ctx context.Context, m *ent.ChannelUpstreamStatusMutation) (ent.Value, error) {
			if ent.TxFromContext(ctx) != nil {
				transactionalWrites.Add(1)
			}
			return next.Mutate(ctx, m)
		})
	})
	svc := biz.NewChannelServiceForTest(db)
	defer svc.Stop()
	handler := NewGraphqlHandlers(Dependencies{Ent: db, ChannelService: svc})
	router := gin.New()
	// 缩短真实路由中间件的预算，避免测试等待默认 30 秒。
	router.Use(middleware.WithTimeout(2500 * time.Millisecond))
	router.Any("/*path", func(c *gin.Context) {
		handler.Graphql.ServeHTTP(c.Writer, c.Request.WithContext(authz.WithTestBypass(c.Request.Context())))
	})
	gql := client.New(router)
	query := `mutation Refresh($ids: [ID!]!, $key: String!) { refreshChannelUpstreams(channelIds: $ids, keyRef: $key, force: true, includeDisabled: true) { refreshed failed } }`
	id := fmt.Sprintf("gid://axonhub/Channel/%d", ch.ID)
	for index, key := range keys {
		var response struct {
			RefreshChannelUpstreams struct{ Refreshed, Failed int }
		}
		err := gql.Post(query, &response, client.Var("ids", []string{id}), client.Var("key", upstream.KeyRef(key)))
		require.NoError(t, err)
		require.Equal(t, 1, response.RefreshChannelUpstreams.Refreshed)
		if index == 1 {
			require.Equal(t, 1, response.RefreshChannelUpstreams.Failed)
		} else {
			require.Zero(t, response.RefreshChannelUpstreams.Failed)
		}
	}
	require.Zero(t, transactionalWrites.Load(), "external requests must not share the GraphQL transaction")
	rows := db.ChannelUpstreamStatus.Query().Order(ent.Asc(channelupstreamstatus.FieldID)).AllX(ctx)
	require.Len(t, rows, 3)
	require.Equal(t, objects.UpstreamQuerySuccess, rows[0].Snapshot.RateState)
	require.Equal(t, objects.UpstreamQueryError, rows[1].Snapshot.RateState)
	require.Equal(t, objects.UpstreamQuerySuccess, rows[2].Snapshot.RateState)
	var response any
	require.Error(t, gql.Post(query, &response, client.Var("ids", []string{id, id}), client.Var("key", upstream.KeyRef(keys[0]))))
	require.Error(t, gql.Post(query, &response, client.Var("ids", []string{id}), client.Var("key", "removed-key")))
}
