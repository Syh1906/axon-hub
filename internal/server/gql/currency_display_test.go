package gql

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestCurrencyDisplayGraphQL(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:currency-display-gql?mode=memory&_fk=0")
	defer client.Close()
	ctx := ent.NewContext(t.Context(), client)
	admin := authz.WithTestBypass(ctx)
	service := biz.NewSystemService(biz.SystemServiceParams{CacheConfig: xcache.Config{Mode: xcache.ModeMemory}, Ent: client})
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{client: client, systemService: service}}))
	member := contexts.WithUser(ctx, &ent.User{ID: 1})
	for _, decimals := range []*int{nil, lo.ToPtr(0), lo.ToPtr(2), lo.ToPtr(8), nil} {
		require.NoError(t, service.SetGeneralSettings(admin, biz.SystemGeneralSettings{
			CurrencyCode: "USD", Timezone: "UTC", CurrencyDisplayDecimals: decimals,
		}))
		req := httptest.NewRequest("POST", "/admin/graphql", strings.NewReader(`{"query":"query { systemCurrencyDisplayDecimals }"}`)).WithContext(member)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		var result struct {
			Data struct {
				Decimals *int `json:"systemCurrencyDisplayDecimals"`
			} `json:"data"`
			Errors []any `json:"errors"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.Empty(t, result.Errors)
		require.Equal(t, decimals, result.Data.Decimals)
	}
}
