package biz

import (
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestUsageStandardCostPreservesModesAndHandlesZeroMultiplier(t *testing.T) {
	items := []objects.CostItem{
		{Subtotal: decimal.Zero, Multiplier: lo.ToPtr(decimal.Zero), PricingEvidence: &objects.CostPricingEvidence{
			BaseSubtotal: decimal.NewFromInt(10), ModeSubtotal: lo.ToPtr(decimal.NewFromInt(20)), CostMultiplier: decimal.Zero}},
		{Subtotal: decimal.NewFromInt(3), Multiplier: lo.ToPtr(decimal.RequireFromString("0.5"))},
	}
	cost, ok := usageStandardCost(&ent.UsageLog{TotalCost: lo.ToPtr(3.0), CostItems: items}, nil)
	require.True(t, ok)
	require.Equal(t, "26", cost.String(), "mode price stays 20 and each item's channel multiplier is removed independently")
	items[0].PricingEvidence.ModeSubtotal = nil
	items[0].PricingEvidence.ModeMultiplier = lo.ToPtr(decimal.NewFromInt(2))
	cost, ok = standardCostFromItems(items)
	require.True(t, ok)
	require.Equal(t, "26", cost.String(), "older mode evidence is also sufficient at zero multiplier")
	items[0].Unpriced = true
	_, ok = standardCostFromItems(items)
	require.False(t, ok)
	_, ok = usageStandardCost(&ent.UsageLog{CostItems: items}, nil)
	require.False(t, ok)
}

func TestUsageStandardCostReconstructsHistoricalVersion(t *testing.T) {
	price := objects.ModelPrice{Multiplier: lo.ToPtr(decimal.Zero), Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage,
		Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}}}}
	row := &ent.UsageLog{TotalCost: lo.ToPtr(0.0), PromptTokens: 1_000_000, CostPriceReferenceID: "historical", CreatedAt: time.Now()}
	cost, ok := usageStandardCost(row, map[string]objects.ModelPrice{"historical": price})
	require.True(t, ok)
	require.Equal(t, "2", cost.String())
	_, ok = usageStandardCost(row, nil)
	require.False(t, ok, "do not use today's model price or invent a 1x multiplier for missing history")
	row.CostSnapshot = &objects.RequestCostSnapshot{ModelPrice: &price, PricedAt: time.Now()}
	cost, ok = usageStandardCost(row, nil)
	require.True(t, ok)
	require.Equal(t, "2", cost.String())
	require.True(t, price.Multiplier.IsZero(), "normalization must not mutate the stored price")
}

func TestChannelTodayUsageStandardCostAcrossBatches(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	for i := 0; i < channelUsageBatchSize+1; i++ {
		req, _ := f.addRequest(t, start, "same-model", request.StatusCompleted, 100, 10, 50, 0, 0, nil)
		f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).SetCostItems([]objects.CostItem{{
			Subtotal: decimal.Zero, PricingEvidence: &objects.CostPricingEvidence{ModeSubtotal: lo.ToPtr(decimal.NewFromInt(2))},
		}}).SaveX(f.ctx)
	}
	f.addRequest(t, start, "unknown-model", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	rows, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "same-model", rows[0].Model)
	require.Equal(t, channelUsageBatchSize+1, rows[0].RequestCount)
	require.NotNil(t, rows[0].StandardCost)
	require.Equal(t, float64((channelUsageBatchSize+1)*2), *rows[0].StandardCost)
	require.Zero(t, rows[0].TotalCost)
	require.Zero(t, rows[0].StandardUnpricedCount)
	require.Nil(t, rows[1].StandardCost)
	require.Equal(t, 1, rows[1].StandardUnpricedCount)
}

func TestChannelTodayUsageStandardCostLoadsSavedVersions(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	svc := &ChannelService{AbstractService: &AbstractService{db: f.client}}
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 5)}}
	prices, err := svc.SaveChannelModelPrices(f.ctx, f.channel.ID, []SaveChannelModelPriceInput{{ModelID: "model", Price: price}}, lo.ToPtr(decimal.Zero))
	require.NoError(t, err)
	req, _ := f.addRequest(t, start, "model", request.StatusCompleted, 100, 10, 0, 0, 0, nil)
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).SetCostPriceReferenceID(prices[0].ReferenceID).SaveX(f.ctx)
	_, err = svc.SaveChannelModelPrices(f.ctx, f.channel.ID, nil, nil)
	require.NoError(t, err)
	rows, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 5.0, *rows[0].StandardCost, "deleted channel overrides retain their immutable historical version")
}
