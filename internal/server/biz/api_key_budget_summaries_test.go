package biz

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

func TestKeyBudgetSummariesBatchMatchesSingleOwnerBalances(t *testing.T) {
	f := newBudgetFixture(t)
	ids := []int{f.key.ID}
	for i := 1; i < 10; i++ {
		key := f.db.APIKey.Create().SetProjectID(f.key.ProjectID).SetUserID(f.key.UserID).
			SetName(fmt.Sprintf("member-%d", i)).SetKey(fmt.Sprintf("fixture-member-%d", i)).SetGroupRef(f.group.ID).SaveX(f.ctx)
		ids = append(ids, key.ID)
	}
	for i, id := range ids {
		f.configure(t, "key", id, "10")
		cost := budgetAmount(t, "2")
		require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, id, 100+i, 100+i, cost, nil))
		_, _, err := f.service.Settle(f.ctx, 100+i, &cost, nil)
		require.NoError(t, err)
	}
	queries := 0
	debug := ent.NewClient(ent.Driver(f.db.Driver()), ent.Debug(), ent.Log(func(args ...any) {
		line := fmt.Sprint(args...)
		if strings.Contains(line, "SELECT") && (strings.Contains(line, "budget_accounts") || strings.Contains(line, "budget_postings") || strings.Contains(line, "api_keys")) {
			queries++
		}
	}))
	admin := *f.admin
	admin.AbstractService = &AbstractService{db: debug}
	readCtx := ent.NewContext(f.ctx, debug)
	expected := map[int]*BudgetView{}
	for _, id := range ids {
		view, err := admin.Budget(readCtx, "key", id)
		require.NoError(t, err)
		expected[id] = view
	}
	before := queries
	queries = 0
	views, err := admin.KeyBudgetSummaries(readCtx, ids, false)
	require.NoError(t, err)
	require.Len(t, views, len(ids))
	require.Positive(t, queries, "the counter must observe the actual transaction client")
	require.LessOrEqual(t, queries, 3, "one authorization scan, one account scan and one ledger aggregate for this page")
	require.Less(t, queries, before)
	t.Logf("10 member balances: individual reads=%d, batch reads=%d", before, queries)
	for _, view := range views {
		want := expected[view.OwnerID]
		require.Equal(t, want.Revision, view.Revision)
		require.Equal(t, want.Timezone, view.Timezone)
		require.Equal(t, want.HasLimits, view.HasLimits)
		require.Equal(t, want.Expired, view.Expired)
		require.Len(t, view.Windows, len(want.Windows))
		for i, window := range view.Windows {
			require.Equal(t, want.Windows[i].Limit, window.Limit)
			require.Equal(t, want.Windows[i].Settled, window.Settled)
			require.Equal(t, want.Windows[i].Reserved, window.Reserved)
			require.Equal(t, want.Windows[i].Available, window.Available)
		}
	}
	other := f.db.Project.Create().SetName("other").SaveX(f.ctx)
	foreign := f.db.APIKey.Create().SetProjectID(other.ID).SetUserID(f.key.UserID).SetName("foreign").SetKey("fixture-foreign").SaveX(f.ctx)
	_, err = admin.KeyBudgetSummaries(f.ctx, []int{f.key.ID, foreign.ID}, true)
	require.True(t, ent.IsNotFound(err), "a batch must not cross the active project")
}

