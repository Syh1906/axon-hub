package biz

import (
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/shopspring/decimal"
)

// Decode only the historical evidence needed for 1x pricing. Detailed tier
// breakdowns, service-tier decisions and reference prices can be very large;
// they are never displayed or used by this aggregation.
type channelUsageCostItems []objects.CostItem

func (items *channelUsageCostItems) Scan(value any) error {
	switch data := value.(type) {
	case nil:
		*items = nil
		return nil
	case []byte:
		return items.UnmarshalJSON(data)
	case string:
		return items.UnmarshalJSON([]byte(data))
	default:
		return fmt.Errorf("unexpected cost items database type %T", value)
	}
}

func (items *channelUsageCostItems) UnmarshalJSON(data []byte) error {
	var values []struct {
		Subtotal        decimal.Decimal      `json:"subtotal"`
		Multiplier      *decimal.Decimal     `json:"multiplier"`
		Unpriced        bool                 `json:"unpriced"`
		Image           *objects.ImageOutput `json:"image"`
		PricingEvidence *struct {
			ModeSubtotal   *decimal.Decimal `json:"modeSubtotal"`
			BaseSubtotal   decimal.Decimal  `json:"baseSubtotal"`
			ModeMultiplier *decimal.Decimal `json:"modeMultiplier"`
			CostMultiplier decimal.Decimal  `json:"costMultiplier"`
		} `json:"pricingEvidence"`
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*items = make(channelUsageCostItems, len(values))
	for i, value := range values {
		item := objects.CostItem{Subtotal: value.Subtotal, Multiplier: value.Multiplier, Unpriced: value.Unpriced, Image: value.Image}
		if evidence := value.PricingEvidence; evidence != nil {
			item.PricingEvidence = &objects.CostPricingEvidence{ModeSubtotal: evidence.ModeSubtotal, BaseSubtotal: evidence.BaseSubtotal,
				ModeMultiplier: evidence.ModeMultiplier, CostMultiplier: evidence.CostMultiplier}
		}
		(*items)[i] = item
	}
	return nil
}

type channelUsageRow struct {
	ent.UsageLog
	CostItems channelUsageCostItems `json:"cost_items"`
}

var channelUsageFields = []string{usagelog.FieldID, usagelog.FieldRequestID, usagelog.FieldProjectID, usagelog.FieldChannelID, usagelog.FieldModelID, usagelog.FieldCreatedAt,
	usagelog.FieldTotalCost, usagelog.FieldCostItems, usagelog.FieldCostPriceReferenceID, usagelog.FieldPromptTokens,
	usagelog.FieldCompletionTokens, usagelog.FieldTotalTokens, usagelog.FieldPromptCachedTokens, usagelog.FieldPromptWriteCachedTokens,
	usagelog.FieldPromptWriteCachedTokens5m, usagelog.FieldPromptWriteCachedTokens1h,
	usagelog.FieldPromptTextTokens, usagelog.FieldPromptImageTokens, usagelog.FieldPromptTextCachedTokens, usagelog.FieldPromptImageCachedTokens}
