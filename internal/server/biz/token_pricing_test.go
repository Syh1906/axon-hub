package biz

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func imageTokenTestPrice() objects.ModelPrice {
	price := objects.ModelPrice{}
	for _, item := range []struct {
		code objects.PriceItemCode
		rate string
	}{
		{objects.PriceItemCodePromptText, "5"},
		{objects.PriceItemCodePromptImage, "8"},
		{objects.PriceItemCodePromptTextCached, "1.25"},
		{objects.PriceItemCodePromptImageCached, "2"},
		{objects.PriceItemCodeCompletion, "30"},
	} {
		price.Items = append(price.Items, objects.ModelPriceItem{ItemCode: item.code,
			Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString(item.rate))}})
	}
	return price
}

func imageTokenTestUsage() *llm.Usage {
	return &llm.Usage{PromptTokens: 3000, CompletionTokens: 1000, TotalTokens: 4000,
		PromptTokensDetails: &llm.PromptTokensDetails{TextTokens: lo.ToPtr(int64(1000)), ImageTokens: lo.ToPtr(int64(2000))}}
}

func TestComputeUsageCostModalityTokens(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*llm.Usage)
		cost       string
		quantities []int64
	}{
		{"mixed input", func(*llm.Usage) {}, "0.051", []int64{1000, 2000, 0, 0, 1000}},
		{"text only", func(u *llm.Usage) {
			u.PromptTokens, u.TotalTokens = 1000, 2000
			u.PromptTokensDetails.ImageTokens = lo.ToPtr(int64(0))
		}, "0.035", []int64{1000, 0, 0, 0, 1000}},
		{"separate cache rates", func(u *llm.Usage) {
			u.PromptTokensDetails.CachedTokens = 1200
			u.PromptTokensDetails.CachedTokensDetails = &llm.CachedTokensDetails{TextTokens: lo.ToPtr(int64(400)), ImageTokens: lo.ToPtr(int64(800))}
		}, "0.0447", []int64{600, 1200, 400, 800, 1000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := imageTokenTestUsage()
			tc.change(u)
			price := imageTokenTestPrice()
			require.NoError(t, price.Validate())
			items, total := ComputeUsageCost(u, price, time.Now())
			require.Equal(t, tc.cost, total.String())
			for i, item := range items {
				require.False(t, item.Unpriced)
				require.Equal(t, tc.quantities[i], item.Quantity)
			}
			price.Multiplier = lo.ToPtr(decimal.NewFromInt(2))
			_, scaled := ComputeUsageCost(u, price, time.Now())
			require.True(t, total.Mul(decimal.NewFromInt(2)).Equal(scaled))
		})
	}
}

func TestModalityPricingCCHReference(t *testing.T) {
	// CCH's image-token regression uses 560 tokens at $0.00000196 per token.
	// AxonHub stores a per-million rate and keeps images inside PromptTokens.
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{
		{ItemCode: objects.PriceItemCodePromptText, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}},
		{ItemCode: objects.PriceItemCodePromptImage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString("1.96"))}},
	}}
	u := &llm.Usage{PromptTokens: 560, TotalTokens: 560, PromptTokensDetails: &llm.PromptTokensDetails{TextTokens: lo.ToPtr(int64(0)), ImageTokens: lo.ToPtr(int64(560))}}
	items, total := ComputeUsageCost(u, price, time.Now())
	require.Equal(t, "0.0010976", total.String())
	require.Equal(t, u.PromptTokens, items[0].Quantity+items[1].Quantity)
	u.PromptTokens, u.TotalTokens, u.PromptTokensDetails.TextTokens = 886, 886, lo.ToPtr(int64(326))
	_, total = ComputeUsageCost(u, price, time.Now())
	require.Equal(t, "0.0017496", total.String(), "the 560 image tokens must not be billed again at the text rate")
}