func TestKeyBudgetSummariesIncludeSharedGroupOnce(t *testing.T) {
	f := newBudgetFixture(t)
	other := f.db.APIKey.Create().SetProjectID(f.key.ProjectID).SetUserID(f.key.UserID).
		SetName("second").SetKey("fixture-second").SetGroupRef(f.group.ID).SaveX(f.ctx)
	f.configure(t, "group", f.group.ID, "5")
	f.configure(t, "key", f.key.ID, "10")
	cost := budgetAmount(t, "2")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 300, 300, cost, nil))
	_, _, err := f.service.Settle(f.ctx, 300, &cost, nil)
	require.NoError(t, err)
	views, err := f.admin.KeyBudgetSummaries(f.ctx, []int{f.key.ID, other.ID, f.key.ID}, true)
	require.NoError(t, err)
	require.Len(t, views, 3, "two unique keys and one shared group")
	require.Equal(t, "key", views[0].OwnerKind)
	require.Equal(t, f.key.ID, views[0].OwnerID)
	require.Equal(t, "key", views[1].OwnerKind)
	require.Equal(t, "group", views[2].OwnerKind)
	require.Equal(t, f.group.ID, views[2].OwnerID)
	for _, window := range views[2].Windows {
		require.Equal(t, "3.00000000", window.Available)
	}
	tx, err := f.db.Tx(f.ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	transactionViews, err := f.admin.KeyBudgetSummaries(ent.NewContext(f.ctx, tx.Client()), []int{f.key.ID, other.ID}, true)
	require.NoError(t, err, "existing transaction clients must be reused")
	require.Len(t, transactionViews, 3)
	for _, window := range transactionViews[2].Windows {
		require.Equal(t, "3.00000000", window.Available)
	}
}

func TestKeyBudgetSummariesKeepOneSettlementSnapshot(t *testing.T) {
	// WAL permits a writer to commit while a read transaction is open. Prepare
	// the real settlement before the summary starts, then commit it exactly
	// between the account and rolling-posting reads, without timing sleeps.
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "budget-summary.db")) + "?_fk=1&_pragma=journal_mode(WAL)&_pragma=busy_timeout(1000)"
	writer := enttest.NewEntClient(t, "sqlite3", dsn)
	t.Cleanup(func() { require.NoError(t, writer.Close()) })
	ctx := authz.WithTestBypass(t.Context())
	owner := writer.User.Create().SetEmail("summary@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(ctx)
	project := writer.Project.Create().SetName("summary").SaveX(ctx)
	ctx = contexts.WithProjectID(contexts.WithUser(ctx, owner), project.ID)
	group := writer.APIKeyGroup.Create().SetProjectID(project.ID).SetName("shared").SaveX(ctx)
	key := writer.APIKey.Create().SetProjectID(project.ID).SetUserID(owner.ID).SetName("member").SetKey("fixture").SetGroupRef(group.ID).SaveX(ctx)
	admin := &APIKeyService{AbstractService: &AbstractService{db: writer}, SystemService: NewSystemService(SystemServiceParams{Ent: writer})}
	f := budgetFixture{ctx: ctx, db: writer, admin: admin, service: NewBudgetService(writer), key: key, group: group}
	f.configure(t, "key", key.ID, "10")
	f.configure(t, "group", group.ID, "10")
	cost := budgetAmount(t, "2")
	require.NoError(t, f.service.Reserve(ctx, project.ID, key.ID, 700, 700, cost, nil))
	settlement, err := writer.Tx(ctx)
	require.NoError(t, err)
	defer settlement.Rollback()
	_, handled, err := f.service.Settle(ent.NewTxContext(ctx, settlement), 700, &cost, nil)
	require.NoError(t, err)
	require.True(t, handled)
	reader, err := ent.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	committed := false
	reader.BudgetAccount.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err == nil && !committed {
				require.NoError(t, settlement.Commit())
				committed = true
			}
			return value, err
		})
	}))
	views, err := admin.KeyBudgetSummaries(ent.NewContext(ctx, reader), []int{key.ID}, true)
	require.NoError(t, err)
	require.True(t, committed, "settlement must commit between the two summary reads")
	require.Len(t, views, 2)
	for _, view := range views {
		for _, window := range view.Windows {
			if window.Period == budget.FiveHour {
				require.Equal(t, "8.00000000", window.Available, "%s must not combine the old $2 hold with the new $2 settlement", view.OwnerKind)
			}
		}
	}
	// A later read sees the settlement and released hold, with the same balance.
	views, err = admin.KeyBudgetSummaries(ent.NewContext(ctx, reader), []int{key.ID}, true)
	require.NoError(t, err)
	for _, view := range views {
		for _, window := range view.Windows {
			if window.Period == budget.FiveHour {
				require.Equal(t, "0.00000000", window.Reserved)
				require.Equal(t, "2.00000000", window.Settled)
				require.Equal(t, "8.00000000", window.Available)
			}
		}
	}
}
