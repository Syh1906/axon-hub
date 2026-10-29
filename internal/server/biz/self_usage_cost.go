package biz

import (
	"context"
	"fmt"
	"sort"

	"entgo.io/ent/dialect/sql"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
)

type SelfUsageTierCost struct {
	UpTo     *int
	Units    int
	Subtotal float64
}

type SelfUsageCostItem struct {
	Image                       *objects.ImageOutput
	ImageRule                   string
	Unpriced                    bool
	ItemCode                    string
	PromptWriteCacheVariantCode *string
	Quantity                    int
	Subtotal                    float64
	TierBreakdown               []SelfUsageTierCost
}

type selfUsageRequestCosts struct {
	items           []SelfUsageCostItem
	multiplier      *float64
	multiplierMixed bool
}

type selfUsageCostKey struct {
	code    objects.PriceItemCode
	variant objects.PromptWriteCacheVariantCode
}

type selfUsageTierKey struct {
	upTo    int64
	hasUpTo bool
}

type selfUsageMergedTier struct {
	units    int64
	subtotal decimal.Decimal
}

type selfUsageMergedItem struct {
	quantity int64
	subtotal decimal.Decimal
	tiers    map[selfUsageTierKey]selfUsageMergedTier
}

// loadRequestCosts restricts usage to this page AND to requests belonging to
// the authenticated key. Usage-log ownership columns are not authoritative.
func (s *SelfUsageService) loadRequestCosts(ctx context.Context, scope selfUsageScope, ids []int) (map[int]selfUsageRequestCosts, error) {
	logs, err := s.client.UsageLog.Query().Where(usagelog.RequestIDIn(ids...)).Select(
		usagelog.FieldRequestID, usagelog.FieldCostItems, usagelog.FieldCostPriceReferenceID, usagelog.FieldTotalCost, usagelog.FieldCostSnapshot,
	).Modify(func(q *sql.Selector) { scope.join(q) }).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query self usage page cost items: %w", err)
	}
	references := make(map[string]struct{})
	for _, log := range logs {
		if log.TotalCost != nil && log.CostPriceReferenceID != "" {
			references[log.CostPriceReferenceID] = struct{}{}
		}
	}
	idsToLoad := make([]string, 0, len(references))
	for id := range references {
		idsToLoad = append(idsToLoad, id)
	}
	multipliers, err := LoadUsagePriceMultipliers(ctx, s.client, idsToLoad)
	if err != nil {
		return nil, err
	}
	type aggregate struct {
		images     []SelfUsageCostItem
		items      map[selfUsageCostKey]*selfUsageMergedItem
		multiplier *decimal.Decimal
		mixed      bool
	}
	byRequest := make(map[int]*aggregate, len(ids))
	for _, log := range logs {
		row := byRequest[log.RequestID]
		if row == nil {
			row = &aggregate{items: make(map[selfUsageCostKey]*selfUsageMergedItem)}
			byRequest[log.RequestID] = row
		}
		for _, item := range log.CostItems {
			if item.Image != nil || item.Unpriced {
				row.images = append(row.images, SelfUsageCostItem{ItemCode: string(item.ItemCode), Image: item.Image, ImageRule: item.ImageRule, Unpriced: item.Unpriced, Quantity: int(item.Quantity), Subtotal: item.Subtotal.InexactFloat64(), TierBreakdown: []SelfUsageTierCost{}})
				continue
			}
			key := selfUsageCostKey{code: item.ItemCode, variant: item.PromptWriteCacheVariantCode}
			merged := row.items[key]
			if merged == nil {
				merged = &selfUsageMergedItem{tiers: make(map[selfUsageTierKey]selfUsageMergedTier)}
				row.items[key] = merged
			}
			merged.quantity += item.Quantity
			merged.subtotal = merged.subtotal.Add(item.Subtotal)
			for _, tier := range item.TierBreakdown {
				tierKey := selfUsageTierKey{}
				if tier.UpTo != nil {
					tierKey.upTo, tierKey.hasUpTo = *tier.UpTo, true
				}
				current := merged.tiers[tierKey]
				current.units += tier.Units
				current.subtotal = current.subtotal.Add(tier.Subtotal)
				merged.tiers[tierKey] = current
			}
		}
		if log.TotalCost == nil {
			continue
		}
		// A missing reference or version means the record was billed without a multiplier.
		multiplier, ok := multipliers[log.CostPriceReferenceID]
		if !ok {
			multiplier = decimal.NewFromInt(1)
		}
		if log.CostSnapshot != nil {
			applied := log.CostSnapshot.DisplayMultiplier(log.CostItems)
			if applied == nil {
				row.mixed = true
				continue
			}
			multiplier = *applied
		}
		if row.multiplier == nil {
			row.multiplier = &multiplier
		} else if !row.multiplier.Equal(multiplier) {
			row.mixed = true
		}
	}
	result := make(map[int]selfUsageRequestCosts, len(byRequest))
	for requestID, row := range byRequest {
		out := selfUsageRequestCosts{items: make([]SelfUsageCostItem, 0, len(row.items)), multiplierMixed: row.mixed}
		out.items = append(out.items, row.images...)
		if row.multiplier != nil && !row.mixed {
			value, _ := row.multiplier.Float64()
			out.multiplier = &value
		}
		for key, merged := range row.items {
			item := SelfUsageCostItem{ItemCode: string(key.code), Quantity: int(merged.quantity), TierBreakdown: make([]SelfUsageTierCost, 0, len(merged.tiers))}
			item.Subtotal, _ = merged.subtotal.Float64()
			if key.variant != "" {
				variant := string(key.variant)
				item.PromptWriteCacheVariantCode = &variant
			}
			for tierKey, tier := range merged.tiers {
				entry := SelfUsageTierCost{Units: int(tier.units)}
				entry.Subtotal, _ = tier.subtotal.Float64()
				if tierKey.hasUpTo {
					limit := int(tierKey.upTo)
					entry.UpTo = &limit
				}
				item.TierBreakdown = append(item.TierBreakdown, entry)
			}
			sort.Slice(item.TierBreakdown, func(i, j int) bool {
				a, b := item.TierBreakdown[i].UpTo, item.TierBreakdown[j].UpTo
				return a != nil && (b == nil || *a < *b)
			})
			out.items = append(out.items, item)
		}
		sort.SliceStable(out.items, func(i, j int) bool {
			a, b := out.items[i], out.items[j]
			ar, br := selfUsageCostOrder(a.ItemCode), selfUsageCostOrder(b.ItemCode)
			if ar != br {
				return ar < br
			}
			if a.ItemCode != b.ItemCode {
				return a.ItemCode < b.ItemCode
			}
			av, bv := selfUsageVariantOrder(a.PromptWriteCacheVariantCode), selfUsageVariantOrder(b.PromptWriteCacheVariantCode)
			if av != bv {
				return av < bv
			}
			if a.PromptWriteCacheVariantCode == nil {
				return false
			}
			return *a.PromptWriteCacheVariantCode < *b.PromptWriteCacheVariantCode
		})
		result[requestID] = out
	}
	return result, nil
}

func selfUsageCostOrder(code string) int {
	switch objects.PriceItemCode(code) {
	case objects.PriceItemCodeUsage:
		return 0
	case objects.PriceItemCodeCompletion:
		return 1
	case objects.PriceItemCodePromptCachedToken:
		return 2
	case objects.PriceItemCodeWriteCachedTokens:
		return 3
	default:
		return 4
	}
}

func selfUsageVariantOrder(variant *string) int {
	if variant == nil {
		return 2
	}
	switch *variant {
	case string(objects.PromptWriteCacheVariantCode5Min):
		return 0
	case string(objects.PromptWriteCacheVariantCode1Hour):
		return 1
	default:
		return 3
	}
}
