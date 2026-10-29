package biz

import "github.com/looplj/axonhub/internal/objects"

func hasImagePricingSnapshot(snapshot *objects.RequestCostSnapshot) bool {
	if snapshot == nil || snapshot.Price == nil {
		return false
	}
	for _, item := range effectiveModelPriceItems(nil, *snapshot.Price, snapshot.PricedAt) {
		if item.Pricing.Mode == objects.PricingModeImageFixed {
			return true
		}
	}
	return false
}

// NeedsImageCostEvidence gates image inspection to requests with an image price.
func NeedsImageCostEvidence(snapshot *objects.RequestCostSnapshot) bool {
	return hasImagePricingSnapshot(snapshot)
}
