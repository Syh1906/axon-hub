package biz

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/stretchr/testify/require"
)

type budgetFixture struct {
	ctx     context.Context
	db      *ent.Client
	admin   *APIKeyService
	service *BudgetService
	key     *ent.APIKey
	group   *ent.APIKeyGroup
}

func newBudgetFixture(t *testing.T) budgetFixture {
	t.Helper()
	s, db := setupTestAPIKeyService(t, xcache.Config{Mode: xcache.ModeMemory})
	t.Cleanup(func() { s.Stop(); require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), db))
	user := db.User.Create().SetEmail("budget@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(ctx)
	project := db.Project.Create().SetName("budget-project").SaveX(ctx)
	ctx = contexts.WithProjectID(contexts.WithUser(ctx, user), project.ID)
	group := db.APIKeyGroup.Create().SetProjectID(project.ID).SetName("shared").SaveX(ctx)
	key := db.APIKey.Create().SetProjectID(project.ID).SetUserID(user.ID).SetName("budget-key").SetKey("fixture").SetGroupRef(group.ID).SaveX(ctx)
	return budgetFixture{ctx, db, s, NewBudgetService(db), key, group}
}

func (f budgetFixture) configure(t *testing.T, kind string, id int, limit string) *BudgetView {
	t.Helper()
	view, err := f.admin.Budget(f.ctx, kind, id)
	require.NoError(t, err)
	rules := make([]*BudgetRuleInput, 0, 5)
	for _, r := range initialBudgetRules() {
		rules = append(rules, &BudgetRuleInput{Period: r.Period, Mode: r.Mode, Limit: limit})
	}
	view, err = f.admin.SaveBudget(f.ctx, kind, id, BudgetSettingsInput{Rules: rules, Timezone: "Asia/Shanghai", Revision: view.Revision, OperationID: uuid.NewString()})
	require.NoError(t, err)
	for _, window := range view.Windows {
		require.Equal(t, "Asia/Shanghai", window.Timezone)
	}
	return view
}

func budgetAmount(t *testing.T, value string) budget.Amount {
	t.Helper()
	a, err := budget.ParseAmount(value)
	require.NoError(t, err)
	return a
}

func TestBudgetAtomicAdmissionAndIdempotentSettlement(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "10")
	f.configure(t, "group", f.group.ID, "5")
	estimate := budgetAmount(t, "3")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, estimate, nil))
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, estimate, nil))
	err := f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 2, 2, estimate, nil)
	var denied *BudgetDenied
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "group", denied.Scope)
	require.Equal(t, 1, f.db.BudgetCharge.Query().CountX(f.ctx))
	for _, a := range f.db.BudgetAccount.Query().AllX(f.ctx) {
		require.Equal(t, int64(estimate), a.Reserved)
	}
	require.NoError(t, f.service.MarkSent(f.ctx, 1))
	require.NoError(t, f.service.EndAttempt(f.ctx, 1))
	require.Equal(t, budgetcharge.StatusUnknown, f.db.BudgetCharge.Query().OnlyX(f.ctx).Status)
	cost := budgetAmount(t, "2.12345678")
	_, handled, err := f.service.Settle(f.ctx, 1, &cost, nil)
	require.NoError(t, err)
	require.True(t, handled)
	_, _, err = f.service.Settle(f.ctx, 1, &cost, nil)
	require.NoError(t, err)
	require.NoError(t, f.service.EndAttempt(f.ctx, 1))
	require.Equal(t, 2, f.db.BudgetPosting.Query().CountX(f.ctx))
	for _, kind := range []string{"key", "group"} {
		id := f.key.ID
		if kind == "group" {
			id = f.group.ID
		}
		view, err := f.admin.Budget(f.ctx, kind, id)
		require.NoError(t, err)
		require.Len(t, view.Windows, 5)
		for _, w := range view.Windows {
			require.Equal(t, "2.12345678", w.Settled)
			require.Equal(t, "0.00000000", w.Reserved)
		}
	}
}

func TestBudgetResetDoesNotReleaseInflightOrChargeNewVersion(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "10")
	estimate := budgetAmount(t, "3")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, estimate, nil))
	require.NoError(t, f.service.MarkSent(f.ctx, 1))
	view, err := f.admin.Budget(f.ctx, "key", f.key.ID)
	require.NoError(t, err)
	input := BudgetResetInput{Periods: []budget.Period{budget.Daily, budget.Total}, Revision: view.Revision, OperationID: uuid.NewString()}
	view, err = f.admin.ResetBudget(f.ctx, "key", f.key.ID, input)
	require.NoError(t, err)
	replayed, err := f.admin.ResetBudget(f.ctx, "key", f.key.ID, input)
	require.NoError(t, err)
	require.Equal(t, view.Revision, replayed.Revision)
	for _, w := range view.Windows {
		require.Equal(t, "3.00000000", w.Reserved)
	}
	cost := budgetAmount(t, "2")
	_, _, err = f.service.Settle(f.ctx, 1, &cost, nil)
	require.NoError(t, err)
	view, err = f.admin.Budget(f.ctx, "key", f.key.ID)
	require.NoError(t, err)
	for _, w := range view.Windows {
		expected := "2.00000000"
		if w.Period == budget.Daily || w.Period == budget.Total {
			expected = "0.00000000"
		}
		require.Equal(t, expected, w.Settled)
		require.Equal(t, "0.00000000", w.Reserved)
	}
}

