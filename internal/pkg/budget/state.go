package budget

import "time"

// Counter is the rebuildable fixed-window aggregate. Rolling windows use the
// independent posting ledger; neither ledger is owned by request-log GC.
type Counter struct {
	Start        *time.Time `json:"start,omitempty"`
	Amount       Amount     `json:"amount"`
	ResetVersion int64      `json:"resetVersion"`
}

type PendingRule struct {
	Rule Rule      `json:"rule"`
	At   time.Time `json:"at"`
}

type State struct {
	Policy   Policy                 `json:"policy"`
	Counters map[Period]Counter     `json:"counters"`
	Pending  map[Period]PendingRule `json:"pending"`
}

func NewState() State {
	return State{Policy: Policy{Timezone: "UTC", Clocks: map[Period]Clock{}}, Counters: map[Period]Counter{}, Pending: map[Period]PendingRule{}}
}

func (s State) HasLimits() bool {
	for _, r := range s.Policy.Rules {
		if r.Limit > 0 {
			return true
		}
	}
	return false
}
