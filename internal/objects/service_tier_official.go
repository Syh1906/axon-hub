package objects

import (
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

// This catalog is an explicit, versioned snapshot of the API price table.
// https://developers.openai.com/api/docs/pricing, verified 2026-10-31.
// OpenAI's 30m/12h cache writes are not Anthropic's 5m/1h variants.
type OfficialServiceTierItem struct {
	ItemCode    PriceItemCode   `json:"itemCode"`
	VariantCode string          `json:"variantCode"`
	UnitPrice   decimal.Decimal `json:"unitPrice"`
}
type OfficialServiceTierContext struct {
	Above     *int64                    `json:"above,omitempty"`
	UpTo      *int64                    `json:"upTo,omitempty"`
	Standard  []OfficialServiceTierItem `json:"standard"`
	Fast      []OfficialServiceTierItem `json:"fast"`
	Ultrafast []OfficialServiceTierItem `json:"ultrafast"`
}
type OfficialServiceTierPrice struct {
	Model      string                       `json:"model"`
	Version    string                       `json:"version"`
	SourceURL  string                       `json:"sourceURL"`
	VerifiedAt string                       `json:"verifiedAt"`
	Currency   string                       `json:"currency"`
	Unit       string                       `json:"unit"`
	Fast       *decimal.Decimal             `json:"fast,omitempty"`
	Ultrafast  *decimal.Decimal             `json:"ultrafast,omitempty"`
	Contexts   []OfficialServiceTierContext `json:"contexts"`
}

func officialItems(values []string) []OfficialServiceTierItem {
	codes := []PriceItemCode{PriceItemCodeUsage, PriceItemCodeCompletion, PriceItemCodePromptCachedToken, PriceItemCodeWriteCachedTokens, PriceItemCodeWriteCachedTokens}
	variants := []string{"", "", "", "thirty_min", "twelve_hour"}
	items := make([]OfficialServiceTierItem, 0, len(values))
	for i, value := range values {
		items = append(items, OfficialServiceTierItem{ItemCode: codes[i], VariantCode: variants[i], UnitPrice: decimal.RequireFromString(value)})
	}
	return items
}

func OfficialServiceTierPrices() []OfficialServiceTierPrice {
	var rows []OfficialServiceTierPrice
	for _, data := range []struct {
		model, fast, ultra                                      string
		short, long, fastShort, fastLong, ultraShort, ultraLong []string
	}{
		{"gpt-6-astra", "2", "6", []string{"10", "50", "1", "12.5", "25"}, []string{"20", "75", "2", "25", "50"}, []string{"20", "100", "2", "25", "50"}, []string{"40", "150", "4", "50", "100"}, []string{"60", "300", "6", "75", "150"}, []string{"120", "450", "12", "150", "300"}},
		{"gpt-6.1-sol", "2", "6", []string{"2", "10", "0.1", "2.5", "5"}, []string{"4", "15", "0.2", "5", "10"}, []string{"4", "20", "0.2", "5", "10"}, []string{"8", "30", "0.4", "10", "20"}, []string{"12", "60", "0.6", "15", "30"}, []string{"24", "90", "1.2", "30", "60"}},
		{"gpt-5.5", "2.5", "", []string{"5", "30", "0.5"}, []string{"10", "45", "1"}, []string{"12.5", "75", "1.25"}, nil, nil, nil},
		{"gpt-4.1", "1.75", "", []string{"2", "8", "0.5"}, nil, []string{"3.5", "14", "0.875"}, nil, nil, nil},
		{"gpt-4o", "1.7", "", []string{"2.5", "10", "1.25"}, nil, []string{"4.25", "17", "2.125"}, nil, nil, nil},
	} {
		row := OfficialServiceTierPrice{Model: data.model, Version: "openai-items-2026-10-31", SourceURL: "https://developers.openai.com/api/docs/pricing", VerifiedAt: "2026-10-31", Currency: "USD", Unit: "million_tokens", Fast: lo.ToPtr(decimal.RequireFromString(data.fast))}
		if data.ultra != "" {
			row.Ultrafast = lo.ToPtr(decimal.RequireFromString(data.ultra))
		}
		short := OfficialServiceTierContext{Standard: officialItems(data.short), Fast: officialItems(data.fastShort), Ultrafast: officialItems(data.ultraShort)}
		if len(data.long) > 0 {
			short.UpTo = lo.ToPtr(int64(272000))
		}
		row.Contexts = append(row.Contexts, short)
		if len(data.long) > 0 {
			row.Contexts = append(row.Contexts, OfficialServiceTierContext{Above: lo.ToPtr(int64(272000)), Standard: officialItems(data.long), Fast: officialItems(data.fastLong), Ultrafast: officialItems(data.ultraLong)})
		}
		rows = append(rows, row)
	}
	return rows
}

func (o OfficialServiceTierPrice) Price(mode, application string, scope OfficialServiceTierContext) *ServiceTierPrice {
	quote := scope.Fast
	if mode == "ultrafast" {
		quote = scope.Ultrafast
	}
	price := &ServiceTierPrice{Source: ServiceTierPriceOfficial, ReferenceModel: o.Model, Version: o.Version, Application: application, ContextAbove: scope.Above, ContextUpTo: scope.UpTo, SourceURL: o.SourceURL, VerifiedAt: o.VerifiedAt, Currency: o.Currency, Unit: o.Unit}
	if len(quote) == 0 {
		price.Unpriced = true
		return price
	}
	for _, key := range []ServiceTierItemRule{
		{ItemCode: PriceItemCodeUsage}, {ItemCode: PriceItemCodeCompletion}, {ItemCode: PriceItemCodePromptCachedToken}, {ItemCode: PriceItemCodeWriteCachedTokens},
		{ItemCode: PriceItemCodeWriteCachedTokens, VariantCode: PromptWriteCacheVariantCode5Min}, {ItemCode: PriceItemCodeWriteCachedTokens, VariantCode: PromptWriteCacheVariantCode1Hour},
	} {
		key.Method = ServiceTierItemUnpriced
		for _, q := range quote {
			if q.ItemCode != key.ItemCode || q.VariantCode != string(key.VariantCode) {
				continue
			}
			key.Method, key.Value = ServiceTierItemUnitPrice, lo.ToPtr(q.UnitPrice)
			key.ReferenceModePrice = lo.ToPtr(q.UnitPrice)
			for _, base := range scope.Standard {
				if base.ItemCode == q.ItemCode && base.VariantCode == q.VariantCode {
					key.ReferenceBasePrice = lo.ToPtr(base.UnitPrice)
					break
				}
			}
			if application == "ratio" {
				for _, base := range scope.Standard {
					if base.ItemCode == q.ItemCode && base.VariantCode == q.VariantCode && !base.UnitPrice.IsZero() {
						key.Method, key.Value = ServiceTierItemMultiplier, lo.ToPtr(q.UnitPrice.Div(base.UnitPrice))
						break
					}
				}
			}
		}
		price.Items = append(price.Items, key)
	}
	return price
}

func matchesOfficialServiceTierPrice(mode string, price *ServiceTierPrice) bool {
	for _, official := range OfficialServiceTierPrices() {
		if official.Model != price.ReferenceModel || official.Version != price.Version {
			continue
		}
		if price.Application != "ratio" && price.Application != "unit_price" {
			return false
		}
		for _, scope := range official.Contexts {
			if price.equals(official.Price(mode, price.Application, scope)) {
				return true
			}
		}
	}
	return false
}
