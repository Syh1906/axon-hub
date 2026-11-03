package biz

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/llm"
)

func reserveOwnedBudget(t *testing.T, f budgetFixture, format llm.APIFormat) (*ent.Request, *ent.RequestExecution, *ent.BudgetCharge) {
	t.Helper()
	req := f.db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetModelID("fixture").
		SetRequestBody([]byte(`{}`)).SetStatus(request.StatusProcessing).SaveX(f.ctx)
	execution := f.db.RequestExecution.Create().SetProjectID(req.ProjectID).SetRequestID(req.ID).SetModelID("fixture").
		SetFormat(format.String()).SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusProcessing).SaveX(f.ctx)
	require.NoError(t, f.service.Reserve(f.ctx, req.ProjectID, f.key.ID, req.ID, execution.ID, budgetAmount(t, "7"), nil))
	return req, execution, f.db.BudgetCharge.Query().Where(budgetcharge.ExecutionIDEQ(execution.ID)).OnlyX(f.ctx)
}

func TestBudgetOwnerRecoveryClosesRecentOrphanAndSettlesLateCost(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(fmt.Sprintf("sent=%v", sent), func(t *testing.T) {
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			f.configure(t, "group", f.group.ID, "35")
			req, execution, charge := reserveOwnedBudget(t, f, llm.APIFormatOpenAIResponse)
			if sent {
				require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			}
			// Model a process dying seconds after send: request age is irrelevant.
			f.db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerExpiresAt(time.Now().Add(-time.Minute)).SetCheckAfter(time.Now().Add(-time.Minute)).ExecX(f.ctx)
			usage := NewUsageLogService(f.db, f.admin.SystemService, NewChannelServiceForTest(f.db))
			require.NoError(t, usage.RecoverBudgets(f.ctx, nil))
			require.NoError(t, usage.BudgetService.RecoverAttempt(f.ctx, execution.ID))
			charge = f.db.BudgetCharge.GetX(f.ctx, charge.ID)
			require.NotNil(t, charge.ReservationReleasedAt)
			require.Equal(t, "", *charge.OwnerToken)
			require.Nil(t, charge.Actual)
			for _, account := range f.db.BudgetAccount.Query().AllX(f.ctx) {
				require.Zero(t, account.Reserved)
			}
			require.Equal(t, request.StatusFailed, f.db.Request.GetX(f.ctx, req.ID).Status)
			require.Equal(t, "execution_owner_lost", f.db.RequestExecution.GetX(f.ctx, execution.ID).Failure.Code)
			require.Error(t, f.service.MarkSent(f.ctx, execution.ID))
			active, err := f.service.renewAttemptOwner(f.ctx, execution.ID)
			require.Error(t, err)
			require.False(t, active)
			if sent {
				require.Equal(t, budgetcharge.StatusUnknown, charge.Status)
				cost := budgetAmount(t, "0.25")
				require.NoError(t, f.service.RecordUsage(f.ctx, execution.ID, nil, nil, &cost))
				for range 2 {
					_, _, err := f.service.Settle(f.ctx, execution.ID, &cost, nil)
					require.NoError(t, err)
				}
				require.Equal(t, 2, f.db.BudgetPosting.Query().CountX(f.ctx))
			} else {
				require.Equal(t, budgetcharge.StatusReleased, charge.Status)
				require.Zero(t, f.db.BudgetPosting.Query().CountX(f.ctx))
			}
		})
	}
}

