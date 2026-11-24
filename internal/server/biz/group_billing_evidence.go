package biz

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
)

func channelItemSource(snap *objects.RequestCostSnapshot, item objects.CostItem) string {
	if item.PricingEvidence != nil && item.PricingEvidence.RuleSource == "non_token" {
		return string(objects.CostMultiplierManual)
	}
	if snap == nil {
		return ""
	}
	return string(snap.Applied.Source)
}

func usedEstimate(snap *objects.RequestCostSnapshot, items []objects.CostItem) bool {
	for _, item := range items {
		if item.Quantity != 0 && !item.Unpriced && item.Multiplier != nil && channelItemSource(snap, item) == string(objects.CostMultiplierEstimated) {
			return true
		}
	}
	return false
}

func UsageBillingEvidence(row *ent.UsageLog) *objects.GroupBillingEvidence {
	snap, items, billed := row.CostSnapshot, row.CostItems, row.BilledItems
	if snap == nil && len(items) == 0 {
		return nil
	}
	e := &objects.GroupBillingEvidence{Source: "channel"}
	override := snap != nil && snap.Billing != nil && snap.Billing.Rate != nil
	if snap != nil {
		if snap.Billing != nil {
			e.GroupName = snap.Billing.GroupName
		}
		if override {
			e.Multiplier, e.Source = snap.Billing.Rate, snap.Billing.Source
		} else {
			e.Multiplier = snap.DisplayMultiplier(items)
			e.Mixed = e.Multiplier == nil
		}
	}
	for idx, item := range items {
		if item.Quantity == 0 && item.Subtotal.IsZero() && !item.Unpriced {
			continue
		}
		code, variant := string(item.ItemCode), string(item.PromptWriteCacheVariantCode)
		if item.Image != nil {
			code, variant = "image", item.ImageRule
			if variant == "" {
				variant = item.Image.Tier
			}
		} else if item.PricingEvidence != nil && item.PricingEvidence.RuleSource == "non_token" {
			code = "flat_fee"
		}
		line := &objects.GroupBillingItem{Code: code, Variant: variant, ChannelRate: item.Multiplier, ChannelSource: channelItemSource(snap, item)}
		if !item.Unpriced {
			line.ChannelAmount = lo.ToPtr(item.Subtotal)
		}
		if override {
			line.BilledRate, line.BilledSource = e.Multiplier, e.Source
			if idx < len(billed) && !billed[idx].Unpriced {
				line.BilledAmount = lo.ToPtr(billed[idx].Subtotal)
			}
		} else {
			line.BilledRate, line.BilledSource, line.BilledAmount = line.ChannelRate, "channel", line.ChannelAmount
		}
		e.Items = append(e.Items, line)
	}
	return e
}
