package gql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestModelStandardPricesGraphQLRuleMatchAndPermission(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:model_standard_prices?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	setup := authz.WithTestBypass(context.Background())
	price := &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}}}}
	client.Model.Create().SetDeveloper("test").SetName("Sol rule").SetModelID(`^gpt-.*-sol$`).SetMatchType(objects.ModelMatchRegex).
		SetIcon("test").SetGroup("test").SetStatus("enabled").SetSettings(&objects.ModelSettings{}).SetModelCard(&objects.ModelCard{Price: price}).SaveX(setup)
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{modelService: biz.NewModelService(biz.ModelServiceParams{Ent: client})}}))
	query := `{"query":"query { matchModelStandardPrices(modelIDs: [\"gpt-6.1-sol\", \"unknown\"]) { requestModelID model { modelID modelCard { price { items { itemCode pricing { usagePerUnit } } } } } } }"}`
	run := func(ctx context.Context) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/admin/graphql", strings.NewReader(query)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		var body map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
		return body
	}
	viewer := &ent.User{ID: 1, Scopes: []string{string(scopes.ScopeReadChannels)}}
	ctx := contexts.WithUser(context.Background(), viewer)
	result := run(ctx)
	require.NotContains(t, result, "errors")
	matches := result["data"].(map[string]any)["matchModelStandardPrices"].([]any)
	require.Len(t, matches, 1)
	match := matches[0].(map[string]any)
	require.Equal(t, "gpt-6.1-sol", match["requestModelID"])
	require.Equal(t, `^gpt-.*-sol$`, match["model"].(map[string]any)["modelID"])
	viewer.Scopes = nil
	require.Contains(t, run(ctx), "errors")
	require.Contains(t, run(context.Background()), "errors")
}
