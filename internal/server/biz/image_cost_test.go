package biz

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func imageTestPrice(automatic bool) objects.ModelPrice {
	return objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeImageFixed, Image: &objects.ImagePricing{Automatic: automatic, Rules: []objects.ImagePriceRule{
		{Name: "2K", Fee: decimal.RequireFromString("0.04"), Match: "any", Conditions: []objects.ImageCondition{{Dimension: "width", Operator: "gt", Value: 1024}, {Dimension: "height", Operator: "gt", Value: 1024}}},
		{Name: "1K", Fee: decimal.RequireFromString("0.02"), Match: "all", Conditions: []objects.ImageCondition{{Dimension: "width", Operator: "lte", Value: 1024}, {Dimension: "height", Operator: "lte", Value: 1024}}},
	}}}}}}
}

func TestImagePricingPerOutputWithoutTokenUsage(t *testing.T) {
	price := imageTestPrice(true)
	require.NoError(t, price.Validate())
	images := []objects.ImageOutput{{Width: 1024, Height: 1024, Tier: "1K"}, {Width: 2048, Height: 2048, Tier: "2K"}, {Width: 2048, Height: 2048, Tier: "2K"}}
	items, total := ComputeUsageCost(nil, price, time.Now(), images)
	require.Equal(t, "0.1", total.String())
	require.Len(t, items, 3)
	require.EqualValues(t, 1, items[0].Quantity)
	price.Items[0].Pricing.Image.Rules[1].Fee = decimal.Zero
	freeItems, freeTotal := ComputeUsageCost(nil, price, time.Now(), images[:1])
	require.True(t, freeTotal.IsZero())
	require.False(t, freeItems[0].Unpriced)
	price.Items[0].Pricing.Image.Rules[1].Fee = decimal.RequireFromString("0.02")
	price.Items[0].Pricing.Image.Automatic = false
	items, total = ComputeUsageCost(nil, price, time.Now(), images)
	require.Equal(t, "0.1", total.String())
	require.False(t, items[0].Unpriced)
	// The first match wins even if a later rule also matches.
	price.Items[0].Pricing.Image.Rules = append(price.Items[0].Pricing.Image.Rules, price.Items[0].Pricing.Image.Rules[0])
	_, total = ComputeUsageCost(nil, price, time.Now(), images)
	require.Equal(t, "0.1", total.String())
}

func TestImagePricingInactiveDraftDoesNotBillOrBlockAutomatic(t *testing.T) {
	price := imageTestPrice(true)
	config := price.Items[0].Pricing.Image
	config.SavedRules = []objects.ImagePriceRuleDraft{{Name: "自定义宽图", Fee: "", Match: "any", Conditions: []objects.ImageConditionDraft{{Dimension: "width", Operator: "gt", Value: 1.5}}}}
	config.Rules[0].Conditions[0].Value = 0 // Inactive conditions in automatic mode.
	require.NoError(t, price.Validate())
	encoded, err := json.Marshal(price)
	require.NoError(t, err)
	var restored objects.ModelPrice
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.True(t, price.Equals(restored))
	_, total := ComputeUsageCost(nil, restored, time.Now(), []objects.ImageOutput{{Tier: "2K"}})
	require.Equal(t, "0.04", total.String())
	config.Rules[0].Name = " 2K "
	require.Error(t, price.Validate()) // Accepted tier names must also match exactly.
}

func TestImagePricingEmptyDraftRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rules     []objects.ImagePriceRuleDraft
		jsonValue string
	}{
		{name: "uninitialized", jsonValue: `"savedRules":null`},
		{name: "intentionally empty", rules: []objects.ImagePriceRuleDraft{}, jsonValue: `"savedRules":[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price := imageTestPrice(true)
			price.Items[0].Pricing.Image.SavedRules = tc.rules
			require.NoError(t, price.Validate())
			encoded, err := json.Marshal(price)
			require.NoError(t, err)
			require.Contains(t, string(encoded), tc.jsonValue)
			var restored objects.ModelPrice
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.Equal(t, tc.rules, restored.Items[0].Pricing.Image.SavedRules)
			require.True(t, price.Equals(restored))
			_, total := ComputeUsageCost(nil, restored, time.Now(), []objects.ImageOutput{{Tier: "2K"}})
			require.Equal(t, "0.04", total.String())
		})
	}
}

func TestImageRulesConditionsAndUnknown(t *testing.T) {
	price := imageTestPrice(false)
	images := []objects.ImageOutput{{Width: 1024, Height: 2048}, {Width: 1024, Height: 1024}, {Tier: "2K"}}
	items, _ := ComputeUsageCost(nil, price, time.Now(), images)
	require.Equal(t, "2K", items[0].ImageRule)
	require.Equal(t, "1K", items[1].ImageRule)
	require.True(t, items[2].Unpriced)
	price.Items[0].Pricing.Image.Rules[0].Match = "all"
	items, _ = ComputeUsageCost(nil, price, time.Now(), images[:1])
	require.True(t, items[0].Unpriced)
	items, _ = ComputeUsageCost(nil, price, time.Now())
	require.True(t, items[0].Unpriced)
}

func TestImageSnapshotMultiplierScheduleAndRoundTrip(t *testing.T) {
	price := imageTestPrice(true)
	snapshot := &objects.RequestCostSnapshot{Price: &price, PriceReferenceID: "image-test", PricedAt: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), ManualMultiplier: decimal.RequireFromString("0.5"), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(8)}, Images: []objects.ImageOutput{{Tier: "2K", Source: "request_tier"}}}
	items, total := computeCostFromSnapshot(nil, price, snapshot)
	require.Equal(t, "0.02", total.String())
	require.Equal(t, "0.5", items[0].Multiplier.String())
	price.Schedule = &objects.PriceSchedule{Timezone: "UTC", Overrides: []objects.PriceOverride{{When: objects.OverrideWhen{Weekdays: []int{4}}, Items: []objects.ModelPriceItem{imageTestPrice(true).Items[0]}}}}
	price.Schedule.Overrides[0].Items[0].Pricing.Image.Rules[0].Fee = decimal.RequireFromString("0.08")
	_, total = computeCostFromSnapshot(nil, price, snapshot)
	require.Equal(t, "0.04", total.String())
	data, err := json.Marshal(price)
	require.NoError(t, err)
	var restored objects.ModelPrice
	require.NoError(t, json.Unmarshal(data, &restored))
	require.True(t, price.Equals(restored))
	restored.Items[0].Pricing.Image.Rules[0].Fee = decimal.NewFromInt(1)
	require.False(t, price.Equals(restored))
}
