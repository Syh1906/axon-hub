package objects

// BudgetAsyncTask keeps provider task and price independently of request logs.
// It contains no provider credentials.
type BudgetAsyncTask struct {
	ChannelID   int         `json:"channelId"`
	ChannelName string      `json:"channelName,omitempty"`
	ExternalID  string      `json:"externalId"`
	Format      string      `json:"format"`
	Price       *ModelPrice `json:"price,omitempty"`
}
