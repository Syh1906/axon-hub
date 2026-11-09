package gql

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestRequestPageSharesChannelReads(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:request-channel-batch?mode=memory&_fk=0")
	defer db.Close()
	ctx := authz.WithTestBypass(context.Background())
	ch := db.Channel.Create().SetName("shared-channel").SetType("openai").SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetCredentials(objects.ChannelCredentials{}).SaveX(ctx)
	for i := 0; i < 50; i++ {
		req := db.Request.Create().SetModelID("model").SetStatus("completed").SetChannelID(ch.ID).SetRequestBody([]byte(`{}`)).SaveX(ctx)
		db.RequestExecution.Create().SetRequestID(req.ID).SetModelID("model").SetStatus("completed").SetChannelID(ch.ID).SetChannelAPIKeyIndex(1).SetRequestBody([]byte(`{}`)).SaveX(ctx)
	}
	var channelReads atomic.Int64
	var executionReads atomic.Int64
	client := ent.NewClient(ent.Driver(db.Driver()), ent.Debug(), ent.Log(func(args ...any) {
		if strings.Contains(fmt.Sprint(args...), "FROM `channels`") {
			channelReads.Add(1)
		}
		if strings.Contains(fmt.Sprint(args...), "FROM `request_executions`") {
			executionReads.Add(1)
		}
	}))
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: client}}))
	for _, size := range []int{1, 50} {
		channelReads.Store(0)
		executionReads.Store(0)
		body := fmt.Sprintf(`{"query":"{requestLogs(first:%d){edges{node{id resultExecution{id responseStatusCode} channel{id name} executions(first:10){edges{node{id channel{id name} channelAPIKeyIndex}}}}}}}"}`, size)
		req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body)).WithContext(ent.NewContext(withRequestChannelSessionCache(withChannelReadCache(ctx)), client))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		require.NotContains(t, response.Body.String(), `"errors"`)
		require.Contains(t, response.Body.String(), `"channelAPIKeyIndex":1`)
		require.LessOrEqual(t, channelReads.Load(), int64(4), "page, attempts and result metadata share bounded channel reads")
		require.LessOrEqual(t, executionReads.Load(), int64(3), "result metadata must load once for the whole page")
	}
	firstCtx := withChannelReadCache(ctx)
	first, err := getNilableChannel(firstCtx, client, ch.ID)
	require.NoError(t, err)
	db.Channel.UpdateOneID(ch.ID).SetName("renamed").SaveX(ctx)
	again, err := getNilableChannel(withChannelReadCache(ctx), client, ch.ID)
	require.NoError(t, err)
	require.NotEqual(t, first.Name, again.Name, "cache does not outlive the GraphQL operation")
}
