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

var budgetVersionColumns = map[budget.Period]string{
	budget.FiveHour: budgetposting.FieldFiveHourVersion, budget.Daily: budgetposting.FieldDailyVersion,
	budget.Weekly: budgetposting.FieldWeeklyVersion, budget.Monthly: budgetposting.FieldMonthlyVersion, budget.Total: budgetposting.FieldTotalVersion,
}

// budgetUsage aggregates all required rolling windows in one indexed scan.
// A missing fixed counter is rebuilt from the durable ledger, never request logs.
func budgetUsage(ctx context.Context, client *ent.Client, account *ent.BudgetAccount, now time.Time) (map[budget.Period]budget.Usage, error) {
	if err := advanceBudget(account, now); err != nil {
		return nil, err
	}
	state := &account.State
	usage := map[budget.Period]budget.Usage{}
	type term struct {
		rule    budget.Rule
		window  budget.Window
		version int64
	}
	terms := []term{}
	var earliest *time.Time
	unbounded := false
	for _, rule := range state.Policy.Rules {
		loc, err := budgetRuleLocation(rule, *state)
		if err != nil {
			return nil, err
		}
		clock := state.Policy.Clocks[rule.Period]
		w, err := rule.Bounds(now, clock, loc)
		if err != nil {
			return nil, err
		}
		u := budget.Usage{Reserved: budget.Amount(account.Reserved)}
		counter, exists := state.Counters[rule.Period]
		if rule.Mode == budget.Fixed && exists {
			u.Settled = counter.Amount
		} else if !w.Empty && !state.Policy.StartedAt.IsZero() {
			if w.Start == nil || w.Start.Before(state.Policy.StartedAt) {
				start := state.Policy.StartedAt
				w.Start = &start
				w.StartExclusive = false
			}
			terms = append(terms, term{rule, w, clock.ResetVersion})
			if w.Start == nil {
				unbounded = true
			} else if earliest == nil || w.Start.Before(*earliest) {
				earliest = w.Start
			}
		}
		usage[rule.Period] = u
	}
	if len(terms) == 0 {
		return usage, nil
	}
	var rows []struct {
		U0 int64
		U1 int64
		U2 int64
		U3 int64
		U4 int64
	}
	q := client.BudgetPosting.Query().Where(budgetposting.ProjectIDEQ(account.ProjectID), budgetposting.AccountIDEQ(account.ID))
	if !unbounded && earliest != nil {
		q.Where(budgetposting.SettledAtGTE(*earliest))
	}
	err := q.Modify(func(s *sql.Selector) {
		expressions := make([]sql.Querier, 0, len(terms))
		for i, t := range terms {
			expressions = append(expressions, sql.ExprFunc(func(b *sql.Builder) {
				b.WriteString("COALESCE(SUM(CASE WHEN ").Ident(s.C(budgetVersionColumns[t.rule.Period])).WriteString(" = ").Arg(t.version)
				if t.window.Start != nil {
					b.WriteString(" AND ").Ident(s.C(budgetposting.FieldSettledAt))
					if t.window.StartExclusive {
						b.WriteString(" > ")
					} else {
						b.WriteString(" >= ")
					}
					b.Arg(*t.window.Start)
				}
				if t.window.End != nil {
					b.WriteString(" AND ").Ident(s.C(budgetposting.FieldSettledAt))
					if t.rule.Mode == budget.Rolling {
						b.WriteString(" <= ")
					} else {
						b.WriteString(" < ")
					}
					b.Arg(*t.window.End)
				}
				b.WriteString(" THEN ").Ident(s.C(budgetposting.FieldAmount)).WriteString(" ELSE 0 END), 0) AS ").Ident(fmt.Sprintf("u%d", i))
			}))
		}
		s.SelectExpr(expressions...)
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("budget aggregate returned no result")
	}
	values := []int64{rows[0].U0, rows[0].U1, rows[0].U2, rows[0].U3, rows[0].U4}
	for i, t := range terms {
		u := usage[t.rule.Period]
		u.Settled = budget.Amount(values[i])
		usage[t.rule.Period] = u
		if t.rule.Mode == budget.Fixed {
			loc, err := budgetRuleLocation(t.rule, *state)
			if err != nil {
				return nil, err
			}
			// Counter identity follows the actual cycle, not the shortened query
			// lower bound used for first activation.
			w, err := t.rule.Bounds(now, state.Policy.Clocks[t.rule.Period], loc)
			if err != nil {
				return nil, err
			}
			state.Counters[t.rule.Period] = budget.Counter{Start: w.Start, Amount: u.Settled, ResetVersion: t.version}
		}
	}
	return usage, nil
}
