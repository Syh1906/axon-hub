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
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func cacheWritePrice(base, hour string) objects.ModelPrice {
	return objects.ModelPrice{Items: []objects.ModelPriceItem{{
		ItemCode: objects.PriceItemCodeWriteCachedTokens,
		Pricing:  objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString(base))},
		PromptWriteCacheVariants: []objects.PromptWriteCacheVariant{{VariantCode: objects.PromptWriteCacheVariantCode1Hour,
			Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString(hour))}}},
	}}}
}

func TestCacheWriteBillingPricesAndActualDuration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rule      objects.CacheWriteBilling
		fivePrice string
		want      string
	}{
		{"historical", "", "", "6.5"},
		{"reported", objects.CacheWriteBillingReported, "", "6.5"},
		{"base-price", objects.CacheWriteBillingSwap, "", "5.6875"},
		{"specific-price", objects.CacheWriteBillingSwap, "3", "6.175"},
		{"explicit-zero", objects.CacheWriteBillingSwap, "0", "3.25"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price := cacheWritePrice("2.5", "5")
			if tc.fivePrice != "" {
				price.Items[0].PromptWriteCacheVariants = append(price.Items[0].PromptWriteCacheVariants, objects.PromptWriteCacheVariant{
					VariantCode: objects.PromptWriteCacheVariantCode5Min,
					Pricing:     objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.RequireFromString(tc.fivePrice))},
				})
			}
			usage := &llm.Usage{PromptTokens: 1250000, PromptTokensDetails: &llm.PromptTokensDetails{
				WriteCachedTokens: 1250000, WriteCached5MinTokens: 500000, WriteCached1HourTokens: 750000,
			}}
			snapshot := &objects.RequestCostSnapshot{CacheWriteBilling: tc.rule, Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("1.3")}}
			items, total := computeCostFromSnapshot(usage, price, snapshot)
			require.Equal(t, tc.want, total.String())
			require.Len(t, items, 2)
			require.Equal(t, objects.PromptWriteCacheVariantCode5Min, items[0].PromptWriteCacheVariantCode)
			require.Equal(t, objects.PromptWriteCacheVariantCode1Hour, items[1].PromptWriteCacheVariantCode)
			require.Equal(t, int64(750000), items[1].Quantity)
			require.Equal(t, int64(750000), usage.PromptTokensDetails.WriteCached1HourTokens)
			wantVariant := objects.PromptWriteCacheVariantCode1Hour
			wantFiveVariant := objects.PromptWriteCacheVariantCode5Min
			if tc.rule == objects.CacheWriteBillingSwap {
				wantVariant = objects.PromptWriteCacheVariantCode5Min
				wantFiveVariant = objects.PromptWriteCacheVariantCode1Hour
			}
			require.Equal(t, wantFiveVariant, *items[0].PricingEvidence.CacheWritePricingVariant)
			require.Equal(t, wantVariant, *items[1].PricingEvidence.CacheWritePricingVariant)
		})
	}
}

func TestCacheWriteBillingContextScheduleAndMode(t *testing.T) {
	price := cacheWritePrice("2.5", "5")
	price.VolumeTiers = []objects.ModelPriceVolumeTier{{Above: 100000, LongContext: true, Items: cacheWritePrice("5", "10").Items}}
	usage := &llm.Usage{PromptTokens: 200000, PromptTokensDetails: &llm.PromptTokensDetails{WriteCachedTokens: 100000, WriteCached1HourTokens: 100000}}
	snapshot := &objects.RequestCostSnapshot{CacheWriteBilling: objects.CacheWriteBillingSwap, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	items, total := computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "0.5", total.String())
	require.True(t, items[0].PricingEvidence.Context.LongContext)
	price.ServiceTiers = &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Items: []objects.ServiceTierItemRule{
		{ItemCode: objects.PriceItemCodeWriteCachedTokens, VariantCode: objects.PromptWriteCacheVariantCode5Min, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(7))},
		{ItemCode: objects.PriceItemCodeWriteCachedTokens, VariantCode: objects.PromptWriteCacheVariantCode1Hour, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(20))},
	}}}
	snapshot.ServiceTier = &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}
	items, total = computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "0.7", total.String())
	require.Equal(t, "5", items[0].PricingEvidence.BaseUnitPrice.String())
	require.Equal(t, "7", items[0].PricingEvidence.FinalUnitPrice.String())
	snapshot.ServiceTier = nil
	price.Schedule = &objects.PriceSchedule{Timezone: "UTC", Overrides: []objects.PriceOverride{{Name: "cache-window", Items: cacheWritePrice("4", "9").Items}}}
	items, total = computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "0.4", total.String())
	require.Equal(t, "schedule", items[0].PricingEvidence.Context.Kind)
	usage.PromptTokensDetails = &llm.PromptTokensDetails{WriteCachedTokens: 100000}
	items, total = computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "0.4", total.String())
	require.Empty(t, items[0].PromptWriteCacheVariantCode)
	require.Nil(t, items[0].PricingEvidence.CacheWritePricingVariant)
}

