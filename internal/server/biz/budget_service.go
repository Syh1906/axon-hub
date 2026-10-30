package biz

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

// BudgetService owns authoritative budget transactions. Authentication and
// management authorization must complete before entering its internal methods.
type BudgetService struct{ db *ent.Client }

func NewBudgetService(db *ent.Client) *BudgetService { return &BudgetService{db: db} }

func (s *BudgetService) transaction(ctx context.Context, fn func(context.Context, *ent.Client) error) error {
	_, err := authz.RunWithSystemBypass(ctx, "budget-accounting", func(ctx context.Context) (bool, error) {
		var tx *ent.Tx
		var err error
		if s.db.Driver().Dialect() == dialect.SQLite {
			tx, err = s.db.Tx(ctx)
		} else {
			tx, err = s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		}
		if err != nil {
			return false, err
		}
		defer tx.Rollback()
		if err = fn(ctx, tx.Client()); err != nil {
			return false, err
		}
		return true, tx.Commit()
	})
	return err
}

// LockBudgetAccount's upsert is intentionally the first database statement in
// admission transactions: it acquires SQLite's write lock before reading usage.
// Ignore preserves the existing state. All writers lock key before group.
func LockBudgetAccount(ctx context.Context, client *ent.Client, projectID int, kind budgetaccount.OwnerKind, ownerID int) (*ent.BudgetAccount, error) {
	if projectID <= 0 || ownerID <= 0 {
		return nil, fmt.Errorf("invalid budget owner")
	}
	state := budget.NewState()
	state.Policy.Rules = initialBudgetRules()
	err := client.BudgetAccount.Create().SetProjectID(projectID).SetOwnerKind(kind).SetOwnerID(ownerID).
		SetState(state).OnConflictColumns(budgetaccount.FieldProjectID, budgetaccount.FieldOwnerKind, budgetaccount.FieldOwnerID).Ignore().Exec(ctx)
	if err != nil {
		return nil, err
	}
	// A write holds the existing row lock as well on all supported databases.
	_, err = client.BudgetAccount.Update().Where(budgetaccount.ProjectIDEQ(projectID), budgetaccount.OwnerKindEQ(kind), budgetaccount.OwnerIDEQ(ownerID)).AddReserved(0).Save(ctx)
	if err != nil {
		return nil, err
	}
	query := client.BudgetAccount.Query().Where(budgetaccount.ProjectIDEQ(projectID), budgetaccount.OwnerKindEQ(kind), budgetaccount.OwnerIDEQ(ownerID))
	if client.Driver().Dialect() != dialect.SQLite {
		query.Modify(func(q *entsql.Selector) { q.ForUpdate() })
	}
	return query.Only(ctx)
}

func budgetVersions(state budget.State) map[budget.Period]int64 {
	versions := make(map[budget.Period]int64, len(budget.Periods))
	for _, period := range budget.Periods {
		versions[period] = state.Policy.Clocks[period].ResetVersion
	}
	return versions
}

func budgetLocation(state budget.State) (*time.Location, error) {
	return time.LoadLocation(state.Policy.Timezone)
}

func budgetRuleLocation(rule budget.Rule, state budget.State) (*time.Location, error) {
	if rule.Timezone != "" {
		return time.LoadLocation(rule.Timezone)
	}
	return budgetLocation(state)
}

// advanceBudget updates derived counters only when their actual window changes.
// It never clears reservations or moves the accounting reset boundary.
func advanceBudget(account *ent.BudgetAccount, now time.Time) error {
	state := &account.State
	if state.Counters == nil {
		state.Counters = map[budget.Period]budget.Counter{}
	}
	if state.Policy.Clocks == nil {
		state.Policy.Clocks = map[budget.Period]budget.Clock{}
	}
	if state.Pending == nil {
		state.Pending = map[budget.Period]budget.PendingRule{}
	}
	for i, rule := range state.Policy.Rules {
		if pending, ok := state.Pending[rule.Period]; ok && !now.Before(pending.At) {
			// Normal policy transition retains accounting versions. Historical
			// postings supply the new mode's usage; callers rebuild the counter.
			state.Policy.Rules[i] = pending.Rule
			delete(state.Pending, rule.Period)
			rule = pending.Rule
			delete(state.Counters, rule.Period)
			clock := state.Policy.Clocks[rule.Period]
			clock.Anchor = nil
			clock.CycleVersion++
			state.Policy.Clocks[rule.Period] = clock
		}
		if rule.Mode == budget.Rolling {
			continue
		}
		clock := state.Policy.Clocks[rule.Period]
		loc, err := budgetRuleLocation(rule, *state)
		if err != nil {
			return err
		}
		window, err := rule.Bounds(now, clock, loc)
		if err != nil {
			return err
		}
		counter, exists := state.Counters[rule.Period]
		if !exists {
			continue
		} // Rebuilt from postings by usage().
		if counter.ResetVersion != clock.ResetVersion || !sameBudgetTime(counter.Start, window.Start) || window.Empty {
			state.Counters[rule.Period] = budget.Counter{Start: window.Start, ResetVersion: clock.ResetVersion}
		}
	}
	return nil
}

func sameBudgetTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func (s *BudgetService) KeyExpired(ctx context.Context, projectID, keyID int) (expired bool, err error) {
	err = s.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		account, err := db.BudgetAccount.Query().Where(budgetaccount.ProjectIDEQ(projectID), budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey), budgetaccount.OwnerIDEQ(keyID)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		expired = account.State.Policy.Expired(time.Now().UTC())
		return nil
	})
	return
}

// Settlement uses immutable account IDs, including detached restored history.
func lockBudgetAccountID(ctx context.Context, db *ent.Client, id int) (*ent.BudgetAccount, error) {
	if err := db.BudgetAccount.UpdateOneID(id).AddReserved(0).Exec(ctx); err != nil {
		return nil, err
	}
	query := db.BudgetAccount.Query().Where(budgetaccount.IDEQ(id))
	if db.Driver().Dialect() != dialect.SQLite {
		query.Modify(func(q *entsql.Selector) { q.ForUpdate() })
	}
	return query.Only(ctx)
}
