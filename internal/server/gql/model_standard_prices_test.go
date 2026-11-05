package gql

import (
	"bytes"
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

func TestModelModalityPricesGraphQLSaveAndRead(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:model_modality_prices?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	server := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{modelService: biz.NewModelService(biz.ModelServiceParams{Ent: client})}}))
	run := func(query string, variables map[string]any) map[string]any {
		body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/admin/graphql", bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		var result map[string]any
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &result))
		return result
	}
	items := []any{}
	for _, entry := range []struct{ code, price string }{
		{"prompt_text_tokens", "5"}, {"prompt_image_tokens", "8"}, {"prompt_text_cached_tokens", "1.25"}, {"prompt_image_cached_tokens", "2"}, {"completion_tokens", "30"},
	} {
		items = append(items, map[string]any{"itemCode": entry.code, "pricing": map[string]any{"mode": "usage_per_unit", "usagePerUnit": entry.price}})
	}
	input := map[string]any{"developer": "test", "modelID": "image-modality-test", "name": "Image test", "icon": "test", "group": "test", "settings": map[string]any{"associations": []any{}}, "modelCard": map[string]any{"price": map[string]any{"items": items}}}
	result := run(`mutation($input: CreateModelInput!) { createModel(input: $input) { id modelCard { cost { input cacheRead } price { items { itemCode pricing { usagePerUnit } } } } } }`, map[string]any{"input": input})
	require.NotContains(t, result, "errors")
	created := result["data"].(map[string]any)["createModel"].(map[string]any)
	card := created["modelCard"].(map[string]any)
	require.Nil(t, card["cost"].(map[string]any)["input"])
	require.Len(t, card["price"].(map[string]any)["items"], 5)
	result = run(`query { matchModelStandardPrices(modelIDs: ["image-modality-test"]) { model { modelCard { price { items { itemCode pricing { usagePerUnit } } } } } } }`, nil)
	require.NotContains(t, result, "errors")
	matches := result["data"].(map[string]any)["matchModelStandardPrices"].([]any)
	require.Len(t, matches, 1)
	read := matches[0].(map[string]any)["model"].(map[string]any)["modelCard"].(map[string]any)
	require.Equal(t, card["price"], read["price"])
	// Editing uses the same enum and validates overlap before replacing the saved price.
	result = run(`mutation($id: ID!, $input: UpdateModelInput!) { updateModel(id: $id, input: $input) { modelCard { price { items { itemCode } } } } }`, map[string]any{
		"id": created["id"], "input": map[string]any{"modelCard": map[string]any{"price": map[string]any{"items": append(items, map[string]any{"itemCode": "prompt_tokens", "pricing": map[string]any{"mode": "usage_per_unit", "usagePerUnit": "5"}})}}},
	})
	require.Contains(t, result, "errors")
}

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
