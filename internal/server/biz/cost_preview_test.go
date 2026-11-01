package biz

import (
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestHistoricalCostPreviewDoesNotRewriteBilling(t *testing.T) {
	f := newConsumptionFixture(t)
	req, _ := f.addRequest(t, time.Now().Add(-time.Hour), "image", request.StatusCompleted, 100, 0, 0, 0, 0, nil)
	price := imageTestPrice(true)
	snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, PricedAt: req.CreatedAt, ManualMultiplier: decimal.NewFromInt(1), Images: []objects.ImageOutput{{Source: "unknown", InspectionError: "image_fetch_failed"}}}
	f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("image").SetStatus(requestexecution.StatusCompleted).
		SetRequestBody([]byte(`{"size":"1536x1024"}`)).SetResponseBody([]byte(`{"data":[{"url":"https://example.invalid/image"}]}`)).SaveX(f.ctx)
	log := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).OnlyX(f.ctx)
	f.client.UsageLog.UpdateOne(log).ClearTotalCost().SetCostSnapshot(snapshot).SaveX(f.ctx)
	preview, err := PreviewRequestCost(f.ctx, f.client, req.ID)
	require.NoError(t, err)
	require.NotNil(t, preview.ProposedCost, "preview reason: %s; images: %+v", preview.Reason, preview.Images)
	require.Equal(t, 0.04, *preview.ProposedCost)
	require.Equal(t, "preview_only", preview.Reason)
	require.Equal(t, "request_dimensions", preview.Images[0].Source)
	require.Equal(t, "image_fetch_failed", preview.Images[0].InspectionError)
	require.Zero(t, preview.Images[0].Width)
	require.Nil(t, f.client.UsageLog.GetX(f.ctx, log.ID).TotalCost)
	require.Equal(t, "unknown", f.client.UsageLog.GetX(f.ctx, log.ID).CostSnapshot.Images[0].Source)
	// Channel-managed prices are stored by immutable reference, not embedded.
	f.client.ChannelModelPriceVersion.Create().SetChannelID(f.channel.ID).SetChannelModelPriceID(1).SetModelID("image").SetReferenceID("historical-image-price").SetStatus("archived").SetEffectiveStartAt(req.CreatedAt).SetPrice(price).SaveX(f.ctx)
	snapshot.ModelPrice, snapshot.PriceReferenceID = nil, "historical-image-price"
	f.client.UsageLog.UpdateOne(log).SetCostSnapshot(snapshot).SaveX(f.ctx)
	preview, err = PreviewRequestCost(f.viewer(t, f.project.ID, "read_requests"), f.client, req.ID)
	require.NoError(t, err)
	require.NotNil(t, preview.ProposedCost)
	require.Equal(t, 0.04, *preview.ProposedCost)
	// A request reader from another project cannot inspect historical prices.
	_, err = PreviewRequestCost(f.viewer(t, f.project.ID+1, "read_requests"), f.client, req.ID)
	require.Error(t, err)
}

func TestHistoricalPreviewPreservesMissingTokenUsage(t *testing.T) {
	f := newConsumptionFixture(t)
	req, _ := f.addRequest(t, time.Now().Add(-time.Hour), "mixed", request.StatusCompleted, 100, 0, 0, 0, 0, nil)
	fee, unit := decimal.RequireFromString("0.09"), decimal.NewFromInt(2)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{
		{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &fee}},
		{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: &unit}},
	}}
	snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, PricedAt: req.CreatedAt, ManualMultiplier: decimal.NewFromInt(1), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	items, _ := computeCostFromSnapshot(nil, price, snapshot)
	log := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).OnlyX(f.ctx)
	f.client.UsageLog.UpdateOne(log).SetPromptTokens(0).SetTotalTokens(0).ClearTotalCost().SetCostSnapshot(snapshot).SetCostItems(items).SaveX(f.ctx)
	preview, err := PreviewRequestCost(f.ctx, f.client, req.ID)
	require.NoError(t, err)
	require.Nil(t, preview.ProposedCost, "unknown token usage must not become confirmed zero token cost")
	require.Equal(t, "usage_missing", preview.Reason)
	// An explicit zero usage response is different from absent provider usage.
	items, _ = computeCostFromSnapshot(&llm.Usage{}, price, snapshot)
	f.client.UsageLog.UpdateOne(log).SetCostItems(items).SaveX(f.ctx)
	preview, err = PreviewRequestCost(f.ctx, f.client, req.ID)
	require.NoError(t, err)
	require.NotNil(t, preview.ProposedCost)
	require.Equal(t, 0.09, *preview.ProposedCost)
}

func TestHistoricalFlatFeePreviewWithoutUsage(t *testing.T) {
	f := newConsumptionFixture(t)
	fee := decimal.RequireFromString("0.09")
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &fee}}}}
	for _, status := range []request.Status{request.StatusCompleted, request.StatusFailed, request.StatusCanceled, request.StatusProcessing} {
		t.Run(string(status), func(t *testing.T) {
			req, _ := f.addRequest(t, time.Now().Add(-time.Hour), "model", status, 0, 0, 0, 0, 0, nil)
			snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, PricedAt: req.CreatedAt, ManualMultiplier: decimal.NewFromInt(1)}
			f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("model").SetStatus(requestexecution.Status(status)).SetRequestBody([]byte(`{}`)).SetCostSnapshot(snapshot).SaveX(f.ctx)
			preview, err := PreviewRequestCost(f.ctx, f.client, req.ID)
			require.NoError(t, err)
			if status == request.StatusCompleted {
				require.NotNil(t, preview.ProposedCost, preview.Reason)
				require.Equal(t, 0.09, *preview.ProposedCost)
			} else {
				require.Nil(t, preview.ProposedCost)
				require.Equal(t, "request_not_completed", preview.Reason)
			}
			require.False(t, f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).ExistX(f.ctx))
		})
	}
}

func TestHistoricalPreviewPreservesCacheVariantUsage(t *testing.T) {
	f := newConsumptionFixture(t)
	req, _ := f.addRequest(t, time.Now().Add(-time.Hour), "cached", request.StatusCompleted, 1000000, 0, 0, 1000000, 2.5, nil)
	fee, fiveMinute, oneHour := decimal.RequireFromString("0.09"), decimal.NewFromInt(1), decimal.NewFromInt(4)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeWriteCachedTokens,
		Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &fee},
		PromptWriteCacheVariants: []objects.PromptWriteCacheVariant{
			{VariantCode: objects.PromptWriteCacheVariantCode5Min, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: &fiveMinute}},
			{VariantCode: objects.PromptWriteCacheVariantCode1Hour, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: &oneHour}},
		},
	}}}
	snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, PricedAt: req.CreatedAt, ManualMultiplier: decimal.NewFromInt(1), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	log := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).OnlyX(f.ctx)
	f.client.UsageLog.UpdateOne(log).SetPromptWriteCachedTokens5m(500000).SetPromptWriteCachedTokens1h(500000).SetCostSnapshot(snapshot).SaveX(f.ctx)
	preview, err := PreviewRequestCost(f.ctx, f.client, req.ID)
	require.NoError(t, err)
	require.NotNil(t, preview.ProposedCost, preview.Reason)
	require.Equal(t, 2.5, *preview.ProposedCost)
	require.Equal(t, 2.5, *f.client.UsageLog.GetX(f.ctx, log.ID).TotalCost)
}