func TestModalityPricingRequiresRatesOnlyForUsedBuckets(t *testing.T) {
	price := imageTokenTestPrice()
	price.Items = []objects.ModelPriceItem{price.Items[0], price.Items[1], price.Items[4]}
	u := imageTokenTestUsage()
	snapshot := &objects.RequestCostSnapshot{Price: &price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	svc := &UsageLogService{}
	_, cost, _ := svc.computeSnapshotUsageCost(context.Background(), u, snapshot)
	require.NotNil(t, cost, "unused cache prices are optional")
	require.InDelta(t, 0.051, *cost, 1e-12)
	u.PromptTokensDetails.CachedTokens = 1200
	u.PromptTokensDetails.CachedTokensDetails = &llm.CachedTokensDetails{TextTokens: lo.ToPtr(int64(400)), ImageTokens: lo.ToPtr(int64(800))}
	items, cost, _ := svc.computeSnapshotUsageCost(context.Background(), u, snapshot)
	require.Nil(t, cost)
	require.Equal(t, "input_modality_price_missing", items[3].UnpricedReason)
	require.Equal(t, "input_modality_price_missing", items[4].UnpricedReason)
	price = imageTokenTestPrice()
	price.Items[2].Pricing.UsagePerUnit = lo.ToPtr(decimal.Zero)
	price.Items[3].Pricing.UsagePerUnit = lo.ToPtr(decimal.Zero)
	_, cost, _ = svc.computeSnapshotUsageCost(context.Background(), u, snapshot)
	require.NotNil(t, cost, "explicit free cache remains valid")
	require.InDelta(t, 0.0426, *cost, 1e-12)
}

func TestModalityPricingMissingEvidenceIsNotFree(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*llm.Usage)
		reason string
	}{
		{"missing breakdown", func(u *llm.Usage) { u.PromptTokensDetails = nil }, "input_modality_missing"},
		{"missing text count", func(u *llm.Usage) { u.PromptTokensDetails.TextTokens = nil }, "input_modality_missing"},
		{"aggregate cache only", func(u *llm.Usage) { u.PromptTokensDetails.CachedTokens = 1200 }, "input_cache_modality_missing"},
		{"inconsistent input", func(u *llm.Usage) { u.PromptTokens = 2000 }, "input_modality_invalid"},
		{"negative input", func(u *llm.Usage) { u.PromptTokensDetails.TextTokens = lo.ToPtr(int64(-1)) }, "input_modality_invalid"},
		{"cache exceeds modality", func(u *llm.Usage) {
			u.PromptTokensDetails.CachedTokens = 1200
			u.PromptTokensDetails.CachedTokensDetails = &llm.CachedTokensDetails{TextTokens: lo.ToPtr(int64(1200)), ImageTokens: lo.ToPtr(int64(0))}
		}, "input_modality_invalid"},
		{"missing zero-modality cache disagrees with total", func(u *llm.Usage) {
			u.PromptTokens, u.TotalTokens = 1000, 2000
			u.PromptTokensDetails.ImageTokens = lo.ToPtr(int64(0))
			u.PromptTokensDetails.CachedTokens = 100
			u.PromptTokensDetails.CachedTokensDetails = &llm.CachedTokensDetails{TextTokens: lo.ToPtr(int64(50))}
		}, "input_modality_invalid"},
		{"unknown cache writes", func(u *llm.Usage) { u.PromptTokensDetails.WriteCachedTokens = 200 }, "input_cache_modality_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := imageTokenTestUsage()
			tc.change(u)
			price := imageTokenTestPrice()
			items, _ := ComputeUsageCost(u, price, time.Now())
			require.True(t, items[0].Unpriced)
			require.Equal(t, tc.reason, items[0].UnpricedReason)
			snapshot := &objects.RequestCostSnapshot{Price: &price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
			service := &UsageLogService{}
			_, cost, _ := service.computeSnapshotUsageCost(context.Background(), u, snapshot)
			require.Nil(t, cost, "partial output cost must not become a settled total")
			amount, err := service.actualBudgetCost(context.Background(), u, snapshot)
			require.NoError(t, err)
			require.Nil(t, amount)
		})
	}
}