func TestBudgetOwnerRecoveryPreservesActiveAndLegacyAttempts(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, sent := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%v/sent=%v", legacy, sent), func(t *testing.T) {
				f := newBudgetFixture(t)
				f.configure(t, "key", f.key.ID, "35")
				req, execution, charge := reserveOwnedBudget(t, f, llm.APIFormatOpenAIResponse)
				if sent {
					require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
				}
				update := f.db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(time.Now().Add(-time.Minute))
				if legacy {
					update.ClearOwnerToken().ClearOwnerExpiresAt()
				}
				update.ExecX(f.ctx)
				usage := NewUsageLogService(f.db, f.admin.SystemService, NewChannelServiceForTest(f.db))
				require.NoError(t, usage.RecoverBudgets(f.ctx, nil))
				require.Nil(t, f.db.BudgetCharge.GetX(f.ctx, charge.ID).ReservationReleasedAt)
				require.Equal(t, request.StatusProcessing, f.db.Request.GetX(f.ctx, req.ID).Status)
				require.EqualValues(t, budgetAmount(t, "7"), f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey)).OnlyX(f.ctx).Reserved)
				if !legacy {
					if sent {
						require.NoError(t, f.service.RecordUsage(f.ctx, execution.ID, nil, nil, nil))
					}
					active, err := f.service.renewAttemptOwner(f.ctx, execution.ID)
					require.NoError(t, err)
					require.True(t, active)
					require.Error(t, usage.BudgetService.MarkSent(f.ctx, execution.ID), "another instance cannot dispatch this execution")
				}
			})
		}
	}
}

func TestBudgetOwnerRecoveryPreservesUncertainVideoAndNewRetry(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIVideo, llm.APIFormatSeedanceVideo, llm.APIFormatOpenAIResponse} {
		t.Run(format.String(), func(t *testing.T) {
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			req, execution, charge := reserveOwnedBudget(t, f, format)
			require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			if format == llm.APIFormatOpenAIResponse {
				newExec := f.db.RequestExecution.Create().SetProjectID(req.ProjectID).SetRequestID(req.ID).SetModelID("retry").
					SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusProcessing).SaveX(f.ctx)
				require.NoError(t, f.service.Reserve(f.ctx, req.ProjectID, f.key.ID, req.ID, newExec.ID, budgetAmount(t, "3"), nil))
			}
			f.db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerExpiresAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
			require.NoError(t, NewBudgetService(f.db).RecoverAttempt(f.ctx, execution.ID))
			charge = f.db.BudgetCharge.GetX(f.ctx, charge.ID)
			if format == llm.APIFormatOpenAIResponse {
				require.NotNil(t, charge.ReservationReleasedAt)
			} else {
				require.Nil(t, charge.ReservationReleasedAt, "missing provider task ID is not proof that submission failed")
			}
			require.Equal(t, request.StatusProcessing, f.db.Request.GetX(f.ctx, req.ID).Status)
		})
	}
}

func TestBudgetOwnerGuardCancelsLostOwnershipButNotNormalCompletion(t *testing.T) {
	for _, outcome := range []string{"recovered", "renewal_failure", "completed", "async"} {
		t.Run(outcome, func(t *testing.T) {
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			_, execution, charge := reserveOwnedBudget(t, f, llm.APIFormatOpenAIResponse)
			require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			switch outcome {
			case "recovered":
				f.db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerExpiresAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
				require.NoError(t, NewBudgetService(f.db).RecoverAttempt(f.ctx, execution.ID))
			case "renewal_failure":
				f.db.BudgetCharge.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						if m, ok := mutation.(*ent.BudgetChargeMutation); ok {
							if _, set := m.OwnerExpiresAt(); set {
								return nil, fmt.Errorf("injected renewal failure")
							}
						}
						return next.Mutate(ctx, mutation)
					})
				})
			case "completed":
				require.NoError(t, f.service.EndAttempt(f.ctx, execution.ID))
			case "async":
				require.NoError(t, f.service.RecordAsyncTask(f.ctx, execution, "accepted"))
			}
			attempt := f.service.keepAttemptAlive(f.ctx, execution.ID, time.Millisecond)
			defer attempt.Stop()
			transport, cancel := attempt.Bind(f.ctx)
			defer cancel()
			select {
			case <-attempt.done:
			case <-time.After(3 * time.Second):
				t.Fatal("owner guard did not finish")
			}
			if outcome == "recovered" || outcome == "renewal_failure" {
				require.True(t, IsBudgetDenied(attempt.Err()))
				select {
				case <-transport.Done():
				case <-time.After(time.Second):
					t.Fatal("transport not canceled")
				}
			} else {
				require.NoError(t, attempt.Err())
				require.NoError(t, transport.Err())
			}
		})
	}
}

