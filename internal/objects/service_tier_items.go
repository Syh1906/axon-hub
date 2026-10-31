package objects

import (
	"fmt"
	"slices"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

type ServiceTierItemMethod string

const (
	ServiceTierItemMultiplier ServiceTierItemMethod = "multiplier"
	ServiceTierItemUnitPrice  ServiceTierItemMethod = "unit_price"
	ServiceTierItemUnpriced   ServiceTierItemMethod = "unpriced"
)

// ServiceTierItemRule overrides one price item. An omitted item inherits;
// an explicit unpriced item stops inheritance.
type ServiceTierItemRule struct {
	ReferenceBasePrice *decimal.Decimal            `json:"referenceBasePrice,omitempty"`
	ReferenceModePrice *decimal.Decimal            `json:"referenceModePrice,omitempty"`
	ItemCode           PriceItemCode               `json:"itemCode"`
	VariantCode        PromptWriteCacheVariantCode `json:"variantCode,omitempty"`
	Method             ServiceTierItemMethod       `json:"method"`
	Value              *decimal.Decimal            `json:"value,omitempty"`
}

func (r ServiceTierItemRule) Validate() error {
	if !slices.Contains([]PriceItemCode{PriceItemCodeUsage, PriceItemCodeCompletion, PriceItemCodePromptCachedToken, PriceItemCodeWriteCachedTokens}, r.ItemCode) {
		return fmt.Errorf("invalid service tier item code")
	}
	if r.VariantCode != "" && (r.ItemCode != PriceItemCodeWriteCachedTokens || !slices.Contains([]PromptWriteCacheVariantCode{PromptWriteCacheVariantCode5Min, PromptWriteCacheVariantCode1Hour}, r.VariantCode)) {
		return fmt.Errorf("invalid service tier cache variant")
	}
	switch r.Method {
	case ServiceTierItemUnpriced:
		if r.Value != nil {
			return fmt.Errorf("unpriced rule cannot have a value")
		}
	case ServiceTierItemMultiplier, ServiceTierItemUnitPrice:
		if r.Value == nil || r.Value.IsNegative() {
			return fmt.Errorf("service tier item value must be non-negative")
		}
	default:
		return fmt.Errorf("invalid service tier item method")
	}
	return nil
}

func (p *ServiceTierPrices) ForMode(mode string) *ServiceTierPrice {
	if p == nil {
		return nil
	}
	if mode == "fast" {
		return p.Fast
	}
	if mode == "ultrafast" {
		return p.Ultrafast
	}
	return nil
}

type ResolvedServiceTierItem struct {
	Method     ServiceTierItemMethod
	Value      *decimal.Decimal
	RuleSource string
	Price      *ServiceTierPrice
}

// ResolveServiceTierItem is shared by reservation and settlement. Context rules
// precede general rules, and a cache variant precedes its generic write rule.
func ResolveServiceTierItem(mode string, inputTokens int64, defaults, context *ServiceTierPrices, code PriceItemCode, variant PromptWriteCacheVariantCode) ResolvedServiceTierItem {
	if mode != "fast" && mode != "ultrafast" {
		return ResolvedServiceTierItem{Method: ServiceTierItemMultiplier, Value: lo.ToPtr(decimal.NewFromInt(1)), RuleSource: "standard"}
	}
	for i, prices := range []*ServiceTierPrices{context, defaults} {
		rate := prices.ForMode(mode)
		if rate == nil {
			continue
		}
		source := "mode"
		if i == 0 {
			source = "context"
		}
		result := ResolvedServiceTierItem{Method: ServiceTierItemUnpriced, RuleSource: source, Price: rate}
		if rate.Unpriced || (rate.ContextAbove != nil && inputTokens <= *rate.ContextAbove) || (rate.ContextUpTo != nil && inputTokens > *rate.ContextUpTo) {
			return result
		}
		idx := slices.IndexFunc(rate.Items, func(r ServiceTierItemRule) bool { return r.ItemCode == code && r.VariantCode == variant })
		if idx < 0 && variant != "" {
			idx = slices.IndexFunc(rate.Items, func(r ServiceTierItemRule) bool { return r.ItemCode == code && r.VariantCode == "" })
		}
		if idx >= 0 {
			result.Method, result.Value, result.RuleSource = rate.Items[idx].Method, rate.Items[idx].Value, source+"_item"
			return result
		}
		if rate.Multiplier != nil {
			result.Method, result.Value = ServiceTierItemMultiplier, rate.Multiplier
			return result
		}
	}
	return ResolvedServiceTierItem{Method: ServiceTierItemUnpriced, RuleSource: "unconfigured"}
}

// Apply returns a new pricing value, never mutating an immutable price snapshot.
func (r ResolvedServiceTierItem) Apply(base Pricing) (Pricing, bool) {
	if base.Mode == PricingModeFlatFee || base.Mode == PricingModeImageFixed {
		return base, true
	}
	if r.Method == ServiceTierItemUnpriced || r.Value == nil {
		return base, false
	}
	if r.Method == ServiceTierItemUnitPrice {
		return Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: r.Value}, true
	}
	copy := base
	if base.UsagePerUnit != nil {
		copy.UsagePerUnit = lo.ToPtr(base.UsagePerUnit.Mul(*r.Value))
	}
	if base.UsageTiered != nil {
		copy.UsageTiered = &TieredPricing{Tiers: slices.Clone(base.UsageTiered.Tiers)}
		for i := range copy.UsageTiered.Tiers {
			copy.UsageTiered.Tiers[i].PricePerUnit = copy.UsageTiered.Tiers[i].PricePerUnit.Mul(*r.Value)
		}
	}
	return copy, true
}