func TestBudgetModalityInputsAreExclusive(t *testing.T) {
	price := imageTokenTestPrice()
	snapshot := &objects.RequestCostSnapshot{Price: &price, PricedAt: time.Now(), ManualMultiplier: decimal.NewFromInt(1), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	service := &BudgetService{}
	quote, err := service.Estimate(context.Background(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{MaxTokens: lo.ToPtr(int64(1000))}, nil, 3000)
	require.NoError(t, err)
	// Without an input breakdown, reserve 3000 at the largest rate (8), plus output.
	require.Equal(t, "0.05400000", quote.String())
	amount, err := (&UsageLogService{}).actualBudgetCost(context.Background(), imageTokenTestUsage(), snapshot)
	require.NoError(t, err)
	require.Equal(t, "0.05100000", amount.String())
	// Switching back to per-token prices may retain an inactive tier draft.
	for i := range price.Items {
		price.Items[i].Pricing.UsageTiered = &objects.TieredPricing{Tiers: []objects.PriceTier{{PricePerUnit: decimal.Zero}}}
	}
	quote, err = service.Estimate(context.Background(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{MaxTokens: lo.ToPtr(int64(1000))}, nil, 3000)
	require.NoError(t, err)
	require.Equal(t, "0.05400000", quote.String())
}

func TestBudgetModalityDecreasingTiers(t *testing.T) {
	for _, mode := range []objects.PricingMode{objects.PricingModeTiered, objects.PricingModeVolume} {
		t.Run(string(mode), func(t *testing.T) {
			price := imageTokenTestPrice()
			price.Items = price.Items[:2]
			for i := range price.Items {
				price.Items[i].Pricing = objects.Pricing{Mode: mode, UsageTiered: &objects.TieredPricing{Tiers: []objects.PriceTier{
					{UpTo: lo.ToPtr(int64(500)), PricePerUnit: decimal.NewFromInt(10)},
					{PricePerUnit: decimal.Zero},
				}}}
			}
			require.NoError(t, price.Validate())
			snapshot := &objects.RequestCostSnapshot{Price: &price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
			quote, err := (&BudgetService{}).Estimate(context.Background(), &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{}, nil, 1000)
			require.NoError(t, err)
			u := &llm.Usage{PromptTokens: 1000, TotalTokens: 1000, PromptTokensDetails: &llm.PromptTokensDetails{TextTokens: lo.ToPtr(int64(500)), ImageTokens: lo.ToPtr(int64(500))}}
			_, total := ComputeUsageCost(u, price, time.Now())
			require.Equal(t, "0.01", total.String())
			require.True(t, quote.Decimal().Equal(total), "do not apply all 1000 tokens to a cheaper tier before the modality split is known")
		})
	}
}

func TestModalityCacheAbsentForProvenZeroInput(t *testing.T) {
	u := &llm.Usage{PromptTokens: 1000, TotalTokens: 1000, PromptTokensDetails: &llm.PromptTokensDetails{
		TextTokens: lo.ToPtr(int64(1000)), ImageTokens: lo.ToPtr(int64(0)), CachedTokens: 100,
		CachedTokensDetails: &llm.CachedTokensDetails{TextTokens: lo.ToPtr(int64(100))},
	}}
	items, total := ComputeUsageCost(u, imageTokenTestPrice(), time.Now())
	require.Equal(t, "0.004625", total.String())
	for _, item := range items {
		require.False(t, item.Unpriced)
	}
}

func TestModalityCacheWithAggregateInputPrice(t *testing.T) {
	price := imageTokenTestPrice()
	price.Items = append([]objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage,
		Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(5))}}}, price.Items[2:4]...)
	require.NoError(t, price.Validate())
	u := &llm.Usage{PromptTokens: 3000, TotalTokens: 3000, PromptTokensDetails: &llm.PromptTokensDetails{
		CachedTokens: 1200, CachedTokensDetails: &llm.CachedTokensDetails{TextTokens: lo.ToPtr(int64(400)), ImageTokens: lo.ToPtr(int64(800))},
	}}
	items, total := ComputeUsageCost(u, price, time.Now())
	require.Equal(t, "0.0111", total.String())
	for i, quantity := range []int64{1800, 400, 800} {
		require.Equal(t, quantity, items[i].Quantity)
		require.False(t, items[i].Unpriced)
	}
}

