package biz

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetposting"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

type BudgetRuleInput struct {
	RestoreCalendar bool          `json:"restoreCalendar"`
	Period          budget.Period `json:"period"`
	Mode            budget.Mode   `json:"mode"`
	Limit           string        `json:"limit"`
	Minute          int           `json:"minute"`
}

type BudgetSettingsInput struct {
	Rules       []*BudgetRuleInput `json:"rules"`
	Timezone    string             `json:"timezone"`
	ExpiresAt   *time.Time         `json:"expiresAt,omitempty"`
	Revision    int                `json:"revision"`
	OperationID string             `json:"operationId"`
}

type BudgetWindowView struct {
	TemporaryAmount   string           `json:"temporaryAmount"`
	TemporaryUntil    *time.Time       `json:"temporaryUntil,omitempty"`
	Period            budget.Period    `json:"period"`
	Mode              budget.Mode      `json:"mode"`
	Minute            int              `json:"minute"`
	Limit             string           `json:"limit"`
	Settled           string           `json:"settled"`
	Reserved          string           `json:"reserved"`
	Available         string           `json:"available"`
	WindowStart       *time.Time       `json:"windowStart,omitempty"`
	WindowEnd         *time.Time       `json:"windowEnd,omitempty"`
	NextReleaseAt     *time.Time       `json:"nextReleaseAt,omitempty"`
	ResetAt           *time.Time       `json:"resetAt,omitempty"`
	CanResetCycle     bool             `json:"canResetCycle"`
	EffectiveSchedule string           `json:"effectiveSchedule"`
	Timezone          string           `json:"timezone"`
	PendingRule       *BudgetRuleInput `json:"pendingRule,omitempty"`
	PendingAt         *time.Time       `json:"pendingAt,omitempty"`
}

type BudgetView struct {
	OwnerKind           string              `json:"ownerKind"`
	OwnerID             int                 `json:"ownerID"`
	Revision            int                 `json:"revision"`
	Timezone            string              `json:"timezone"`
	ExpiresAt           *time.Time          `json:"expiresAt,omitempty"`
	AccountingStartedAt *time.Time          `json:"accountingStartedAt,omitempty"`
	ServerTime          time.Time           `json:"serverTime"`
	HasLimits           bool                `json:"hasLimits"`
	Expired             bool                `json:"expired"`
	Windows             []*BudgetWindowView `json:"windows"`
}

func buildBudgetView(ctx context.Context, db *ent.Client, account *ent.BudgetAccount, now time.Time) (*BudgetView, error) {
	usage, err := budgetUsage(ctx, db, account, now)
	if err != nil {
		return nil, err
	}
	return buildBudgetViewWithUsage(ctx, db, account, now, usage, true)
}

func buildBudgetViewWithUsage(ctx context.Context, db *ent.Client, account *ent.BudgetAccount, now time.Time, usage map[budget.Period]budget.Usage, includeRelease bool) (*BudgetView, error) {
	state := account.State
	view := &BudgetView{OwnerKind: string(account.OwnerKind), OwnerID: account.OwnerID, Revision: int(account.Revision), Timezone: state.Policy.Timezone,
		ExpiresAt: state.Policy.ExpiresAt, ServerTime: now, HasLimits: state.HasLimits(), Expired: state.Policy.Expired(now), Windows: []*BudgetWindowView{}}
	if !state.Policy.StartedAt.IsZero() {
		started := state.Policy.StartedAt
		view.AccountingStartedAt = &started
	}
	for _, rule := range state.Policy.Rules {
		loc, err := budgetRuleLocation(rule, state)
		if err != nil {
			return nil, err
		}
		clock := state.Policy.Clocks[rule.Period]
		w, err := rule.Bounds(now, clock, loc)
		if err != nil {
			return nil, err
		}
		u := usage[rule.Period]
		available := budget.Amount(0)
		grant := state.Policy.ActiveGrant(rule, now)
		limit, err := rule.Limit.Add(grant.Amount)
		if err != nil {
			return nil, err
		}
		if limit > u.Settled && limit-u.Settled > u.Reserved {
			available = limit - u.Settled - u.Reserved
		}
		item := &BudgetWindowView{Period: rule.Period, Mode: rule.Mode, Minute: rule.Minute, Limit: rule.Limit.String(), Settled: u.Settled.String(), Reserved: u.Reserved.String(), Available: available.String(),
			WindowStart: w.Start, WindowEnd: w.End, ResetAt: clock.ResetAt, CanResetCycle: rule.CanRestartCycle(), Timezone: loc.String()}
		item.TemporaryAmount = grant.Amount.String()
		if grant.Amount > 0 {
			item.TemporaryUntil = &grant.Until
		}
		if includeRelease && rule.Mode == budget.Rolling && u.Settled > 0 {
			postings := budgetPostingWindow(w, state.Policy.StartedAt)
			query := db.BudgetPosting.Query().Where(budgetposting.ProjectIDEQ(account.ProjectID), budgetposting.AccountIDEQ(account.ID), budgetposting.SettledAtLTE(*postings.End), budgetposting.AmountGT(0)).
				Where(func(s *sql.Selector) { s.Where(sql.EQ(s.C(budgetVersionColumns[rule.Period]), clock.ResetVersion)) }).Order(ent.Asc(budgetposting.FieldSettledAt))
			if postings.StartExclusive {
				query.Where(budgetposting.SettledAtGT(*postings.Start))
			} else {
				query.Where(budgetposting.SettledAtGTE(*postings.Start))
			}
			first, err := query.First(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return nil, err
			}
			if first != nil {
				next := first.SettledAt.Add(rule.Duration())
				item.NextReleaseAt = &next
			}
		}
		if clock.Anchor != nil && rule.Mode == budget.Fixed && rule.Period != budget.FiveHour && rule.Period != budget.Total {
			anchor := clock.Anchor.In(loc)
			switch rule.Period {
			case budget.Daily:
				item.EffectiveSchedule = anchor.Format("15:04")
			case budget.Weekly:
				item.EffectiveSchedule = fmt.Sprintf("%d %s", anchor.Weekday(), anchor.Format("15:04"))
			case budget.Monthly:
				item.EffectiveSchedule = fmt.Sprintf("%d %s", anchor.Day(), anchor.Format("15:04"))
			}
		}
		if pending, ok := state.Pending[rule.Period]; ok {
			item.PendingRule = &BudgetRuleInput{Period: pending.Rule.Period, Mode: pending.Rule.Mode, Limit: pending.Rule.Limit.String(), Minute: pending.Rule.Minute}
			item.PendingAt = &pending.At
		}
		view.Windows = append(view.Windows, item)
	}
	return view, nil
}

func initialBudgetRules() []budget.Rule {
	return []budget.Rule{{Period: budget.FiveHour, Mode: budget.Rolling}, {Period: budget.Daily, Mode: budget.Fixed}, {Period: budget.Weekly, Mode: budget.Fixed}, {Period: budget.Monthly, Mode: budget.Fixed}, {Period: budget.Total, Mode: budget.Fixed}}
}