func TestCacheWriteBillingSaveSnapshotBudgetAndReadback(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(ent.NewContext(f.ctx, f.client))
	ch := f.client.Channel.Create().SetName("cache-billing").SetType(channel.TypeOpenaiFake).
		SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	svc := NewChannelServiceForTest(f.client)
	rule := objects.CacheWriteBillingSwap
	_, err := svc.SaveChannelModelPrices(ctx, ch.ID, nil, lo.ToPtr(decimal.RequireFromString("1.3")), ChannelPricingSettings{CacheWriteBilling: &rule})
	require.NoError(t, err)
	saved := f.client.Channel.GetX(ctx, ch.ID)
	require.Equal(t, rule, *saved.Settings.CacheWriteBilling)
	require.Zero(t, f.client.ChannelModelPrice.Query().CountX(ctx))
	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{}})
	require.NoError(t, err)
	saved = f.client.Channel.GetX(ctx, ch.ID)
	require.Equal(t, rule, *saved.Settings.CacheWriteBilling)
	require.Equal(t, "1.3", saved.Settings.ModelPriceMultiplier.String())
	price := cacheWritePrice("2.5", "5")
	snapshot := captureRequestCostSnapshot(ctx, f.client, &Channel{Channel: saved}, "model", &objects.ModelCard{Price: &price})
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restored objects.RequestCostSnapshot
	require.NoError(t, json.Unmarshal(raw, &restored))
	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, nil, nil, ChannelPricingSettings{CacheWriteBilling: lo.ToPtr(objects.CacheWriteBillingReported)})
	require.NoError(t, err)
	require.Equal(t, rule, restored.CacheWriteBilling)
	usage := &llm.Usage{PromptTokens: 1000000, TotalTokens: 1000000, PromptTokensDetails: &llm.PromptTokensDetails{WriteCachedTokens: 1000000, WriteCached1HourTokens: 1000000}}
	service := NewUsageLogService(f.client, f.service.system, svc)
	service.InjectUsageCost(ctx, ch.ID, "model", usage, &restored)
	require.NotNil(t, usage.Cost)
	require.InDelta(t, 3.25, *usage.Cost, 1e-12)
	settled, err := service.actualBudgetCost(ctx, usage, &restored)
	require.NoError(t, err)
	require.Equal(t, "3.25000000", settled.String())
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	req := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "model")
	log, err := service.CreateUsageLog(ctx, CreateUsageLogParams{RequestID: req.ID, ProjectID: f.project.ID, ChannelID: ch.ID, ActualModelID: "model", Usage: usage, Source: "api", Format: string(llm.APIFormatAnthropicMessage), APIKeyID: &f.key.ID, CostSnapshot: &restored})
	require.NoError(t, err)
	log = f.client.UsageLog.GetX(ctx, log.ID)
	require.Equal(t, int64(1000000), log.PromptWriteCachedTokens1h)
	require.Equal(t, objects.PromptWriteCacheVariantCode1Hour, log.CostItems[0].PromptWriteCacheVariantCode)
	require.Equal(t, objects.PromptWriteCacheVariantCode5Min, *log.CostItems[0].PricingEvidence.CacheWritePricingVariant)
	standard, known := usageStandardCost(log, nil)
	require.True(t, known)
	require.Equal(t, "2.5", standard.String())
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, objects.PromptWriteCacheVariantCode5Min, *page.Items[0].CostItems[0].PricingEvidence.CacheWritePricingVariant)
	_, err = svc.SaveChannelModelPrices(ctx, ch.ID, nil, nil, ChannelPricingSettings{CacheWriteBilling: lo.ToPtr(objects.CacheWriteBilling("invalid"))})
	require.Error(t, err)
	require.Equal(t, objects.CacheWriteBillingReported, *f.client.Channel.GetX(ctx, ch.ID).Settings.CacheWriteBilling)
}

