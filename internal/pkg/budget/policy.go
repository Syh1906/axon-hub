package budget

import (
	"fmt"
	"time"
)

// Policy is persisted independently of routing profiles. The service assigns
// StartedAt once and does not change it when limits are disabled or re-enabled.
type Policy struct {
	Rules     []Rule           `json:"rules"`
	Timezone  string           `json:"timezone"`
	ExpiresAt *time.Time       `json:"expiresAt,omitempty"`
	StartedAt time.Time        `json:"startedAt"`
	Clocks    map[Period]Clock `json:"clocks"`
}

func (p Policy) Validate() error {
	if _, err := time.LoadLocation(p.Timezone); err != nil || p.Timezone == "" {
		return fmt.Errorf("invalid budget timezone")
	}
	seen := make(map[Period]bool, len(p.Rules))
	for _, rule := range p.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
		if seen[rule.Period] {
			return fmt.Errorf("duplicate budget period: %s", rule.Period)
		}
		seen[rule.Period] = true
	}
	return nil
}

func (p Policy) Expired(now time.Time) bool {
	return p.ExpiresAt != nil && !now.Before(*p.ExpiresAt)
}

type Usage struct {
	Settled  Amount
	Reserved Amount
}

type Blocked struct {
	Scope  string
	Period Period
	Reason string
}

// Check evaluates a consistent snapshot supplied by a database transaction.
// It never allocates or releases reservations. Both checks and reservation
// writes must share that transaction; calling Check alone is not admission.
// A missing usage entry means the authoritative read confirmed zero usage,
// never that the query was skipped or failed. Read failures belong to the caller.
func Check(now time.Time, scope string, policy Policy, usage map[Period]Usage, estimate Amount) (*Blocked, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if estimate < 0 {
		return nil, fmt.Errorf("negative estimate")
	}
	if policy.Expired(now) {
		return &Blocked{Scope: scope, Reason: "expired"}, nil
	}
	for _, rule := range policy.Rules {
		used := usage[rule.Period]
		if used.Settled < 0 || used.Reserved < 0 {
			return nil, fmt.Errorf("negative budget accounting")
		}
		if !Fits(rule.Limit, used.Settled, used.Reserved, estimate) {
			return &Blocked{Scope: scope, Period: rule.Period, Reason: "insufficient_budget"}, nil
		}
	}
	return nil, nil
}

// Contains follows the bounds returned by Bounds. Reset versions must be
// matched separately, so simultaneous settlement/reset is ordered by the DB.
func (w Window) Contains(at time.Time) bool {
	if w.Empty {
		return false
	}
	if w.Start != nil {
		if at.Before(*w.Start) || (w.StartExclusive && at.Equal(*w.Start)) {
			return false
		}
	}
	if w.End != nil {
		if w.StartExclusive {
			return !at.After(*w.End)
		}
		return at.Before(*w.End)
	}
	return true
}
