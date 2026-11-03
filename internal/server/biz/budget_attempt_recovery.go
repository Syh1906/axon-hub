package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

// RecoverAttempt makes the release decision on the primary under the same
// account locks as renewal and settlement. Replica/candidate snapshots grant no
// authority to release a reservation or revoke a live execution.
func (s *BudgetService) RecoverAttempt(ctx context.Context, executionID int) error {
	return s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, accounts []*ent.BudgetAccount) error {
		if charge.ReservationReleasedAt != nil {
			return nil
		}
		now, err := budgetDatabaseTime(ctx, db)
		if err != nil {
			return err
		}
		expired := charge.OwnerToken != nil && charge.OwnerExpiresAt != nil && !charge.OwnerExpiresAt.After(now)
		if charge.OwnerToken != nil && *charge.OwnerToken != "" && charge.OwnerExpiresAt != nil && charge.OwnerExpiresAt.After(now) {
			return db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(time.Now().UTC().Add(time.Minute)).Exec(ctx)
		}
		execution, err := db.RequestExecution.Query().Where(requestexecution.IDEQ(charge.ExecutionID), requestexecution.ProjectIDEQ(charge.ProjectID)).
			Select(requestexecution.FieldID, requestexecution.FieldStatus).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		req, err := db.Request.Query().Where(request.IDEQ(charge.RequestID), request.ProjectIDEQ(charge.ProjectID)).
			Select(request.FieldID, request.FieldStatus).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		finished := execution != nil && execution.Status != requestexecution.StatusPending && execution.Status != requestexecution.StatusProcessing &&
			(req == nil || req.Status != request.StatusPending && req.Status != request.StatusProcessing)
		asyncFinished := charge.AsyncTask != nil && charge.AsyncTask.Finished()
		if !expired && !finished && !asyncFinished {
			if charge.OwnerExpiresAt != nil {
				return db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(time.Now().UTC().Add(time.Minute)).Exec(ctx)
			}
			return nil
		}
		if expired {
			// Keep revocation distinguishable from normal finalization, including
			// when a delayed owner wakes after recovery already released its hold.
			if err := db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerToken("").Exec(ctx); err != nil {
				return err
			}
		}
		pending := false
		if charge.Status != budgetcharge.StatusPrepared {
			pending, err = budgetAsyncStillPending(ctx, db, charge)
			if err != nil {
				return err
			}
		}
		if err := endBudgetAttempt(ctx, db, charge, accounts); err != nil {
			return err
		}
		if !expired || pending {
			return nil
		}
		message := "执行归属已失效，请求已中断；预留已释放，实际费用仍待核对"
		if charge.Status == budgetcharge.StatusPrepared {
			message = "执行归属已失效，请求未发送；预留已释放"
		}
		_, err = db.RequestExecution.Update().Where(requestexecution.IDEQ(charge.ExecutionID), requestexecution.ProjectIDEQ(charge.ProjectID),
			requestexecution.StatusIn(requestexecution.StatusPending, requestexecution.StatusProcessing)).
			SetStatus(requestexecution.StatusFailed).SetErrorMessage(message).
			SetFailure(&objects.RequestExecutionFailure{Source: "gateway", Code: "execution_owner_lost", Type: "budget_error"}).Save(ctx)
		if err != nil {
			return err
		}
		// An older attempt cannot finish a logical request that has a newer retry.
		_, err = db.Request.Update().Where(request.IDEQ(charge.RequestID), request.ProjectIDEQ(charge.ProjectID),
			request.StatusIn(request.StatusPending, request.StatusProcessing),
			request.Not(request.HasExecutionsWith(requestexecution.IDGT(charge.ExecutionID)))).
			SetStatus(request.StatusFailed).
			SetFailure(&objects.RequestFailure{Stage: "execution", Code: "execution_owner_lost", Type: "budget_error", Message: message}).Save(ctx)
		return err
	})
}

// Age-based startup cleanup must leave held budget executions to the recovery
// protocol. This includes legacy requests still running during rolling upgrades.
func withoutBudgetHold(chargeField string) func(*sql.Selector) {
	return func(s *sql.Selector) {
		charges := sql.Table(budgetcharge.Table)
		s.Where(sql.Not(sql.Exists(sql.Select(charges.C(budgetcharge.FieldID)).From(charges).Where(sql.And(
			sql.ColumnsEQ(charges.C(chargeField), s.C("id")),
			sql.ColumnsEQ(charges.C(budgetcharge.FieldProjectID), s.C("project_id")),
			sql.IsNull(charges.C(budgetcharge.FieldReservationReleasedAt)),
		)))))
	}
}
