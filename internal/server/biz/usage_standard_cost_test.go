package biz

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/intercept"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestChannelTodayUsageCostItemsAcceptSQLiteText(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Now().UTC().Truncate(24 * time.Hour)
	req, _ := f.addRequest(t, start, "model", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).SetCostItems([]objects.CostItem{{
		Subtotal: decimal.NewFromInt(1), PricingEvidence: &objects.CostPricingEvidence{ModeSubtotal: lo.ToPtr(decimal.NewFromInt(2))},
	}}).SaveX(f.ctx)
	var result stdsql.Result
	require.NoError(t, f.client.Driver().Exec(f.ctx, "UPDATE usage_logs SET cost_items = CAST(cost_items AS TEXT)", []any{}, &result))
	rows, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 2.0, *rows[0].StandardCost)
}

func TestChannelUsageProjectionPreservesHistoricalEvidence(t *testing.T) {
	for _, item := range []objects.CostItem{
		{Subtotal: decimal.NewFromInt(6), Multiplier: lo.ToPtr(decimal.NewFromInt(2))},
		{Subtotal: decimal.Zero, PricingEvidence: &objects.CostPricingEvidence{ModeSubtotal: lo.ToPtr(decimal.NewFromInt(3)), CostMultiplier: decimal.Zero}},
		{Subtotal: decimal.Zero, PricingEvidence: &objects.CostPricingEvidence{BaseSubtotal: decimal.NewFromInt(2), ModeMultiplier: lo.ToPtr(decimal.NewFromInt(3))}},
		{Subtotal: decimal.NewFromInt(8), PricingEvidence: &objects.CostPricingEvidence{CostMultiplier: decimal.NewFromInt(4)}},
		{Subtotal: decimal.NewFromInt(8), Unpriced: true},
		{Image: &objects.ImageOutput{Width: 1024, Height: 768}},
	} {
		encoded, err := json.Marshal([]objects.CostItem{item})
		require.NoError(t, err)
		var projected channelUsageCostItems
		require.NoError(t, json.Unmarshal(encoded, &projected))
		want, known := standardCostFromItems([]objects.CostItem{item})
		got, projectedKnown := standardCostFromItems(projected)
		require.Equal(t, known, projectedKnown)
		require.True(t, want.Equal(got))
		require.Equal(t, item.Image, projected[0].Image)
	}
}

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
	for range channelUsageBatchSize + 1 {
		req, _ := f.addRequest(t, start, "model", request.StatusCompleted, 100, 10, 0, 0, 0, nil)
		f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).SetCostPriceReferenceID(prices[0].ReferenceID).SaveX(f.ctx)
	}
	_, err = svc.SaveChannelModelPrices(f.ctx, f.channel.ID, nil, nil)
	require.NoError(t, err)
	var versionReads atomic.Int64
	f.client.Intercept(intercept.Func(func(_ context.Context, query intercept.Query) error {
		if query.Type() == ent.TypeChannelModelPriceVersion {
			versionReads.Add(1)
		}
		return nil
	}))
	rows, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, float64((channelUsageBatchSize+1)*5), *rows[0].StandardCost, "deleted channel overrides retain their immutable historical version")
	require.EqualValues(t, 1, versionReads.Load(), "the same immutable price version is loaded once across batches")
}

func TestChannelTodayModelCostsUseMatchingPricedSamples(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f.addRequest(t, start, "priced", request.StatusCompleted, 100, 100, 0, 0, 2, nil)
	missing, _ := f.addRequest(t, start, "priced", request.StatusFailed, 900, 100, 0, 0, 0, nil)
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(missing.ID)).ClearTotalCost().ExecX(f.ctx)
	f.addRequest(t, start, "priced", request.StatusCompleted, 100, 100, 0, 0, 0, nil)
	flat, _ := f.addRequest(t, start, "priced", request.StatusCompleted, 0, 0, 0, 0, 0, nil)
	f.client.UsageLog.Create().SetRequestID(flat.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("priced").SetTotalCost(3).SaveX(f.ctx)
	partial, _ := f.addRequest(t, start, "priced", request.StatusCompleted, 100, 0, 0, 0, 5, nil)
	f.client.UsageLog.Create().SetRequestID(partial.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("priced").SetPromptTokens(50).SetTotalTokens(50).SaveX(f.ctx)
	f.addRequest(t, start, "unknown", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	rows, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "priced", rows[0].Model)
	require.Equal(t, 2, rows[0].TokenPricedRequestCount)
	require.Equal(t, 2, rows[0].UnpricedCount)
	require.NotNil(t, rows[0].AvgCostPerMillionTokens)
	require.Equal(t, 5000.0, *rows[0].AvgCostPerMillionTokens, "zero-cost tokens participate; unpriced requests and tokenless flat fees do not")
	require.Equal(t, 10.0, rows[0].TotalCost)
	require.Nil(t, rows[1].AvgCostPerMillionTokens)
}