func TestCacheWriteBillingBudgetEstimate(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "fast"}[fast], func(t *testing.T) {
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "7")
			price := cacheWritePrice("2.5", "5")
			snap := &objects.RequestCostSnapshot{Price: &price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("1.3")}}
			if fast {
				price.ServiceTiers = &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(1)), Items: []objects.ServiceTierItemRule{
					{ItemCode: objects.PriceItemCodeWriteCachedTokens, VariantCode: objects.PromptWriteCacheVariantCode1Hour, Method: objects.ServiceTierItemUnpriced},
				}}}
				snap.ServiceTier = &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}
			}
			execution := &ent.RequestExecution{CostSnapshot: snap}
			quote, err := f.service.Estimate(f.ctx, execution, &llm.Request{}, nil, 1000000)
			if fast {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, "6.50000000", quote.String())
			}
			snap.CacheWriteBilling = objects.CacheWriteBillingSwap
			quote, err = f.service.Estimate(f.ctx, execution, &llm.Request{}, nil, 1000000)
			// Either actual duration may occur, so swapping still reserves the larger rate.
			if fast {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "6.50000000", quote.String())
			require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, quote, nil))
		})
	}
}

func TestCacheWriteBillingChangesPricingMode(t *testing.T) {
	for _, mode := range []objects.PricingMode{objects.PricingModeUsagePerUnit, objects.PricingModeTiered} {
		t.Run(string(mode), func(t *testing.T) {
			price := cacheWritePrice("5", "8")
			original := objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: lo.ToPtr(decimal.NewFromInt(8))}
			price.Items[0].PromptWriteCacheVariants[0].Pricing = original
			if mode == objects.PricingModeTiered {
				price.Items[0].Pricing = objects.Pricing{Mode: mode, UsageTiered: &objects.TieredPricing{Tiers: []objects.PriceTier{
					{UpTo: lo.ToPtr(int64(500000)), PricePerUnit: decimal.NewFromInt(4)},
					{PricePerUnit: decimal.NewFromInt(6)},
				}}}
			}
			usage := &llm.Usage{PromptTokens: 1000000, PromptTokensDetails: &llm.PromptTokensDetails{WriteCachedTokens: 1000000, WriteCached1HourTokens: 1000000}}
			snap := &objects.RequestCostSnapshot{CacheWriteBilling: objects.CacheWriteBillingSwap, Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}, ManualMultiplier: decimal.NewFromInt(1)}
			items, total := computeCostFromSnapshot(usage, price, snap)
			require.Equal(t, "5", total.String())
			require.Equal(t, int64(1000000), items[0].Quantity)
			require.Equal(t, objects.PromptWriteCacheVariantCode1Hour, items[0].PromptWriteCacheVariantCode)
			require.Equal(t, objects.PromptWriteCacheVariantCode5Min, *items[0].PricingEvidence.CacheWritePricingVariant)
			require.Equal(t, original, price.Items[0].PromptWriteCacheVariants[0].Pricing)
			snap.CacheWriteBilling = objects.CacheWriteBillingReported
			_, originalTotal := computeCostFromSnapshot(usage, price, snap)
			require.Equal(t, "8", originalTotal.String())
		})
	}
	t.Run("base-fixed-fee-with-five-minute-unit-price", func(t *testing.T) {
		price := cacheWritePrice("5", "8")
		price.Items[0].Pricing = objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: lo.ToPtr(decimal.NewFromInt(8))}
		price.Items[0].PromptWriteCacheVariants = []objects.PromptWriteCacheVariant{{VariantCode: objects.PromptWriteCacheVariantCode5Min, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(5))}}}
		usage := &llm.Usage{PromptTokens: 1000000, PromptTokensDetails: &llm.PromptTokensDetails{WriteCachedTokens: 1000000, WriteCached1HourTokens: 1000000}}
		snap := &objects.RequestCostSnapshot{CacheWriteBilling: objects.CacheWriteBillingSwap, Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}, ManualMultiplier: decimal.NewFromInt(1)}
		items, total := computeCostFromSnapshot(usage, price, snap)
		require.Equal(t, "5", total.String())
		require.Equal(t, int64(1000000), items[0].Quantity)
		require.Len(t, price.Items[0].PromptWriteCacheVariants, 1)
		usage.PromptTokensDetails = &llm.PromptTokensDetails{WriteCachedTokens: 1000000}
		items, total = computeCostFromSnapshot(usage, price, snap)
		require.Equal(t, "8", total.String())
		require.Empty(t, items[0].PromptWriteCacheVariantCode)
		require.Nil(t, items[0].PricingEvidence.CacheWritePricingVariant)
	})
}

