package biz

import (
	"context"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/shopspring/decimal"
)

// ModeSubtotal is the historical amount after mode/context pricing but before
// the channel/key multiplier. It remains usable even when the multiplier is zero.
func standardCostFromItems(items []objects.CostItem) (decimal.Decimal, bool) {
	total := decimal.Zero
	if len(items) == 0 {
		return total, false
	}
	for _, item := range items {
		if item.Unpriced {
			return decimal.Zero, false
		}
		if evidence := item.PricingEvidence; evidence != nil {
			if evidence.ModeSubtotal != nil {
				total = total.Add(*evidence.ModeSubtotal)
				continue
			}
			if evidence.ModeMultiplier != nil {
				total = total.Add(evidence.BaseSubtotal.Mul(*evidence.ModeMultiplier))
				continue
			}
			if evidence.CostMultiplier.IsPositive() {
				total = total.Add(item.Subtotal.Div(evidence.CostMultiplier))
				continue
			}
		}
		if item.Multiplier != nil && item.Multiplier.IsPositive() {
			total = total.Add(item.Subtotal.Div(*item.Multiplier))
			continue
		}
		return decimal.Zero, false
	}
	return total, true
}

// Callers have already authorized and bounded these usage records. Only legacy
// records without item evidence need their immutable historical price versions.
func loadStandardCostVersionsInto(ctx context.Context, client *ent.Client, logs []*ent.UsageLog, result map[string]objects.ModelPrice, loaded map[string]bool) error {
	missing := make(map[int]*ent.UsageLog)
	for _, row := range logs {
		if _, ok := standardCostFromItems(row.CostItems); !ok && row.TotalCost != nil {
			missing[row.ID] = row
		}
	}
	if len(missing) > 0 {
		ids := make([]int, 0, len(missing))
		for id := range missing {
			ids = append(ids, id)
		}
		snapshots, err := client.UsageLog.Query().Where(usagelog.IDIn(ids...)).Select(usagelog.FieldID, usagelog.FieldCostSnapshot).All(ctx)
		if err != nil {
			return err
		}
		for _, row := range snapshots {
			missing[row.ID].CostSnapshot = row.CostSnapshot
		}
	}
	refs := map[string]struct{}{}
	for _, row := range missing {
		if row.CostSnapshot.CurrentPrice() != nil {
			continue
		}
		if row.CostPriceReferenceID != "" && !loaded[row.CostPriceReferenceID] {
			refs[row.CostPriceReferenceID] = struct{}{}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	versions, err := client.ChannelModelPriceVersion.Query().Where(channelmodelpriceversion.ReferenceIDIn(ids...)).
		Select(channelmodelpriceversion.FieldReferenceID, channelmodelpriceversion.FieldPrice).All(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		loaded[id] = true
	}
	for _, version := range versions {
		result[version.ReferenceID] = version.Price
	}
	return nil
}

func usageStandardCost(row *ent.UsageLog, versions map[string]objects.ModelPrice) (decimal.Decimal, bool) {
	if row.TotalCost == nil {
		return decimal.Zero, false
	}
	if total, ok := standardCostFromItems(row.CostItems); ok {
		return total, true
	}
	var price objects.ModelPrice
	if saved := row.CostSnapshot.CurrentPrice(); saved != nil {
		price = *saved
	} else {
		var ok bool
		price, ok = versions[row.CostPriceReferenceID]
		if !ok {
			return decimal.Zero, false
		}
	}
	usage := &llm.Usage{PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, TotalTokens: row.TotalTokens,
		PromptTokensDetails: promptTokenDetailsFromLog(row)}
	var items []objects.CostItem
	var total decimal.Decimal
	if row.CostSnapshot != nil {
		snapshot := *row.CostSnapshot
		snapshot.Applied.Value, snapshot.ManualMultiplier = decimal.NewFromInt(1), decimal.NewFromInt(1)
		items, total = computeCostFromSnapshot(usage, price, &snapshot)
	} else {
		price.Multiplier = nil
		var images []objects.ImageOutput
		for _, item := range row.CostItems {
			if item.Image != nil {
				images = append(images, *item.Image)
			}
		}
		items, total = ComputeUsageCost(usage, price, row.CreatedAt, images)
	}
	for _, item := range items {
		if item.Unpriced {
			return decimal.Zero, false
		}
	}
	return total, true
}
