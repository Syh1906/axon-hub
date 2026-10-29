package biz

import (
	"context"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
)

func captureRequestCostSnapshot(ctx context.Context, db *ent.Client, ch *Channel, model string) *objects.RequestCostSnapshot {
	now := time.Now().UTC()
	manual := channelManualMultiplier(ch.Channel)
	snapshot := &objects.RequestCostSnapshot{PricedAt: now, ManualMultiplier: manual}
	if price, ok := ch.cachedModelPrices[model]; ok {
		priceCopy := price.Price
		snapshot.Price, snapshot.PriceReferenceID = &priceCopy, price.ReferenceID
		manual = price.Price.EffectiveMultiplier()
		snapshot.ManualMultiplier = manual
	}
	settings := ch.Settings.UpstreamSettings()
	var upstreamSnapshot *objects.UpstreamKeySnapshot
	if key, ok := contexts.GetChannelAPIKey(ctx); ok && !ch.Credentials.IsOAuth() {
		snapshot.KeyRef, snapshot.KeyLabel = upstream.KeyRef(key), upstream.MaskKey(key)
		if settings.Enabled && settings.CostMode == objects.ChannelCostModeUpstream {
			var err error
			upstreamSnapshot, err = loadRequestUpstreamSnapshot(ctx, db, ch.Channel, key)
			if err != nil {
				log.Warn(ctx, "load selected key rate; using manual estimate", log.Int("channel_id", ch.ID), log.Cause(err))
			}
		}
	}
	snapshot.Applied = appliedUpstreamMultiplier(settings, manual, upstreamSnapshot, now)
	return snapshot
}

func (s *UsageLogService) computeSnapshotUsageCost(ctx context.Context, usage *llm.Usage, snapshot *objects.RequestCostSnapshot) ([]objects.CostItem, *float64, string) {
	if snapshot.PriceReferenceID == "" {
		return nil, nil, ""
	}
	price := snapshot.Price
	if price == nil {
		version, err := authz.RunWithSystemBypass(ctx, "request-historical-cost-snapshot", func(ctx context.Context) (*ent.ChannelModelPriceVersion, error) {
			return s.entFromContext(ctx).ChannelModelPriceVersion.Query().Where(channelmodelpriceversion.ReferenceID(snapshot.PriceReferenceID)).Only(ctx)
		})
		if err != nil {
			log.Warn(ctx, "load historical cost price", log.Cause(err))
			return nil, nil, ""
		}
		price = &version.Price
	}
	items, total := computeCostFromSnapshot(usage, *price, snapshot)
	for _, item := range items {
		if item.Unpriced {
			return items, nil, snapshot.PriceReferenceID
		}
	}
	return items, lo.ToPtr(total.InexactFloat64()), snapshot.PriceReferenceID
}

func computeCostFromSnapshot(usage *llm.Usage, price objects.ModelPrice, snapshot *objects.RequestCostSnapshot) ([]objects.CostItem, decimal.Decimal) {
	price.Multiplier = nil
	items, _ := ComputeUsageCost(usage, price, snapshot.PricedAt, snapshot.Images)
	effective := effectiveModelPriceItems(usage, price, snapshot.PricedAt)
	total := decimal.Zero
	for i := range items {
		multiplier := snapshot.Applied.Value
		// An independent flat charge does not acquire a token group multiplier.
		for _, item := range effective {
			if item.ItemCode != items[i].ItemCode {
				continue
			}
			pricing := item.Pricing
			if items[i].PromptWriteCacheVariantCode != "" {
				pricing = item.FindPromptWriteCacheVariantPricing(items[i].PromptWriteCacheVariantCode)
			}
			if pricing.Mode == objects.PricingModeFlatFee || pricing.Mode == objects.PricingModeImageFixed {
				multiplier = snapshot.ManualMultiplier
			}
			break
		}
		items[i].Multiplier = lo.ToPtr(multiplier)
		items[i].Subtotal = items[i].Subtotal.Mul(multiplier)
		for j := range items[i].TierBreakdown {
			items[i].TierBreakdown[j].Subtotal = items[i].TierBreakdown[j].Subtotal.Mul(multiplier)
		}
		total = total.Add(items[i].Subtotal)
	}
	return items, total
}
