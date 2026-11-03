package biz

import (
	"context"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/scheduler"
	"github.com/looplj/axonhub/llm"
	"time"
)

// Persist evidence separately before the atomic log/ledger transaction. If that
// transaction fails, a bounded recovery pass can replay the exact same charge.
func (s *BudgetService) RecordUsage(ctx context.Context, executionID int, usage *llm.Usage, snapshot *objects.RequestCostSnapshot, cost *budget.Amount) error {
	return s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, _ []*ent.BudgetAccount) error {
		if charge.Status == budgetcharge.StatusSettled || charge.Status == budgetcharge.StatusReleased {
			return nil
		}
		update := db.BudgetCharge.UpdateOneID(charge.ID).SetStatus(budgetcharge.StatusUnknown).SetCheckAfter(time.Now().UTC().Add(time.Minute))
		if usage != nil {
			update.SetUsage(usage)
		}
		if snapshot != nil {
			update.SetCostSnapshot(snapshot)
		}
		if cost != nil {
			update.SetActual(int64(*cost))
		}
		return update.Exec(ctx)
	})
}
func (s *UsageLogService) RegisterBudgetRecovery(ctx context.Context, sched *scheduler.Scheduler, video *VideoService) error {
	return sched.Register(ctx, scheduler.TaskSpec{Name: "budget-recovery", Description: "核对未完成的额度结算", FixRate: time.Minute}, func(ctx context.Context) {
		if err := s.RecoverBudgets(ctx, video); err != nil {
			log.Error(ctx, "budget recovery", log.Cause(err))
		}
	})
}
func (s *UsageLogService) RecoverBudgets(ctx context.Context, video *VideoService) error {
	_, err := authz.RunWithSystemBypass(ctx, "budget-recovery", func(ctx context.Context) (bool, error) {
		charges, err := s.db.BudgetCharge.Query().Where(
			budgetcharge.StatusIn(budgetcharge.StatusPrepared, budgetcharge.StatusSent, budgetcharge.StatusUnknown),
			budgetcharge.CheckAfterLTE(time.Now().UTC()),
			// A released hold with no billing evidence cannot change by polling the
			// same empty record. RecordUsage makes it eligible when evidence arrives.
			budgetcharge.Or(budgetcharge.StatusNEQ(budgetcharge.StatusUnknown), budgetcharge.ReservationReleasedAtIsNil(),
				budgetcharge.ActualNotNil(), budgetcharge.UsageNotNil(), budgetcharge.CostSnapshotNotNil(), budgetcharge.AsyncTaskNotNil()),
		).Order(ent.Asc(budgetcharge.FieldCheckAfter)).Limit(100).All(ctx)
		if err != nil {
			return false, err
		}
		for _, charge := range charges {
			// Move each item behind other work before processing; a malformed
			// price or provider failure must not starve the remaining charges.
			if err := s.db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(time.Now().UTC().Add(15 * time.Minute)).Exec(ctx); err != nil {
				return false, err
			}
			if charge.Status == budgetcharge.StatusPrepared {
				if err := s.BudgetService.RecoverAttempt(ctx, charge.ExecutionID); err != nil {
					return false, err
				}
				continue
			}
			req, reqErr := s.db.Request.Query().Where(request.IDEQ(charge.RequestID)).Select(
				request.FieldID, request.FieldProjectID, request.FieldAPIKeyID, request.FieldSource, request.FieldFormat, request.FieldStatus, request.FieldExternalID,
			).Only(ctx)
			execution, execErr := s.db.RequestExecution.Query().Where(requestexecution.IDEQ(charge.ExecutionID)).Select(
				requestexecution.FieldID, requestexecution.FieldChannelID, requestexecution.FieldModelID, requestexecution.FieldFormat,
				requestexecution.FieldStatus, requestexecution.FieldResponseStatusCode,
			).Only(ctx)
			if reqErr != nil && !ent.IsNotFound(reqErr) {
				return false, reqErr
			}
			if execErr != nil && !ent.IsNotFound(execErr) {
				return false, execErr
			}
			if charge.AsyncTask == nil && video != nil && req != nil && execution != nil && req.ExternalID != "" && (execution.Format == llm.APIFormatOpenAIVideo.String() || execution.Format == llm.APIFormatSeedanceVideo.String()) {
				if _, err := video.GetTask(ent.NewContext(ctx, s.db), req.ID); err != nil {
					log.Warn(ctx, "recover video budget", log.Int("request_id", req.ID), log.Cause(err))
				}
			}
			if charge.AsyncTask != nil && !charge.AsyncTask.Finished() && charge.Actual == nil && video != nil {
				response, taskErr := video.pollBudgetTask(ctx, charge.AsyncTask)
				if taskErr != nil {
					log.Warn(ctx, "recover async budget task", log.Int("charge_id", charge.ID), log.Cause(taskErr))
				}
				if taskErr == nil && response != nil && response.Video != nil && (response.Video.Status == "succeeded" || response.Video.Status == "failed") {
					if req != nil && video.RequestService != nil {
						if err := video.RequestService.UpdateRequestStatusExternalIDAndResponseBody(ent.NewContext(ctx, s.db), req.ID, mapVideoStatusToRequestStatus(response.Video.Status), charge.AsyncTask.ExternalID, response.Video, nil); err != nil {
							log.Warn(ctx, "update recovered video task", log.Cause(err))
						}
					}
					snapshot := charge.CostSnapshot
					if snapshot != nil {
						copied := *snapshot
						copied.Price = charge.AsyncTask.Price
						snapshot = &copied
					}
					usage := completedVideoUsage(response.Usage, response.Video.Status == "succeeded", snapshot)
					if usage == nil {
						usage = charge.Usage
					}
					var cost *budget.Amount
					if usage != nil || response.Video.Status == "succeeded" {
						cost, err = s.actualBudgetCost(ctx, usage, snapshot)
						if err != nil {
							return false, err
						}
					}
					if err := s.BudgetService.RecordUsage(ctx, charge.ExecutionID, usage, snapshot, cost); err != nil {
						return false, err
					}
					charge.Usage, charge.CostSnapshot = usage, snapshot
					if cost != nil {
						value := int64(*cost)
						charge.Actual = &value
					}
					if err := s.BudgetService.RecordAsyncResult(ctx, charge.ExecutionID, response.Video.Status); err != nil {
						return false, err
					}
					charge.AsyncTask.Status = response.Video.Status
				}
			}
			if charge.AsyncTask != nil && charge.AsyncTask.Finished() && req != nil {
				status := mapVideoStatusToRequestStatus(charge.AsyncTask.Status)
				if req.Status != status {
					if err := s.db.Request.UpdateOneID(req.ID).SetStatus(status).Select(request.FieldID).Exec(ctx); err != nil {
						return false, err
					}
					req.Status = status
				}
			}
			completedFee := charge.AsyncTask == nil && execution != nil && execution.Status == requestexecution.StatusCompleted &&
				req != nil && req.Status == request.StatusCompleted && charge.CostSnapshot != nil
			if charge.Actual != nil || charge.Usage != nil || completedFee {
				var recordedCost *budget.Amount
				if charge.Actual != nil {
					value := budget.Amount(*charge.Actual)
					recordedCost = &value
				}
				if req != nil && execution != nil {
					execution.CostSnapshot = charge.CostSnapshot
					if _, err := s.createUsageLogFromRequest(ctx, req, execution, charge.Usage, recordedCost); err != nil {
						return false, err
					}
				} else if charge.Actual != nil {
					cost := budget.Amount(*charge.Actual)
					if _, _, err := s.BudgetService.Settle(ctx, charge.ExecutionID, &cost, nil); err != nil {
						return false, err
					}
				}
			}
			if err := s.BudgetService.RecoverAttempt(ctx, charge.ExecutionID); err != nil {
				return false, err
			}
		}
		return true, nil
	})
	return err
}

