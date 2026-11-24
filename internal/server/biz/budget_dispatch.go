package biz

import (
	"context"
	"fmt"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/samber/lo"
)

// UpdateDispatch records the first provider handoff, or retracts this execution's
// handoff when the transport positively identifies a pre-write failure.
func (s *BudgetService) UpdateDispatch(ctx context.Context, req *ent.Request, exec *ent.RequestExecution, held, sent bool) error {
	billing := exec.CostSnapshot
	if billing == nil || billing.Billing == nil {
		if !held {
			return nil
		}
		if sent {
			return s.MarkSent(ctx, exec.ID)
		}
		return s.ReleaseUnwrittenAttempt(ctx, exec.ID)
	}
	record := func(ctx context.Context, db *ent.Client) error {
		if sent {
			saved, err := db.Request.Query().Where(request.IDEQ(req.ID), request.ProjectIDEQ(req.ProjectID), request.APIKeyIDEQ(req.APIKeyID)).Select(request.FieldBillingGroupRef).Only(ctx)
			if err != nil {
				return err
			}
			group := saved.BillingGroupRef
			if group == nil {
				key, err := db.APIKey.Query().Where(apikey.IDEQ(req.APIKeyID), apikey.ProjectIDEQ(req.ProjectID)).Select(apikey.FieldGroupRef).Only(ctx)
				if err != nil {
					return err
				}
				group = key.GroupRef
			}
			if lo.FromPtr(group) != lo.FromPtr(billing.Billing.GroupID) {
				return ErrBillingGroupChanged
			}
			if saved.BillingGroupRef == nil {
				if err := db.Request.UpdateOneID(req.ID).SetBillingGroupRef(lo.FromPtr(group)).Exec(ctx); err != nil {
					return err
				}
			}
		}
		err := db.RequestExecution.UpdateOneID(exec.ID).Where(requestexecution.RequestIDEQ(req.ID), requestexecution.ProjectIDEQ(req.ProjectID), requestexecution.BillingDispatchedEQ(!sent)).SetBillingDispatched(sent).Exec(ctx)
		if err != nil || sent {
			return err
		}
		other, err := db.RequestExecution.Query().Where(requestexecution.RequestIDEQ(req.ID), requestexecution.BillingDispatchedEQ(true)).Exist(ctx)
		if err != nil || other {
			return err
		}
		return db.Request.UpdateOneID(req.ID).ClearBillingGroupRef().Exec(ctx)
	}
	if !held {
		return s.transaction(ctx, func(ctx context.Context, db *ent.Client) error {
			if _, err := LockBudgetAccount(ctx, db, req.ProjectID, budgetaccount.OwnerKindKey, req.APIKeyID); err != nil {
				return err
			}
			return record(ctx, db)
		})
	}
	// Commit the group decision and charge transition under the same account lock.
	observed := false
	err := s.withCharge(ctx, exec.ID, func(ctx context.Context, db *ent.Client, c *ent.BudgetCharge, accounts []*ent.BudgetAccount) error {
		observed = true
		if c.KeyID != req.APIKeyID || c.RequestID != req.ID || c.ProjectID != req.ProjectID {
			return fmt.Errorf("budget dispatch identity mismatch")
		}
		if err := s.checkAttemptOwner(ctx, db, c); err != nil {
			return err
		}
		if sent {
			if c.Status != budgetcharge.StatusPrepared {
				return fmt.Errorf("budget attempt is no longer prepared")
			}
			if err := db.BudgetCharge.UpdateOneID(c.ID).SetStatus(budgetcharge.StatusSent).Exec(ctx); err != nil {
				return err
			}
		} else {
			if c.Status != budgetcharge.StatusSent || c.ReservationReleasedAt != nil {
				return fmt.Errorf("budget attempt is no longer pending transport")
			}
			if err := finishBudgetAttempt(ctx, db, c, accounts, true); err != nil {
				return err
			}
		}
		return record(ctx, db)
	})
	if err == nil && !observed {
		return fmt.Errorf("missing reserved budget charge")
	}
	return err
}
