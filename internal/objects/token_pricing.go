package objects

import "fmt"

// IsModalityToken identifies input items whose quantities must be disjoint.
func (c PriceItemCode) IsModalityToken() bool {
	switch c {
	case PriceItemCodePromptText, PriceItemCodePromptImage, PriceItemCodePromptTextCached, PriceItemCodePromptImageCached:
		return true
	default:
		return false
	}
}

// ValidateTokenPriceItems keeps aggregate and modality rates from billing the
// same tokens twice. A zero-priced modality must be explicit, not omitted.
func ValidateTokenPriceItems(items []ModelPriceItem) error {
	seen := make(map[PriceItemCode]bool, len(items))
	for _, item := range items {
		if item.ItemCode.IsModalityToken() {
			if seen[item.ItemCode] {
				return fmt.Errorf("duplicate token price item: %s", item.ItemCode)
			}
			if item.Pricing.Mode == PricingModeFlatFee || item.Pricing.Mode == PricingModeImageFixed {
				return fmt.Errorf("%s requires token pricing", item.ItemCode)
			}
		}
		seen[item.ItemCode] = true
	}
	for _, group := range [][3]PriceItemCode{
		{PriceItemCodeUsage, PriceItemCodePromptText, PriceItemCodePromptImage},
		{PriceItemCodePromptCachedToken, PriceItemCodePromptTextCached, PriceItemCodePromptImageCached},
	} {
		if seen[group[0]] && (seen[group[1]] || seen[group[2]]) {
			return fmt.Errorf("%s cannot be combined with modality prices", group[0])
		}
		if seen[group[1]] != seen[group[2]] {
			return fmt.Errorf("%s and %s must be configured together", group[1], group[2])
		}
	}
	return nil
}
