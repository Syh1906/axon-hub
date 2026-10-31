package biz

import (
	"context"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
)

func captureRequestCostSnapshot(ctx context.Context, db *ent.Client, ch *Channel, model string, modelCard *objects.ModelCard) *objects.RequestCostSnapshot {
	now := time.Now().UTC()
	manual := channelManualMultiplier(ch.Channel)
	snapshot := &objects.RequestCostSnapshot{PricedAt: now, ManualMultiplier: manual}
	if modelCard != nil {
		snapshot.ModelOutputLimit = int64(modelCard.Limit.Output)
	}
	if price, ok := ch.cachedModelPrices[model]; ok {
		priceCopy := price.Price
		snapshot.Price, snapshot.PriceReferenceID = &priceCopy, price.ReferenceID
		manual = price.Price.EffectiveMultiplier()
		snapshot.ManualMultiplier = manual
	} else if price, ok := modelCardToChannelModelPrice(modelCard); ok {
		// The model provides base prices; only the selected channel/key supplies
		// the multiplier. Keep the model price in the durable request snapshot.
		price.Multiplier = nil
		snapshot.Price, snapshot.ModelPrice = &price, &price
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
	price := snapshot.CurrentPrice()
	if price == nil {
		if snapshot.PriceReferenceID == "" {
			return nil, nil, ""
		}
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
