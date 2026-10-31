package biz

import (
	"slices"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

// Select the base rates, context evidence and matching mode overrides together.
func selectedModelPricing(usage *llm.Usage, price objects.ModelPrice, now time.Time) ([]objects.ModelPriceItem, *objects.ServiceTierPrices, objects.ContextPricing) {
	result := objects.ContextPricing{Kind: "standard"}
	items := price.Items
	var modes *objects.ServiceTierPrices
	if len(price.VolumeTiers) > 0 {
		result.Threshold = lo.ToPtr(price.VolumeTiers[0].Above)
	}
	if usage != nil {
		result.InputTokens = usage.PromptTokens
	}
	for _, tier := range price.VolumeTiers {
		if usage == nil || usage.PromptTokens <= tier.Above {
			break
		}
		items, modes = tier.Items, tier.ServiceTiers
		result.Kind, result.Threshold, result.LongContext = "volume", lo.ToPtr(tier.Above), tier.LongContext
	}
	if price.Schedule != nil {
		if override := findMatchingOverride(now, price.Schedule); override != nil {
			items, modes = override.Items, override.ServiceTiers
			result.Kind, result.LongContext, result.Threshold, result.Name = "schedule", false, nil, override.Name
		}
	}
	return items, modes, result
}

func selectedContextPricing(usage *llm.Usage, price objects.ModelPrice, now time.Time) objects.ContextPricing {
	_, _, context := selectedModelPricing(usage, price, now)
	return context
}

func computeCostFromSnapshot(usage *llm.Usage, price objects.ModelPrice, snapshot *objects.RequestCostSnapshot) ([]objects.CostItem, decimal.Decimal) {
	effective, overrides, context := selectedModelPricing(usage, price, snapshot.PricedAt)
	items, _ := computeUsageCostWithItems(usage, effective, snapshot.Images)
	decision := snapshot.ServiceTier.Decision()
	total := decimal.Zero
	for i := range items {
		item := &items[i]
		var base objects.Pricing
		for _, entry := range effective {
			if entry.ItemCode == item.ItemCode {
				base = entry.Pricing
				if item.PromptWriteCacheVariantCode != "" {
					base = entry.FindPromptWriteCacheVariantPricing(item.PromptWriteCacheVariantCode)
				}
				break
			}
		}
		rule := objects.ResolveServiceTierItem(decision.Mode, context.InputTokens, price.ServiceTiers, overrides, item.ItemCode, item.PromptWriteCacheVariantCode)
		costMultiplier := snapshot.Applied.Value
		fixed := base.Mode == objects.PricingModeFlatFee || base.Mode == objects.PricingModeImageFixed
		if fixed {
			costMultiplier = snapshot.ManualMultiplier
			rule = objects.ResolvedServiceTierItem{Method: objects.ServiceTierItemMultiplier, Value: lo.ToPtr(decimal.NewFromInt(1)), RuleSource: "non_token"}
		}
		evidence := &objects.CostPricingEvidence{ServiceTier: decision, Context: context, BaseSubtotal: item.Subtotal, CostMultiplier: costMultiplier, ItemMethod: lo.ToPtr(rule.Method), RuleSource: rule.RuleSource, BaseUnitPrice: base.UsagePerUnit, Reference: rule.Price}
		if rule.Price != nil {
			reference := *rule.Price
			reference.Items = slices.Clone(rule.Price.Items)
			evidence.Reference = &reference
		}
		if rule.Method == objects.ServiceTierItemMultiplier {
			evidence.ModeMultiplier = rule.Value
		}

		for _, tier := range item.TierBreakdown {
			evidence.BaseTierSubtotals = append(evidence.BaseTierSubtotals, tier.Subtotal)
		}
		item.PricingEvidence, item.Multiplier = evidence, lo.ToPtr(costMultiplier)
		finalPricing, priced := rule.Apply(base)
		if !priced {
			// An unused optional cache item cannot invalidate a priced request.
			item.Unpriced = item.Unpriced || item.Quantity > 0
			item.Subtotal = decimal.Zero
			continue
		}
		evidence.FinalUnitPrice = finalPricing.UsagePerUnit
		if !fixed {
			calculated, _ := computeItemSubtotal(item.Quantity, finalPricing)
			item.Subtotal, item.TierBreakdown = calculated.Subtotal, calculated.TierBreakdown
		}
		evidence.ModeSubtotal = lo.ToPtr(item.Subtotal)
		item.Subtotal = item.Subtotal.Mul(costMultiplier)
		for j := range item.TierBreakdown {
			evidence.ModeTierSubtotals = append(evidence.ModeTierSubtotals, item.TierBreakdown[j].Subtotal)
			item.TierBreakdown[j].Subtotal = item.TierBreakdown[j].Subtotal.Mul(costMultiplier)
		}
		total = total.Add(item.Subtotal)
	}
	return items, total
}