func TestBudgetUnsentReleaseAndExpiryWithoutLimits(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "10")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, budgetAmount(t, "3"), nil))
	require.NoError(t, f.service.EndAttempt(f.ctx, 1))
	require.NoError(t, f.service.EndAttempt(f.ctx, 1))
	for _, a := range f.db.BudgetAccount.Query().AllX(f.ctx) {
		require.Zero(t, a.Reserved)
	}
	f.configure(t, "key", f.key.ID, "0")
	a := f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey)).OnlyX(f.ctx)
	state := a.State
	at := time.Now().Add(-time.Second)
	state.Policy.ExpiresAt = &at
	a.Update().SetState(state).SaveX(f.ctx)
	err := f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 2, 2, 0, nil)
	var denied *BudgetDenied
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "expired", denied.Reason)
}

func TestBudgetFirstActivationDoesNotInheritOldReservation(t *testing.T) {
	for _, firstKind := range []string{"key", "group"} {
		t.Run(firstKind, func(t *testing.T) {
			f := newBudgetFixture(t)
			firstID, nextID := f.key.ID, f.group.ID
			nextKind := "group"
			if firstKind == "group" {
				firstID, nextID = f.group.ID, f.key.ID
				nextKind = "key"
			}
			f.configure(t, firstKind, firstID, "100")
			require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, budgetAmount(t, "90"), nil))
			require.NoError(t, f.service.MarkSent(f.ctx, 1))
			view := f.configure(t, nextKind, nextID, "10")
			for _, w := range view.Windows {
				require.Equal(t, "0.00000000", w.Reserved)
			}
			require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 2, 2, budgetAmount(t, "1"), nil))
			cost := budgetAmount(t, "80")
			_, _, err := f.service.Settle(f.ctx, 1, &cost, nil)
			require.NoError(t, err)
			view, err = f.admin.Budget(f.ctx, nextKind, nextID)
			require.NoError(t, err)
			for _, w := range view.Windows {
				require.Equal(t, "1.00000000", w.Reserved)
				require.Equal(t, "0.00000000", w.Settled)
			}
			require.NoError(t, f.service.EndAttempt(f.ctx, 2))
		})
	}
}
func TestBudgetRequiresWriteScope(t *testing.T) {
	f := newBudgetFixture(t)
	member := f.db.User.Create().SetEmail("read-budget@example.test").SetPassword("fixture").SetScopes([]string{"write_users", "write_roles", "read_api_keys"}).SaveX(f.ctx)
	ctx := contexts.WithProjectID(contexts.WithUser(ent.NewContext(context.Background(), f.db), member), f.key.ProjectID)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &member.ID})
	require.NoError(t, err)
	_, err = f.admin.SaveBudget(ctx, "key", f.key.ID, BudgetSettingsInput{})
	require.ErrorContains(t, err, "write_api_keys")
	_, err = f.admin.ResetBudget(ctx, "key", f.key.ID, BudgetResetInput{})
	require.ErrorContains(t, err, "write_api_keys")
}
func TestBudgetConcurrentSharedAdmission(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "group", f.group.ID, "10")
	accepted := make(chan int, 100)
	failures := make(chan error, 100)
	var workers sync.WaitGroup
	for id := 1; id <= 100; id++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			defer func() {
				if value := recover(); value != nil {
					failures <- fmt.Errorf("worker panic: %v", value)
				}
			}()
			err := f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, id, id, budget.Amount(100000000), nil)
			if err == nil {
				accepted <- id
			} else if !IsBudgetDenied(err) {
				failures <- err
			}
		}(id)
	}
	workers.Wait()
	close(accepted)
	close(failures)
	require.Len(t, accepted, 10)
	for err := range failures {
		require.NoError(t, err)
	}
	view, err := f.admin.Budget(f.ctx, "group", f.group.ID)
	require.NoError(t, err)
	for _, w := range view.Windows {
		require.Equal(t, "10.00000000", w.Reserved)
	}
}
func TestBudgetRestoreCalendarPreservesUsageUntilBoundary(t *testing.T) {
	f := newBudgetFixture(t)
	view := f.configure(t, "key", f.key.ID, "10")
	view, err := f.admin.ResetBudget(f.ctx, "key", f.key.ID, BudgetResetInput{Periods: []budget.Period{budget.Daily, budget.Weekly, budget.Monthly}, ResetCycle: true, Revision: view.Revision, OperationID: uuid.NewString()})
	require.NoError(t, err)
	rules := make([]*BudgetRuleInput, 0, 5)
	for _, w := range view.Windows {
		rules = append(rules, &BudgetRuleInput{Period: w.Period, Mode: w.Mode, Minute: w.Minute, Limit: w.Limit, RestoreCalendar: true})
	}
	view, err = f.admin.SaveBudget(f.ctx, "key", f.key.ID, BudgetSettingsInput{Rules: rules, Timezone: view.Timezone, Revision: view.Revision, OperationID: uuid.NewString()})
	require.NoError(t, err)
	for _, w := range view.Windows {
		if w.Period == budget.Daily || w.Period == budget.Weekly || w.Period == budget.Monthly {
			require.NotNil(t, w.PendingAt)
			require.NotEmpty(t, w.EffectiveSchedule)
		}
	}
}

