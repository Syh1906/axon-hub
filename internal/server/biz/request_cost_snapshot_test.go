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
	updatedModel := f.client.Model.GetX(ctx, configured.ID)
	newSnapshot := captureRequestCostSnapshot(ctx, f.client, ch, "upstream-astra", updatedModel.ModelCard)
	_, newCost := computeCostFromSnapshot(&llm.Usage{}, *newSnapshot.CurrentPrice(), newSnapshot)
	require.Equal(t, "24.75", newCost.String(), "new requests follow updated model prices with the channel multiplier")
	require.Zero(t, f.client.ChannelModelPrice.Query().CountX(ctx))
	require.Zero(t, f.client.ChannelModelPriceVersion.Query().CountX(ctx))
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
	// Reserve the 1M input once at max(2, 0.2), plus the model's 100K output
	// limit at 10, then apply 0.25: (2 + 1) * 0.25 = 0.75.
	require.Equal(t, "0.75000000", quote.String(), "model output limit and mutually exclusive input/cache pricing feed the estimate")

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
	_, cost, _ = service.computeSnapshotUsageCost(ctx, &llm.Usage{PromptTokens: 100, TotalTokens: 100}, &objects.RequestCostSnapshot{
		ModelPrice: &objects.ModelPrice{}, Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)},
	})
	require.Nil(t, cost, "an empty price configuration is not an explicit zero price")
}

func TestAssociationPriceFollowsEachExecution(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(ent.NewContext(f.ctx, f.client))
	card := func(rate int64, limit int) *objects.ModelCard {
		return &objects.ModelCard{Price: &objects.ModelPrice{Items: []objects.ModelPriceItem{
			{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(rate))}},
		}}, Limit: objects.ModelCardLimit{Output: limit}}
	}
	entrance := card(1, 8000)
	target := f.client.Model.Create().SetModelID("-terra").SetMatchType(objects.ModelMatchSuffix).SetDeveloper("test").
		SetName("Terra").SetIcon("test").SetGroup("test").SetStatus(model.StatusEnabled).SetSettings(&objects.ModelSettings{}).SetModelCard(card(10, 30000)).SaveX(ctx)
	service := NewUsageLogService(f.client, f.service.system, NewChannelServiceForTest(f.client))
	ch := &Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{}}}
	for _, tc := range []struct {
		actual bool
		want   float64
	}{{false, 1}, {true, 10}} {
		snap, err := captureExecutionCostSnapshot(ctx, f.client, ch, "gpt-6.1-terra", entrance, &objects.RequestExecutionSelection{UseActualModelPrice: tc.actual})
		require.NoError(t, err)
		require.EqualValues(t, 8000, snap.ModelOutputLimit)
		if tc.actual {
			require.Equal(t, target.ModelID, snap.StandardPriceModelID)
		}
		usage := &llm.Usage{PromptTokens: 1000000, TotalTokens: 1000000}
		quote, err := service.BudgetService.Estimate(ctx, &ent.RequestExecution{CostSnapshot: snap}, &llm.Request{}, nil, usage.PromptTokens)
		require.NoError(t, err)
		settled, err := service.actualBudgetCost(ctx, usage, snap)
		require.NoError(t, err)
		require.Equal(t, quote, *settled)
		_, cost, _ := service.computeSnapshotUsageCost(ctx, usage, snap)
		require.NotNil(t, cost)
		require.Equal(t, tc.want, *cost)
		require.Equal(t, tc.want, settled.Decimal().InexactFloat64())
	}
	// A budgeted client may omit an output cap. A routed family card can
	// supply the estimate even when the price itself comes from the channel.
	outputCard := card(10, 30000)
	outputCard.Price.Items[0].ItemCode = objects.PriceItemCodeCompletion
	target.Update().SetModelCard(outputCard).SaveX(ctx)
	routed := &objects.RequestExecutionSelection{UseActualModelPrice: true}
	for _, override := range []bool{false, true} {
		if override {
			ch.cachedModelPrices = map[string]*ent.ChannelModelPrice{"gpt-6.1-terra": {Price: *outputCard.Price}}
		}
		snap, err := captureExecutionCostSnapshot(ctx, f.client, ch, "gpt-6.1-terra", nil, routed)
		require.NoError(t, err)
		quote, err := service.BudgetService.Estimate(ctx, &ent.RequestExecution{CostSnapshot: snap, ModelID: "gpt-6.1-terra", Selection: routed}, &llm.Request{}, nil, 1000)
		require.NoError(t, err)
		require.Equal(t, "0.30000000", quote.String())
	}
	ch.cachedModelPrices = nil
	target.Update().SetStatus(model.StatusDisabled).SaveX(ctx)
	sel := &objects.RequestExecutionSelection{UseActualModelPrice: true}
	missing, err := captureExecutionCostSnapshot(ctx, f.client, ch, "gpt-6.1-terra", entrance, sel)
	require.NoError(t, err)
	require.Nil(t, missing.CurrentPrice())
	ch.cachedModelPrices = map[string]*ent.ChannelModelPrice{"gpt-6.1-terra": {ReferenceID: "free", Price: *card(0, 1).Price}}
	free, err := captureExecutionCostSnapshot(ctx, f.client, ch, "gpt-6.1-terra", entrance, sel)
	require.NoError(t, err)
	require.Equal(t, "free", free.PriceReferenceID)
	require.Nil(t, free.ModelPrice)
}
