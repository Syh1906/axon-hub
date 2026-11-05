package objects

import (
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestModelPriceModalityValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		codes []PriceItemCode
		valid bool
	}{
		{"legacy", []PriceItemCode{PriceItemCodeUsage, PriceItemCodePromptCachedToken, PriceItemCodeCompletion}, true},
		{"split input", []PriceItemCode{PriceItemCodePromptText, PriceItemCodePromptImage, PriceItemCodeCompletion}, true},
		{"split cache", []PriceItemCode{PriceItemCodePromptTextCached, PriceItemCodePromptImageCached}, true},
		{"overlapping input", []PriceItemCode{PriceItemCodeUsage, PriceItemCodePromptText, PriceItemCodePromptImage}, false},
		{"overlapping cache", []PriceItemCode{PriceItemCodePromptCachedToken, PriceItemCodePromptTextCached, PriceItemCodePromptImageCached}, false},
		{"missing image price", []PriceItemCode{PriceItemCodePromptText}, false},
		{"missing text cache price", []PriceItemCode{PriceItemCodePromptImageCached}, false},
		{"duplicate split price", []PriceItemCode{PriceItemCodePromptText, PriceItemCodePromptImage, PriceItemCodePromptImage}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]ModelPriceItem, 0, len(tc.codes))
			for _, code := range tc.codes {
				items = append(items, ModelPriceItem{ItemCode: code, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(5))}})
			}
			for _, price := range []ModelPrice{
				{Items: items},
				{Items: items, VolumeTiers: []ModelPriceVolumeTier{{Above: 1000, Items: items}}},
				{Schedule: &PriceSchedule{Timezone: "UTC", Overrides: []PriceOverride{{Name: "test", Items: items, When: OverrideWhen{Weekdays: []int{1}}}}}},
			} {
				err := price.Validate()
				if tc.valid {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}

func TestModelCardModalitySummaryPreservesUnknownAggregate(t *testing.T) {
	var card ModelCard
	require.NoError(t, json.Unmarshal([]byte(`{"price":{"items":[
		{"itemCode":"prompt_text_tokens","pricing":{"mode":"usage_per_unit","usagePerUnit":"5"}},
		{"itemCode":"prompt_image_tokens","pricing":{"mode":"usage_per_unit","usagePerUnit":"8"}},
		{"itemCode":"prompt_text_cached_tokens","pricing":{"mode":"usage_per_unit","usagePerUnit":"1.25"}},
		{"itemCode":"prompt_image_cached_tokens","pricing":{"mode":"usage_per_unit","usagePerUnit":"2"}}
	]}}`), &card))
	require.NoError(t, card.Price.Validate())
	require.Nil(t, card.Cost.Input)
	require.Nil(t, card.Cost.CacheRead)
	encoded, err := json.Marshal(card)
	require.NoError(t, err)
	var restored ModelCard
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Equal(t, card, restored, "JSON cache and database envelopes retain the full authoritative price")
	for _, item := range card.Price.Items {
		rule := ServiceTierItemRule{ItemCode: item.ItemCode, Method: ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(10))}
		require.NoError(t, rule.Validate())
	}
}
