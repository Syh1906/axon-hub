package biz

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func TestModelPriceFallbackFeedsRequestAndKeyUsage(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(ent.NewContext(f.ctx, f.client))
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{
		{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}},
		{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(10))}},
		{ItemCode: objects.PriceItemCodePromptCachedToken, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString("0.2"))}},
	}}
	configured := f.client.Model.Create().SetDeveloper("test").SetIcon("test").SetGroup("test").
		SetModelID(`^gpt-\d+-astra$`).SetName("Astra rule").SetSettings(&objects.ModelSettings{}).
		SetMatchType(objects.ModelMatchRegex).SetStatus(model.StatusEnabled).
		SetModelCard(&objects.ModelCard{Price: &price, Limit: objects.ModelCardLimit{Output: 100000}}).SaveX(ctx)
	resolved, err := NewModelService(ModelServiceParams{Ent: f.client}).ResolveModel(ctx, "gpt-6-astra")
	require.NoError(t, err)
	require.Equal(t, configured.ID, resolved.ID)
	channelEntity := f.client.Channel.Create().SetName("model-price-fallback").SetType(channel.TypeOpenaiFake).
		SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"upstream-astra"}).
		SetDefaultTestModel("upstream-astra").
		SetSettings(&objects.ChannelSettings{ModelPriceMultiplier: lo.ToPtr(decimal.RequireFromString("0.25"))}).SaveX(ctx)
	ch := &Channel{Channel: channelEntity}
	snapshot := captureRequestCostSnapshot(ctx, f.client, ch, "upstream-astra", resolved.ModelCard)
	require.NotNil(t, snapshot.ModelPrice)
	require.Empty(t, snapshot.PriceReferenceID)
	require.Zero(t, f.client.ChannelModelPrice.Query().CountX(ctx), "inheriting a model price must not create a channel override")

	// The saved request keeps its price when the model card is edited later.
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	f.client.Model.UpdateOneID(configured.ID).SetModelCard(&objects.ModelCard{
		Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 99)}},
	}).SaveX(ctx)
	var restored objects.RequestCostSnapshot
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Nil(t, restored.Price)
	usage := &llm.Usage{PromptTokens: 1000000, CompletionTokens: 100000, TotalTokens: 1100000,
		PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 200000}}
	channelService := NewChannelServiceForTest(f.client)
	service := NewUsageLogService(f.client, f.service.system, channelService)
	service.InjectUsageCost(ctx, ch.ID, "upstream-astra", usage, &restored)
	require.NotNil(t, usage.Cost)
	require.InDelta(t, 0.66, *usage.Cost, 1e-12)
	settled, err := service.actualBudgetCost(ctx, usage, &restored)
	require.NoError(t, err)
	require.Equal(t, "0.66000000", settled.String())
	quote, err := service.BudgetService.Estimate(ctx, &ent.RequestExecution{CostSnapshot: &restored}, &llm.Request{}, nil, 1000000)
	require.NoError(t, err)
	require.Equal(t, "0.80000000", quote.String(), "the matched model's output limit is available for budget estimation")

	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	req := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "gpt-6-astra")
	log, err := service.CreateUsageLog(ctx, CreateUsageLogParams{
		RequestID: req.ID, ProjectID: f.project.ID, ChannelID: ch.ID, ActualModelID: "upstream-astra",
		Usage: usage, Source: "api", Format: string(llm.APIFormatOpenAIResponse), APIKeyID: &f.key.ID, CostSnapshot: &restored,
	})
	require.NoError(t, err)
	require.NotNil(t, log.TotalCost)
	require.InDelta(t, 0.66, *log.TotalCost, 1e-12)
	require.Len(t, log.CostItems, 3)
	stats, err := f.service.Stats(f.ctx, "2026-01-01", "2026-01-01")
	require.NoError(t, err)
	require.NotNil(t, stats.Totals.Cost)
	require.InDelta(t, 0.66, *stats.Totals.Cost, 1e-12)
	require.Zero(t, stats.Totals.UnpricedRecords)
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.InDelta(t, 0.66, *page.Items[0].Cost, 1e-12)
	require.InDelta(t, 0.25, *page.Items[0].CostMultiplier, 1e-12)
}

func TestModelPriceFallbackPreservesExplicitChannelPrices(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(ent.NewContext(f.ctx, f.client))
	card := &objects.ModelCard{Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 99)}}}
	service := NewUsageLogService(f.client, f.service.system, NewChannelServiceForTest(f.client))
	for _, fee := range []int64{0, 4} {
		t.Run(decimal.NewFromInt(fee).String(), func(t *testing.T) {
			price := objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, float64(fee))}, Multiplier: lo.ToPtr(decimal.RequireFromString("0.5"))}
			ch := &Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{}}, cachedModelPrices: map[string]*ent.ChannelModelPrice{
				"actual": {ReferenceID: "explicit", Price: price},
			}}
			snapshot := captureRequestCostSnapshot(ctx, f.client, ch, "actual", card)
			require.Nil(t, snapshot.ModelPrice)
			require.Equal(t, "explicit", snapshot.PriceReferenceID)
			_, cost, _ := service.computeSnapshotUsageCost(ctx, &llm.Usage{}, snapshot)
			require.NotNil(t, cost)
			require.Equal(t, float64(fee)*0.5, *cost)
		})
	}
	ch := &Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{}}}
	missing := captureRequestCostSnapshot(ctx, f.client, ch, "missing", nil)
	_, cost, _ := service.computeSnapshotUsageCost(ctx, &llm.Usage{}, missing)
	require.Nil(t, cost, "a missing price is not a free request")
}