// RecordAsyncResult persists the provider's terminal outcome independently of request log retention.
// Old tasks without this evidence remain pending, never assumed free or finished.
func (s *BudgetService) RecordAsyncResult(ctx context.Context, executionID int, status string) error {
	return s.withCharge(ctx, executionID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, _ []*ent.BudgetAccount) error {
		if charge.Status == budgetcharge.StatusSettled || charge.Status == budgetcharge.StatusReleased {
			return nil
		}
		update := db.BudgetCharge.UpdateOneID(charge.ID)
		var task objects.BudgetAsyncTask
		if charge.AsyncTask != nil {
			task = *charge.AsyncTask
		} else {
			// Legacy submissions or a failed task save can still have the provider
			// ID on the execution. GetTask has now confirmed the provider outcome;
			// preserve that evidence before deciding whether to release its hold.
			execution, err := db.RequestExecution.Query().Where(requestexecution.IDEQ(executionID)).Select(
				requestexecution.FieldChannelID, requestexecution.FieldExternalID, requestexecution.FieldFormat, requestexecution.FieldCostSnapshot,
			).Only(ctx)
			if err != nil {
				return err
			}
			task = objects.BudgetAsyncTask{ChannelID: execution.ChannelID, ExternalID: execution.ExternalID, Format: execution.Format}
			if execution.CostSnapshot != nil {
				task.Price = execution.CostSnapshot.CurrentPrice()
				update.SetCostSnapshot(execution.CostSnapshot)
			}
		}
		task.Status = status
		return update.SetAsyncTask(&task).Exec(ctx)
	})
}

// RecordAsyncTask saves the provider identifier before returning it to the caller.
func (s *BudgetService) RecordAsyncTask(ctx context.Context, execution *ent.RequestExecution, externalID string) error {
	if execution == nil || externalID == "" {
		return nil
	}
	return s.withCharge(ctx, execution.ID, func(ctx context.Context, db *ent.Client, charge *ent.BudgetCharge, _ []*ent.BudgetAccount) error {
		if charge.Status == budgetcharge.StatusSettled || charge.Status == budgetcharge.StatusReleased {
			return nil
		}
		task := &objects.BudgetAsyncTask{ChannelID: execution.ChannelID, ExternalID: externalID, Format: execution.Format}
		update := db.BudgetCharge.UpdateOneID(charge.ID).SetAsyncTask(task).SetStatus(budgetcharge.StatusUnknown).ClearOwnerExpiresAt()
		if execution.CostSnapshot != nil {
			task.Price = execution.CostSnapshot.CurrentPrice()
			update.SetCostSnapshot(execution.CostSnapshot)
		}
		return update.Exec(ctx)
	})
}
