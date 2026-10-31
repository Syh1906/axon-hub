package biz

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func serviceTierTestPrice() objects.ModelPrice {
	items := func(input, output, cache string) []objects.ModelPriceItem {
		return []objects.ModelPriceItem{
			{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString(input))}},
			{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString(output))}},
			{ItemCode: objects.PriceItemCodePromptCachedToken, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString(cache))}},
		}
	}
	return objects.ModelPrice{Items: items("2", "10", "0.1"), VolumeTiers: []objects.ModelPriceVolumeTier{{Above: 272000, LongContext: true, Items: items("4", "15", "0.2")}}, ServiceTiers: &objects.ServiceTierPrices{
		Fast:      &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(2))},
		Ultrafast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(6))},
	}}
}

func TestServiceTierCostUsesContextRatesOnceAndFreezesEvidence(t *testing.T) {
	price := serviceTierTestPrice()
	usage := &llm.Usage{PromptTokens: 300000, CompletionTokens: 2000, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 60000}}
	snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.8")}, ManualMultiplier: decimal.RequireFromString("0.8"), ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, Client: "ultrafast", Outbound: "priority"}}
	snapshot.ServiceTier.Observe([]byte(`{"service_tier":"priority"}`))
	items, total := computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "1.6032", total.String())
	require.Len(t, items, 3)
	base := decimal.Zero
	for _, item := range items {
		base = base.Add(item.PricingEvidence.BaseSubtotal)
		require.True(t, item.PricingEvidence.Context.LongContext)
		require.Equal(t, int64(300000), item.PricingEvidence.Context.InputTokens)
		require.Equal(t, "fast", item.PricingEvidence.ServiceTier.Mode)
	}
	require.Equal(t, "1.002", base.String())
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restored objects.RequestCostSnapshot
	require.NoError(t, json.Unmarshal(data, &restored))
	price.ServiceTiers.Fast.Multiplier = lo.ToPtr(decimal.NewFromInt(9))
	_, again := computeCostFromSnapshot(usage, *restored.CurrentPrice(), &restored)
	require.Equal(t, total, again)
}

func TestServiceTierContextBoundariesAndZeroMultiplier(t *testing.T) {
	price := serviceTierTestPrice()
	snapshot := &objects.RequestCostSnapshot{Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}, ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}}
	for _, tokens := range []int64{271999, 272000, 272001} {
		items, _ := computeCostFromSnapshot(&llm.Usage{PromptTokens: tokens, CompletionTokens: 999999, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: tokens}}, price, snapshot)
		require.Equal(t, tokens > 272000, items[0].PricingEvidence.Context.LongContext)
	}
	price.ServiceTiers.Fast.Multiplier = lo.ToPtr(decimal.Zero)
	items, total := computeCostFromSnapshot(&llm.Usage{PromptTokens: 10000, CompletionTokens: 2000}, price, snapshot)
	require.True(t, total.IsZero())
	require.Equal(t, "0.02", items[0].PricingEvidence.BaseSubtotal.String())
	require.False(t, items[0].Unpriced)
	price.ServiceTiers = nil
	items, _ = computeCostFromSnapshot(&llm.Usage{PromptTokens: 10000}, price, snapshot)
	require.True(t, items[0].Unpriced)
}

func TestServiceTierBudgetEstimateUsesClientAndRejectsMissingRate(t *testing.T) {
	price := serviceTierTestPrice()
	snap := &objects.RequestCostSnapshot{ModelPrice: &price, Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.8")}, ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}, Client: "ultrafast", Upstream: "default", Observed: true}}
	service := &BudgetService{}
	estimate, err := service.Estimate(context.Background(), &ent.RequestExecution{CostSnapshot: snap}, &llm.Request{MaxTokens: lo.ToPtr(int64(2000))}, nil, 10000)
	require.NoError(t, err)
	require.GreaterOrEqual(t, int64(estimate), int64(19200000))
	price.ServiceTiers = nil
	_, err = service.Estimate(context.Background(), &ent.RequestExecution{CostSnapshot: snap}, &llm.Request{MaxTokens: lo.ToPtr(int64(2000))}, nil, 10000)
	require.ErrorContains(t, err, "service tier")
}

func TestServiceTierScheduleOverridesContextAndFixedFeesIgnoreMode(t *testing.T) {
	price := serviceTierTestPrice()
	snapshot := &objects.RequestCostSnapshot{PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.8")}, ManualMultiplier: decimal.RequireFromString("0.8"), ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "ultrafast"}}
	price.Schedule = &objects.PriceSchedule{Timezone: "UTC", Overrides: []objects.PriceOverride{{Name: "night", Items: price.Items}}}
	items, total := computeCostFromSnapshot(&llm.Usage{PromptTokens: 300000, CompletionTokens: 2000}, price, snapshot)
	require.Equal(t, "2.976", total.String())
	require.Equal(t, "schedule", items[0].PricingEvidence.Context.Kind)
	require.False(t, items[0].PricingEvidence.Context.LongContext)
	price.Schedule = nil
	price.VolumeTiers = nil
	price.Items = []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: lo.ToPtr(decimal.NewFromInt(3))}}}
	price.ServiceTiers = nil
	items, total = computeCostFromSnapshot(&llm.Usage{PromptTokens: 300000}, price, snapshot)
	require.Equal(t, "2.4", total.String())
	require.False(t, items[0].Unpriced)
	require.Equal(t, "1", items[0].PricingEvidence.ModeMultiplier.String())
}