func TestUsageLogModalityRoundTripAndHistoricalRecalculation(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:modality_usage?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	p := client.Project.Create().SetName("modality-test").SetStatus(project.StatusActive).SaveX(ctx)
	req := client.Request.Create().SetProjectID(p.ID).SetModelID("image-test").SetStatus(request.StatusCompleted).SetRequestBody(objects.JSONRawMessage(`{}`)).SaveX(ctx)
	price := imageTokenTestPrice()
	snapshot := &objects.RequestCostSnapshot{Price: &price, ModelPrice: &price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	svc := NewUsageLogService(client, nil, nil)
	u := imageTokenTestUsage()
	u.PromptTokensDetails.CachedTokens = 1200
	u.PromptTokensDetails.CachedTokensDetails = &llm.CachedTokensDetails{TextTokens: lo.ToPtr(int64(400)), ImageTokens: lo.ToPtr(int64(800))}
	params := CreateUsageLogParams{RequestID: req.ID, ProjectID: p.ID, ActualModelID: "image-test", Usage: u, Source: usagelog.SourceAPI, CostSnapshot: snapshot}
	row, err := svc.CreateUsageLog(ctx, params)
	require.NoError(t, err)
	stored := client.UsageLog.GetX(ctx, row.ID)
	require.Equal(t, lo.ToPtr(int64(1000)), stored.PromptTextTokens)
	require.Equal(t, lo.ToPtr(int64(2000)), stored.PromptImageTokens)
	require.Equal(t, lo.ToPtr(int64(400)), stored.PromptTextCachedTokens)
	require.Equal(t, lo.ToPtr(int64(800)), stored.PromptImageCachedTokens)
	require.InDelta(t, 0.0447, *stored.TotalCost, 1e-12)
	// Force reconstruction from retained token and price evidence, not subtotals.
	stored.CostItems = nil
	cost, ok := usageStandardCost(stored, nil)
	require.True(t, ok)
	require.Equal(t, "0.0447", cost.String())
	stored.PromptTextTokens, stored.PromptImageTokens = nil, nil
	_, ok = usageStandardCost(stored, nil)
	require.False(t, ok, "historical missing counts must not be inferred from the total")
	// Genuine zeros survive storage; omitted evidence remains SQL NULL.
	u.PromptTokensDetails.ImageTokens = lo.ToPtr(int64(0))
	u.PromptTokens, u.TotalTokens = 1000, 2000
	u.PromptTokensDetails.CachedTokens, u.PromptTokensDetails.CachedTokensDetails = 0, nil
	zero, err := svc.CreateUsageLog(ctx, params)
	require.NoError(t, err)
	zero = client.UsageLog.GetX(ctx, zero.ID)
	require.Equal(t, lo.ToPtr(int64(0)), zero.PromptImageTokens)
	require.Nil(t, zero.PromptImageCachedTokens)
}

func TestUsageLogModalityUpgradeLeavesLegacyCountsUnknown(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:modality_upgrade?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	p := client.Project.Create().SetName("legacy-project").SaveX(ctx)
	req := client.Request.Create().SetProjectID(p.ID).SetModelID("legacy").SetStatus(request.StatusCompleted).SetRequestBody(objects.JSONRawMessage(`{}`)).SaveX(ctx)
	legacy := client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(p.ID).SetModelID("legacy").
		SetPromptTokens(1000).SetCompletionTokens(0).SetTotalTokens(1000).SetTotalCost(0.005).SaveX(ctx)
	// Recreate the pre-upgrade shape, then run the same Ent-managed migration.
	for _, column := range []string{"prompt_text_tokens", "prompt_image_tokens", "prompt_text_cached_tokens", "prompt_image_cached_tokens"} {
		require.NoError(t, client.Driver().Exec(ctx, "ALTER TABLE usage_logs DROP COLUMN "+column, []any{}, nil))
	}
	require.NoError(t, client.Schema.Create(ctx))
	row := client.UsageLog.GetX(ctx, legacy.ID)
	require.Nil(t, row.PromptTextTokens)
	require.Nil(t, row.PromptImageTokens)
	require.Nil(t, row.PromptTextCachedTokens)
	require.Nil(t, row.PromptImageCachedTokens)
	require.Equal(t, int64(1000), row.PromptTokens)
	require.Equal(t, 0.005, *row.TotalCost)
}
