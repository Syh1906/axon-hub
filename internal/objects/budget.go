package objects

// BudgetAsyncTask keeps provider task and price independently of request logs.
// It contains no provider credentials.
type BudgetAsyncTask struct {
	ChannelID   int         `json:"channelId"`
	ChannelName string      `json:"channelName,omitempty"`
	ExternalID  string      `json:"externalId"`
	Format      string      `json:"format"`
	Status      string      `json:"status,omitempty"`
	Price       *ModelPrice `json:"price,omitempty"`
}

func (t *BudgetAsyncTask) Finished() bool {
	return t.Status == "succeeded" || t.Status == "failed"
}