func TestBudgetOwnerRenewalAndRecoverySerialize(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%v", expired), func(t *testing.T) {
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			_, execution, charge := reserveOwnedBudget(t, f, llm.APIFormatOpenAIResponse)
			require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			if expired {
				f.db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerExpiresAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var active bool
			var renewErr, recoverErr error
			go func() {
				defer wg.Done()
				defer func() {
					if value := recover(); value != nil {
						renewErr = fmt.Errorf("renew panic: %v", value)
					}
				}()
				<-start
				active, renewErr = f.service.renewAttemptOwner(f.ctx, execution.ID)
			}()
			go func() {
				defer wg.Done()
				defer func() {
					if value := recover(); value != nil {
						recoverErr = fmt.Errorf("recovery panic: %v", value)
					}
				}()
				<-start
				recoverErr = NewBudgetService(f.db).RecoverAttempt(f.ctx, execution.ID)
			}()
			close(start)
			wg.Wait()
			require.NoError(t, recoverErr)
			charge = f.db.BudgetCharge.GetX(f.ctx, charge.ID)
			if expired {
				require.Error(t, renewErr)
				require.False(t, active)
				require.NotNil(t, charge.ReservationReleasedAt)
			} else {
				require.NoError(t, renewErr)
				require.True(t, active)
				require.Nil(t, charge.ReservationReleasedAt)
			}
		})
	}
}

func TestBudgetOwnerStartupCleanupPreservesLongRunningRequests(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			req, execution, charge := reserveOwnedBudget(t, f, llm.APIFormatOpenAIResponse)
			require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			if legacy {
				f.db.BudgetCharge.UpdateOneID(charge.ID).ClearOwnerToken().ClearOwnerExpiresAt().ExecX(f.ctx)
			}
			// CreatedAt is immutable in the API; use the fixture driver to model an
			// execution that started hours ago and is still renewing on another instance.
			var result sql.Result
			old := time.Now().Add(-2 * time.Hour)
			require.NoError(t, f.db.Driver().Exec(f.ctx, "UPDATE requests SET created_at = ? WHERE id = ?", []any{old, req.ID}, &result))
			require.NoError(t, f.db.Driver().Exec(f.ctx, "UPDATE request_executions SET created_at = ? WHERE id = ?", []any{old, execution.ID}, &result))
			service := &RequestService{AbstractService: &AbstractService{db: f.db}}
			require.NoError(t, service.ClearStaleProcessingOnStartup(f.ctx))
			require.Equal(t, request.StatusProcessing, f.db.Request.GetX(f.ctx, req.ID).Status)
			require.Equal(t, requestexecution.StatusProcessing, f.db.RequestExecution.GetX(f.ctx, execution.ID).Status)
			require.NoError(t, NewBudgetService(f.db).RecoverAttempt(f.ctx, execution.ID))
			require.Nil(t, f.db.BudgetCharge.GetX(f.ctx, charge.ID).ReservationReleasedAt)
		})
	}
}

func TestBudgetOwnerFinalizationChecksPrimaryAuthority(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "35")
	_, execution, charge := reserveOwnedBudget(t, f, llm.APIFormatOpenAIResponse)
	require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
	lost, err := NewBudgetService(f.db).EndOwnedAttempt(f.ctx, execution.ID)
	require.NoError(t, err)
	require.True(t, lost)
	require.Nil(t, f.db.BudgetCharge.GetX(f.ctx, charge.ID).ReservationReleasedAt, "a different instance cannot end a live attempt")
	f.db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerExpiresAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
	for range 2 {
		lost, err = f.service.EndOwnedAttempt(f.ctx, execution.ID)
		require.NoError(t, err)
		require.True(t, lost, "finalization must catch expiration without waiting for a renewal tick")
	}
	charge = f.db.BudgetCharge.GetX(f.ctx, charge.ID)
	require.Equal(t, "", *charge.OwnerToken)
	require.NotNil(t, charge.ReservationReleasedAt)
	for _, account := range f.db.BudgetAccount.Query().AllX(f.ctx) {
		require.Zero(t, account.Reserved)
	}
}
