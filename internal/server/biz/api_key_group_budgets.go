package biz

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetposting"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

// The caller has already selected authorized project groups. Read account
// states in bulk and aggregate missing/rolling counters in bounded SQL batches.
func (s *APIKeyService) groupBudgetViews(ctx context.Context, projectID int, groupIDs []int, now time.Time) (map[int]*BudgetView, error) {
	views := map[int]*BudgetView{}
	if len(groupIDs) == 0 {
		return views, nil
	}
	zone := s.SystemService.TimeLocation(ctx).String()
	ctx = authz.WithSystemBypass(ctx, "key-group-budget-summary")
	db := s.entFromContext(ctx)
	for start := 0; start < len(groupIDs); start += 20 {
		end := start + 20
		if end > len(groupIDs) {
			end = len(groupIDs)
		}
		ids := groupIDs[start:end]
		accounts, err := db.BudgetAccount.Query().Where(budgetaccount.ProjectIDEQ(projectID), budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindGroup), budgetaccount.OwnerIDIn(ids...)).All(ctx)
		if err != nil {
			return nil, err
		}
		byGroup := map[int]*ent.BudgetAccount{}
		for _, account := range accounts {
			byGroup[account.OwnerID] = account
		}
		for _, id := range ids {
			if byGroup[id] == nil {
				state := budget.NewState()
				state.Policy.Rules = initialBudgetRules()
				byGroup[id] = &ent.BudgetAccount{ProjectID: projectID, OwnerID: id, OwnerKind: budgetaccount.OwnerKindGroup, State: state}
			}
			if byGroup[id].Revision == 0 {
				byGroup[id].State.Policy.Timezone = zone
			}
		}
		type term struct {
			accountID int
			window    budget.Window
			version   int64
			rolling   bool
		}
		terms := map[budget.Period][]term{}
		usages := map[int]map[budget.Period]budget.Usage{}
		accountToGroup := map[int]int{}
		queryIDs := []int{}
		for _, id := range ids {
			a := byGroup[id]
			if err := advanceBudget(a, now); err != nil {
				return nil, err
			}
			usages[id] = map[budget.Period]budget.Usage{}
			needsQuery := false
			for _, rule := range a.State.Policy.Rules {
				u := budget.Usage{Reserved: budget.Amount(a.Reserved)}
				counter, exists := a.State.Counters[rule.Period]
				if rule.Mode == budget.Fixed && exists {
					u.Settled = counter.Amount
				} else if !a.State.Policy.StartedAt.IsZero() {
					loc, err := budgetRuleLocation(rule, a.State)
					if err != nil {
						return nil, err
					}
					clock := a.State.Policy.Clocks[rule.Period]
					w, err := rule.Bounds(now, clock, loc)
					if err != nil {
						return nil, err
					}
					if !w.Empty {
						if w.Start == nil || w.Start.Before(a.State.Policy.StartedAt) {
							at := a.State.Policy.StartedAt
							w.Start = &at
							w.StartExclusive = false
						}
						terms[rule.Period] = append(terms[rule.Period], term{a.ID, w, clock.ResetVersion, rule.Mode == budget.Rolling})
						needsQuery = true
					}
				}
				usages[id][rule.Period] = u
			}
			if needsQuery {
				queryIDs = append(queryIDs, a.ID)
				accountToGroup[a.ID] = id
			}
		}
		if len(queryIDs) > 0 {
			var rows []struct {
				AccountID int   `json:"account_id"`
				U0        int64 `json:"u0"`
				U1        int64 `json:"u1"`
				U2        int64 `json:"u2"`
				U3        int64 `json:"u3"`
				U4        int64 `json:"u4"`
			}
			err := db.BudgetPosting.Query().Where(budgetposting.ProjectIDEQ(projectID), budgetposting.AccountIDIn(queryIDs...)).Modify(func(q *sql.Selector) {
				exprs := []sql.Querier{sql.ExprFunc(func(b *sql.Builder) { b.Ident(q.C(budgetposting.FieldAccountID)) })}
				for i, period := range budget.Periods {
					items := terms[period]
					col := budgetVersionColumns[period]
					alias := fmt.Sprintf("u%d", i)
					exprs = append(exprs, sql.ExprFunc(func(b *sql.Builder) {
						b.WriteString("COALESCE(SUM(CASE WHEN ")
						if len(items) == 0 {
							b.WriteString("1=0")
						}
						for j, t := range items {
							if j > 0 {
								b.WriteString(" OR ")
							}
							b.WriteString("(").Ident(q.C(budgetposting.FieldAccountID)).WriteString(" = ").Arg(t.accountID).WriteString(" AND ").Ident(q.C(col)).WriteString(" = ").Arg(t.version)
							if t.window.Start != nil {
								b.WriteString(" AND ").Ident(q.C(budgetposting.FieldSettledAt))
								if t.window.StartExclusive {
									b.WriteString(" > ")
								} else {
									b.WriteString(" >= ")
								}
								b.Arg(*t.window.Start)
							}
							if t.window.End != nil {
								b.WriteString(" AND ").Ident(q.C(budgetposting.FieldSettledAt))
								if t.rolling {
									b.WriteString(" <= ")
								} else {
									b.WriteString(" < ")
								}
								b.Arg(*t.window.End)
							}
							b.WriteString(")")
						}
						b.WriteString(" THEN ").Ident(q.C(budgetposting.FieldAmount)).WriteString(" ELSE 0 END),0) AS ").Ident(alias)
					}))
				}
				q.SelectExpr(exprs...).GroupBy(q.C(budgetposting.FieldAccountID))
			}).Scan(ctx, &rows)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				group := accountToGroup[row.AccountID]
				values := []int64{row.U0, row.U1, row.U2, row.U3, row.U4}
				for i, p := range budget.Periods {
					for _, t := range terms[p] {
						if t.accountID == row.AccountID {
							u := usages[group][p]
							u.Settled = budget.Amount(values[i])
							usages[group][p] = u
							break
						}
					}
				}
			}
		}
		for _, id := range ids {
			view, err := buildBudgetViewWithUsage(ctx, db, byGroup[id], now, usages[id], false)
			if err != nil {
				return nil, err
			}
			views[id] = view
		}
	}
	return views, nil
}
