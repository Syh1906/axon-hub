package gql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
)

func TestConsumptionGraphQLReadOnlyPermission(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:consumption_graphql?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	setup := authz.WithTestBypass(context.Background())
	client.Project.Create().SetName("统计项目").SaveX(setup)
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: client})
	require.NoError(t, system.SetGeneralSettings(setup, biz.SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "USD"}))
	service := biz.NewConsumptionService(client, system, xcache.Config{})
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{consumptionService: service}}))
	query := `{"query":"query { consumptionProjects { id name } consumptionLeaderboard(input: {dimension: CHANNEL, metric: CACHE, period: ALL}) { timezone currencyCode entries { id name metrics { requestCount cacheHitRate cacheCoefficient } models { model metrics { totalTokens avgTokensPerSecond } } } } }"}`
	run := func(ctx context.Context) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(query)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		require.Equal(t, http.StatusOK, response.Code)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		return payload
	}
	viewer := &ent.User{ID: 1, Scopes: []string{string(scopes.ScopeReadLeaderboard)}}
	ctx := contexts.WithUser(context.Background(), viewer)
	response := run(ctx)
	require.NotContains(t, response, "errors")
	data := response["data"].(map[string]any)
	require.Len(t, data["consumptionProjects"], 1)
	require.Equal(t, "Asia/Shanghai", data["consumptionLeaderboard"].(map[string]any)["timezone"])
	viewer.Scopes = nil
	require.Contains(t, run(ctx), "errors")
	require.Contains(t, run(context.Background()), "errors")
}
