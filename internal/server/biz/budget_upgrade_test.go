package biz

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBudgetHoldReleaseColumnUpgradePreservesUnsettledCharge(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "35")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, budgetAmount(t, "30"), nil))
	require.NoError(t, f.service.MarkSent(f.ctx, 1))
	before := f.db.BudgetCharge.Query().OnlyX(f.ctx)
	var result sql.Result
	require.NoError(t, f.db.Driver().Exec(f.ctx, "ALTER TABLE budget_charges DROP COLUMN reservation_released_at", []any{}, &result))
	require.NoError(t, f.db.Schema.Create(f.ctx))
	after := f.db.BudgetCharge.GetX(f.ctx, before.ID)
	require.Equal(t, before.Estimate, after.Estimate)
	require.Equal(t, before.Status, after.Status)
	require.Nil(t, after.ReservationReleasedAt)
	require.NoError(t, f.service.EndAttempt(f.ctx, after.ExecutionID))
	require.WithinDuration(t, time.Now(), *f.db.BudgetCharge.GetX(f.ctx, after.ID).ReservationReleasedAt, time.Minute)
}

func TestBudgetSchemaUpgradePreservesExistingKeys(t *testing.T) {
	f := newBudgetFixture(t)
	before := f.db.APIKey.GetX(f.ctx, f.key.ID)
	// Model the preceding schema: the four new independent budget tables are absent.
	for _, table := range []string{"budget_changes", "budget_postings", "budget_charges", "budget_accounts"} {
		var result sql.Result
		require.NoError(t, f.db.Driver().Exec(f.ctx, "DROP TABLE "+table, []any{}, &result))
	}
	require.NoError(t, f.db.Schema.Create(f.ctx))
	after := f.db.APIKey.GetX(f.ctx, f.key.ID)
	require.Equal(t, before.Key, after.Key)
	require.Equal(t, before.GroupRef, after.GroupRef)
	require.Equal(t, before.Profiles, after.Profiles)
	require.Zero(t, f.db.BudgetAccount.Query().CountX(f.ctx))
	f.configure(t, "key", f.key.ID, "10")
	require.Equal(t, 1, f.db.BudgetAccount.Query().CountX(f.ctx))
	require.NoError(t, f.db.Schema.Create(f.ctx))
	require.Equal(t, 1, f.db.BudgetAccount.Query().CountX(f.ctx))
}

func TestBudgetOwnerColumnsUpgradePreservesLegacyUnknownHold(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "35")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, budgetAmount(t, "7"), nil))
	require.NoError(t, f.service.MarkSent(f.ctx, 1))
	before := f.db.BudgetCharge.Query().OnlyX(f.ctx)
	for _, column := range []string{"owner_token", "owner_expires_at"} {
		var result sql.Result
		require.NoError(t, f.db.Driver().Exec(f.ctx, "ALTER TABLE budget_charges DROP COLUMN "+column, []any{}, &result))
	}
	for range 2 {
		require.NoError(t, f.db.Schema.Create(f.ctx))
	}
	after := f.db.BudgetCharge.GetX(f.ctx, before.ID)
	require.Nil(t, after.OwnerToken)
	require.Nil(t, after.OwnerExpiresAt)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Estimate, after.Estimate)
	require.NoError(t, NewBudgetService(f.db).RecoverAttempt(f.ctx, after.ExecutionID))
	require.Nil(t, f.db.BudgetCharge.GetX(f.ctx, before.ID).ReservationReleasedAt)
}