func TestBudgetInitialTimezoneUsesSystemAndKeepsSavedCalendar(t *testing.T) {
	f := newBudgetFixture(t)
	system := f.admin.SystemService
	require.NoError(t, system.SetGeneralSettings(f.ctx, SystemGeneralSettings{Timezone: "Asia/Shanghai"}))
	// Admission may have already created empty accounts before any limit is set.
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, 0, nil))
	view, err := f.admin.Budget(f.ctx, "key", f.key.ID)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", view.Timezone)
	for _, w := range view.Windows {
		require.Equal(t, "Asia/Shanghai", w.Timezone)
	}
	rules := make([]*BudgetRuleInput, 0, len(view.Windows))
	for _, w := range view.Windows {
		rules = append(rules, &BudgetRuleInput{Period: w.Period, Mode: w.Mode, Minute: w.Minute, Limit: "1"})
	}
	saved, err := f.admin.SaveBudget(f.ctx, "key", f.key.ID, BudgetSettingsInput{Rules: rules, Timezone: view.Timezone, Revision: view.Revision, OperationID: uuid.NewString()})
	require.NoError(t, err)
	for _, w := range saved.Windows {
		if w.Period == budget.Daily {
			require.Equal(t, 16, w.WindowEnd.UTC().Hour())
		}
	}
	require.NoError(t, system.SetGeneralSettings(f.ctx, SystemGeneralSettings{Timezone: "America/New_York"}))
	view, err = f.admin.Budget(f.ctx, "key", f.key.ID)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", view.Timezone)
	shared, err := f.admin.Budget(f.ctx, "group", f.group.ID)
	require.NoError(t, err)
	require.Equal(t, "America/New_York", shared.Timezone)
	preview, err := f.admin.PreviewBudgetReset(f.ctx, "group", f.group.ID, BudgetResetInput{Periods: []budget.Period{budget.Daily}, Revision: shared.Revision, OperationID: uuid.NewString()})
	require.NoError(t, err)
	require.Equal(t, "America/New_York", preview.Timezone)
	reset, err := f.admin.ResetBudget(f.ctx, "group", f.group.ID, BudgetResetInput{Periods: []budget.Period{budget.Daily}, Revision: shared.Revision, OperationID: uuid.NewString()})
	require.NoError(t, err)
	require.Equal(t, "America/New_York", reset.Timezone)
}

func TestBudgetMixedKeyTypesUseTheSameGroupLimit(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "group", f.group.ID, "1")
	amount := budgetAmount(t, "0.4")
	for index, kind := range []apikey.Type{apikey.TypeUser, apikey.TypePersonal, apikey.TypeServiceAccount} {
		key, err := f.admin.CreateAPIKey(f.ctx, ent.CreateAPIKeyInput{Name: "mixed-" + string(kind), ProjectID: f.key.ProjectID, Type: &kind, GroupRef: &f.group.ID})
		require.NoError(t, err)
		require.Equal(t, f.group.ID, *key.GroupRef)
		err = f.service.Reserve(f.ctx, key.ProjectID, key.ID, 100+index, 100+index, amount, nil)
		if index < 2 {
			require.NoError(t, err)
			require.NoError(t, f.service.MarkSent(f.ctx, 100+index))
			_, _, err = f.service.Settle(f.ctx, 100+index, &amount, nil)
			require.NoError(t, err)
		} else {
			var denied *BudgetDenied
			require.ErrorAs(t, err, &denied)
			require.Equal(t, "group", denied.Scope)
		}
	}
	view, err := f.admin.Budget(f.ctx, "group", f.group.ID)
	require.NoError(t, err)
	require.Equal(t, "0.80000000", view.Windows[0].Settled)
}
