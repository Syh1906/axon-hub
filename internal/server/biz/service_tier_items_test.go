package biz

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestServiceTierItemsPreviewAndRestoration(t *testing.T) {
	price := serviceTierTestPrice()
	price.ServiceTiers.Fast.Items = []objects.ServiceTierItemRule{
		{ItemCode: objects.PriceItemCodeCompletion, Method: objects.ServiceTierItemMultiplier, Value: lo.ToPtr(decimal.RequireFromString("2.5"))},
		{ItemCode: objects.PriceItemCodePromptCachedToken, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.RequireFromString("0.3"))},
	}
	snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.8")}, ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}}
	usage := &llm.Usage{PromptTokens: 272001, CompletionTokens: 2000, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 60000}}
	items, total := computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "1.4312064", total.String())
	for i, expected := range []string{"1.696008", "0.075", "0.018"} {
		require.Equal(t, expected, items[i].PricingEvidence.ModeSubtotal.String())
	}
	require.Nil(t, items[2].PricingEvidence.ModeMultiplier)
	require.Equal(t, "0.3", items[2].PricingEvidence.FinalUnitPrice.String())
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restored objects.RequestCostSnapshot
	require.NoError(t, json.Unmarshal(encoded, &restored))
	price.ServiceTiers.Fast.Items[1].Value = lo.ToPtr(decimal.NewFromInt(100))
	_, again := computeCostFromSnapshot(usage, *restored.CurrentPrice(), &restored)
	require.True(t, total.Equal(again))
}

func TestServiceTierItemsContextInheritanceAndUnpriced(t *testing.T) {
	price := serviceTierTestPrice()
	price.ServiceTiers.Fast.Items = []objects.ServiceTierItemRule{{ItemCode: objects.PriceItemCodeCompletion, Method: objects.ServiceTierItemMultiplier, Value: lo.ToPtr(decimal.NewFromInt(3))}}
	price.VolumeTiers[0].ServiceTiers = &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(4)), Items: []objects.ServiceTierItemRule{{ItemCode: objects.PriceItemCodeUsage, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.Zero)}}}}
	snap := &objects.RequestCostSnapshot{PricedAt: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}, ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}}
	usage := &llm.Usage{PromptTokens: 300000, CompletionTokens: 1000000}
	items, total := computeCostFromSnapshot(usage, price, snap)
	require.Equal(t, "60", total.String()) // context default 4 precedes general output 3
	require.Equal(t, "0", items[0].PricingEvidence.FinalUnitPrice.String())
	price.Schedule = &objects.PriceSchedule{Timezone: "UTC", Overrides: []objects.PriceOverride{{Name: "day", Items: price.Items, When: objects.OverrideWhen{DailyTime: &objects.DailyTimeRange{Start: "00:00", End: "23:59"}}}}}
	items, total = computeCostFromSnapshot(usage, price, snap)
	require.Equal(t, "31.2", total.String()) // schedule discards context multiplier 4 and zero input override
	require.False(t, items[0].PricingEvidence.Context.LongContext)
	price.Schedule.Overrides[0].ServiceTiers = &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Items: []objects.ServiceTierItemRule{{ItemCode: objects.PriceItemCodeCompletion, Method: objects.ServiceTierItemUnpriced}}}}
	items, _ = computeCostFromSnapshot(usage, price, snap)
	require.True(t, items[1].Unpriced)
	usage.CompletionTokens = 0
	items, _ = computeCostFromSnapshot(usage, price, snap)
	require.False(t, items[1].Unpriced)
}

func TestServiceTierItemsBudgetUsesFinalOutputAndVariantPrices(t *testing.T) {
	price := serviceTierTestPrice()
	price.VolumeTiers = nil
	price.Items = price.Items[1:2]
	price.Items[0].Pricing.UsagePerUnit = lo.ToPtr(decimal.Zero)
	price.ServiceTiers.Fast = &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Items: []objects.ServiceTierItemRule{{ItemCode: objects.PriceItemCodeCompletion, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(25))}}}
	snap := &objects.RequestCostSnapshot{ModelPrice: &price, ModelOutputLimit: 1000000, Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}, ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}}
	estimate, err := (&BudgetService{}).Estimate(context.Background(), &ent.RequestExecution{CostSnapshot: snap}, &llm.Request{}, nil, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2500000000), int64(estimate))
	price.Items = []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeWriteCachedTokens, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}, PromptWriteCacheVariants: []objects.PromptWriteCacheVariant{{VariantCode: objects.PromptWriteCacheVariantCode1Hour, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(4))}}}}}
	price.ServiceTiers.Fast = &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(1)), Items: []objects.ServiceTierItemRule{
		{ItemCode: objects.PriceItemCodeWriteCachedTokens, VariantCode: objects.PromptWriteCacheVariantCode5Min, Method: objects.ServiceTierItemMultiplier, Value: lo.ToPtr(decimal.NewFromInt(10))},
		{ItemCode: objects.PriceItemCodeWriteCachedTokens, VariantCode: objects.PromptWriteCacheVariantCode1Hour, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(1))},
	}}
	estimate, err = (&BudgetService{}).Estimate(context.Background(), &ent.RequestExecution{CostSnapshot: snap}, &llm.Request{}, nil, 1000000)
	require.NoError(t, err)
	require.Equal(t, int64(2000000000), int64(estimate))
	items, total := computeCostFromSnapshot(&llm.Usage{PromptTokens: 1000000, PromptTokensDetails: &llm.PromptTokensDetails{WriteCachedTokens: 1000000, WriteCached5MinTokens: 500000, WriteCached1HourTokens: 500000}}, price, snap)
	require.Len(t, items, 2)
	require.Equal(t, "10.5", total.String())
}
