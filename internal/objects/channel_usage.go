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
