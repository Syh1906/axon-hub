package biz

import (
	"database/sql"
	"github.com/stretchr/testify/require"
	"testing"
)

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