func TestCacheWriteBillingReverseContextScheduleAndMode(t *testing.T) {
	price := cacheWritePrice("2.5", "5")
	price.VolumeTiers = []objects.ModelPriceVolumeTier{{Above: 100000, LongContext: true, Items: cacheWritePrice("5", "10").Items}}
	usage := &llm.Usage{PromptTokens: 200000, PromptTokensDetails: &llm.PromptTokensDetails{WriteCachedTokens: 100000, WriteCached5MinTokens: 100000}}
	snapshot := &objects.RequestCostSnapshot{CacheWriteBilling: objects.CacheWriteBillingSwap, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	items, total := computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "1", total.String())
	require.True(t, items[0].PricingEvidence.Context.LongContext)
	require.Equal(t, objects.PromptWriteCacheVariantCode5Min, items[0].PromptWriteCacheVariantCode)
	require.Equal(t, objects.PromptWriteCacheVariantCode1Hour, *items[0].PricingEvidence.CacheWritePricingVariant)
	require.Equal(t, int64(100000), usage.PromptTokensDetails.WriteCached5MinTokens)
	price.ServiceTiers = &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Items: []objects.ServiceTierItemRule{
		{ItemCode: objects.PriceItemCodeWriteCachedTokens, VariantCode: objects.PromptWriteCacheVariantCode5Min, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(7))},
		{ItemCode: objects.PriceItemCodeWriteCachedTokens, VariantCode: objects.PromptWriteCacheVariantCode1Hour, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(20))},
	}}}
	snapshot.ServiceTier = &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}
	items, total = computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "2", total.String())
	require.Equal(t, "10", items[0].PricingEvidence.BaseUnitPrice.String())
	require.Equal(t, "20", items[0].PricingEvidence.FinalUnitPrice.String())
	snapshot.ServiceTier = nil
	price.Schedule = &objects.PriceSchedule{Timezone: "UTC", Overrides: []objects.PriceOverride{{Name: "cache-window", Items: cacheWritePrice("4", "9").Items}}}
	_, total = computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "0.9", total.String())
	price = cacheWritePrice("2.5", "5")
	price.Items[0].Pricing = objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: lo.ToPtr(decimal.NewFromInt(8))}
	items, total = computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "0.5", total.String())
	require.Equal(t, int64(100000), items[0].Quantity)
	require.Equal(t, objects.PricingModeFlatFee, price.Items[0].Pricing.Mode)
}

func TestCacheWriteBillingBudgetUsesSelectedPricingMultiplier(t *testing.T) {
	f := newBudgetFixture(t)
	price := cacheWritePrice("5", "8")
	price.Items[0].PromptWriteCacheVariants[0].Pricing = objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: lo.ToPtr(decimal.NewFromInt(8))}
	snapshot := &objects.RequestCostSnapshot{Price: &price, CacheWriteBilling: objects.CacheWriteBillingSwap, ManualMultiplier: decimal.NewFromInt(2), Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.1")}}
	quote, err := f.service.Estimate(f.ctx, &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{}, nil, 1000000)
	require.NoError(t, err)
	require.Equal(t, "16.00000000", quote.String())
	usage := &llm.Usage{PromptTokens: 1000000, PromptTokensDetails: &llm.PromptTokensDetails{WriteCachedTokens: 1000000, WriteCached5MinTokens: 1000000}}
	_, total := computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "16", total.String())
	price.Items[0].Pricing, price.Items[0].PromptWriteCacheVariants[0].Pricing = price.Items[0].PromptWriteCacheVariants[0].Pricing, price.Items[0].Pricing
	snapshot.ManualMultiplier, snapshot.Applied.Value = snapshot.Applied.Value, snapshot.ManualMultiplier
	quote, err = f.service.Estimate(f.ctx, &ent.RequestExecution{CostSnapshot: snapshot}, &llm.Request{}, nil, 1000000)
	require.NoError(t, err)
	require.Equal(t, "10.00000000", quote.String())
	_, total = computeCostFromSnapshot(usage, price, snapshot)
	require.Equal(t, "10", total.String())
}
