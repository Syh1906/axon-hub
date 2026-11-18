package biz

import (
	"context"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetchange"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

// Resolve against the same detailed budget snapshot as the editor. Never move
// an expired preview to a later release, even if the revision did not change.
func (in GroupWindowGrantInput) resolve(view *BudgetView) (budget.WindowGrant, error) {
	if view.Revision != in.Revision {
		return budget.WindowGrant{}, fmt.Errorf("budget changed; reload before saving")
	}
	amount, err := budget.ParseAmount(in.Amount)
	if err != nil || amount <= 0 {
		return budget.WindowGrant{}, fmt.Errorf("invalid temporary amount")
	}
	for _, w := range view.Windows {
		if w.Period != in.Period || w.Period == budget.Total || view.Expired {
			continue
		}
		base, err := budget.ParseAmount(w.Limit)
		if err != nil {
			return budget.WindowGrant{}, err
		}
		if base == 0 {
			break
		}
		until := w.WindowEnd
		if w.Mode == budget.Rolling {
			until = w.NextReleaseAt
		}
		if until == nil || !until.After(view.ServerTime) || !until.Equal(in.Until) {
			break
		}
		if w.PendingAt != nil && w.PendingAt.Before(*until) {
			return budget.WindowGrant{}, fmt.Errorf("budget schedule changes before the grant deadline")
		}
		extra, err := budget.ParseAmount(w.TemporaryAmount)
		if err != nil {
			return budget.WindowGrant{}, err
		}
		total, err := extra.Add(amount)
		if err != nil {
			return budget.WindowGrant{}, err
		}
		if _, err := base.Add(total); err != nil {
			return budget.WindowGrant{}, err
		}
		return budget.WindowGrant{Amount: total, Until: *until}, nil
	}
	return budget.WindowGrant{}, fmt.Errorf("temporary budget window changed; reopen before saving")
}

// Called inside the authorized management row transaction. The row receipt
// makes retries idempotent; the budget lock serializes grants with reservations.
func (s *APIKeyService) grantGroupWindow(ctx context.Context, projectID, groupID, actorID int, operation string, in *GroupWindowGrantInput) error {
	return NewBudgetService(s.db).transaction(ctx, func(ctx context.Context, db *ent.Client) error {
		account, err := LockBudgetAccount(ctx, db, projectID, budgetaccount.OwnerKindGroup, groupID)
		if err != nil {
			return err
		}
		view, err := buildBudgetView(ctx, db, account, time.Now().UTC())
		if err != nil {
			return err
		}
		grant, err := in.resolve(view)
		if err != nil {
			return err
		}
		before, err := cloneBudgetState(account.State)
		if err != nil {
			return err
		}
		if account.State.Policy.Temporary == nil {
			account.State.Policy.Temporary = map[budget.Period]budget.WindowGrant{}
		}
		account.State.Policy.Temporary[in.Period] = grant
		if err := db.BudgetAccount.UpdateOneID(account.ID).SetState(account.State).AddRevision(1).Exec(ctx); err != nil {
			return err
		}
		return db.BudgetChange.Create().SetProjectID(projectID).SetAccountID(account.ID).SetActorID(actorID).
			SetOperationID(operation).SetOperation(budgetchange.OperationConfigure).SetBefore(before).SetAfter(account.State).Exec(ctx)
	})
}
