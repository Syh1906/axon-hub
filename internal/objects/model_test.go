package objects

import (
	"encoding/json"
	"testing"

	"github.com/samber/lo"

	"github.com/stretchr/testify/require"
)

func TestModelCardJSONLegacyPriceCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		configured bool
		rates      map[PriceItemCode]string
	}{
		{name: "absent cost remains unpriced", data: `{}`},
		{name: "null cost remains unpriced", data: `{"cost":null}`},
		{name: "empty legacy cost remains unpriced", data: `{"cost":{}}`},
		{
			name:       "explicit zero survives alongside paid cache rates",
			data:       `{"cost":{"input":0,"output":2,"cacheRead":0,"cacheWrite":0.25}}`,
			configured: true,
			rates:      map[PriceItemCode]string{PriceItemCodeUsage: "0", PriceItemCodeCompletion: "2", PriceItemCodePromptCachedToken: "0", PriceItemCodeWriteCachedTokens: "0.25"},
		},
		{
			name: "omitted legacy components are not manufactured",
			data: `{"cost":{"input":0}}`, configured: true,
			rates: map[PriceItemCode]string{PriceItemCodeUsage: "0"},
		},
		{name: "explicit null price overrides stale cost", data: `{"price":null,"cost":{"input":99}}`},
		{name: "empty price overrides stale cost", data: `{"price":{"items":[]},"cost":{"input":99}}`, configured: true},
		{
			name:       "authoritative price overrides stale cost",
			data:       `{"price":{"items":[{"itemCode":"prompt_tokens","pricing":{"mode":"usage_per_unit","usagePerUnit":"3"}}]},"cost":{"input":99}}`,
			configured: true,
			rates:      map[PriceItemCode]string{PriceItemCodeUsage: "3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Match the nested envelope used by database entities and JSON caches.
			var cached struct {
				ModelCard *ModelCard `json:"modelCard"`
			}
			require.NoError(t, json.Unmarshal([]byte(`{"modelCard":`+tt.data+`}`), &cached))
			require.NotNil(t, cached.ModelCard)
			card := cached.ModelCard
			if !tt.configured {
				require.Nil(t, card.Price)
			} else {
				require.NotNil(t, card.Price)
				require.Len(t, card.Price.Items, len(tt.rates))
				for _, item := range card.Price.Items {
					require.Equal(t, tt.rates[item.ItemCode], item.Pricing.UsagePerUnit.String())
				}
			}
			require.Equal(t, card.PriceSummary(), card.Cost)
			encoded, err := json.Marshal(cached)
			require.NoError(t, err)
			var restored struct {
				ModelCard *ModelCard `json:"modelCard"`
			}
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.Equal(t, card, restored.ModelCard)
		})
	}
}

func TestModelCardMarshalIgnoresLegacyCostWrites(t *testing.T) {
	card := ModelCard{Cost: ModelCardCost{Input: lo.ToPtr(99.0)}}
	encoded, err := json.Marshal(card)
	require.NoError(t, err)
	var restored ModelCard
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Nil(t, restored.Price)
	require.Zero(t, *restored.Cost.Input)
}
