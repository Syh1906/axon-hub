package objects

// ChannelTodayUsage summarizes requests assigned to a channel in the system day.
type ChannelTodayUsage struct {
	ChannelID     GUID    `json:"channelId"`
	RequestCount  int     `json:"requestCount"`
	UnpricedCount int     `json:"unpricedCount"`
	TotalCost     float64 `json:"totalCost"`
	Timezone      string  `json:"timezone"`
	CurrencyCode  string  `json:"currencyCode"`
}

type ChannelTodayUsageDetails struct {
	ChannelID    GUID                      `json:"channelId"`
	Timezone     string                    `json:"timezone"`
	CurrencyCode string                    `json:"currencyCode"`
	Models       []*ChannelTodayModelUsage `json:"models"`
}

type ChannelTodayModelUsage struct {
	Model                   string   `json:"model"`
	RequestCount            int      `json:"requestCount"`
	UnpricedCount           int      `json:"unpricedCount"`
	TotalTokens             int64    `json:"totalTokens"`
	TotalCost               float64  `json:"totalCost"`
	CacheHitRate            *float64 `json:"cacheHitRate"`
	StandardCost            *float64 `json:"standardCost"`
	StandardUnpricedCount   int      `json:"standardUnpricedCount"`
	AvgCostPerMillionTokens *float64 `json:"avgCostPerMillionTokens"`
	TokenPricedRequestCount int      `json:"tokenPricedRequestCount"`
}
