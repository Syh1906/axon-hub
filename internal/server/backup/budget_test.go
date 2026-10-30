package backup

import (
	"encoding/json"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/shopspring/decimal"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
)

func TestBudgetBackupRestoresBalancesAndDetachedHistory(t *testing.T) {
	for _, detached := range []bool{false, true} {
		name := "live"
		if detached {
			name = "deleted-project"
		}
		t.Run(name, func(t *testing.T) {
			db, svc, ctx := setupBackupTest(t)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			owner, _ := contexts.GetUser(ctx)
			project := db.Project.Create().SetName("source-budget").SaveX(ctx)
			group := db.APIKeyGroup.Create().SetProjectID(project.ID).SetName("budget-group").SaveX(ctx)
			key := db.APIKey.Create().SetGroupRef(group.ID).SetName("key").SetKey("fixture-budget").SetUserID(owner.ID).SetProjectID(project.ID).SaveX(ctx)
			state := budget.NewState()
			now := time.Now().UTC()
			state.Policy.StartedAt = now.Add(-time.Hour)
			state.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 1000000000}}
			db.BudgetAccount.Create().SetProjectID(project.ID).SetOwnerKind(budgetaccount.OwnerKindGroup).SetOwnerID(group.ID).SetState(state).SaveX(ctx)
			account := db.BudgetAccount.Create().SetProjectID(project.ID).SetOwnerKind(budgetaccount.OwnerKindKey).SetOwnerID(key.ID).SetState(state).SaveX(ctx)
			accounting := biz.NewBudgetService(db)
			require.NoError(t, accounting.Reserve(ctx, project.ID, key.ID, 100, 100, 300000000, nil))
			require.NoError(t, accounting.MarkSent(ctx, 100))
			cost := budget.Amount(200000000)
			_, _, err := accounting.Settle(ctx, 100, &cost, nil)
			require.NoError(t, err)
			require.NoError(t, accounting.Reserve(ctx, project.ID, key.ID, 101, 101, 300000000, nil))
			require.NoError(t, accounting.MarkSent(ctx, 101))
			require.NoError(t, accounting.RecordUsage(ctx, 101, nil, nil, &cost))
			if detached {
				require.NoError(t, db.APIKey.DeleteOneID(key.ID).Exec(ctx))
				require.NoError(t, db.Project.DeleteOneID(project.ID).Exec(ctx))
			}
			bytes, err := svc.Backup(ctx, BackupOptions{IncludeAPIKeys: true})
			require.NoError(t, err)
			var snapshot BackupData
			require.NoError(t, json.Unmarshal(bytes, &snapshot))
			require.Len(t, snapshot.BudgetAccounts, 2)
			require.Len(t, snapshot.BudgetCharges, 2)
			require.Len(t, snapshot.BudgetPostings, 2)
			if detached {
				require.Empty(t, snapshot.BudgetAccounts[0].ProjectName)
			} else {
				target := db.Project.Create().SetName("target-budget").SaveX(ctx)
				snapshot.APIKeys[0].ProjectName = target.Name
				snapshot.APIKeys[0].Key = "fixture-budget-copy"
				for _, a := range snapshot.BudgetAccounts {
					a.ProjectName = target.Name
					if a.OwnerKind == budgetaccount.OwnerKindKey {
						a.OwnerKey = "fixture-budget-copy"
					}
				}
			}
			bytes, err = json.Marshal(snapshot)
			require.NoError(t, err)
			require.NoError(t, svc.Restore(ctx, bytes, RestoreOptions{IncludeAPIKeys: true}))
			restored := db.BudgetAccount.Query().Where(budgetaccount.IDNEQ(account.ID), budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey)).OnlyX(ctx)
			require.Equal(t, int64(300000000), restored.Reserved)
			require.Equal(t, budget.Amount(200000000), restored.State.Counters[budget.Total].Amount)
			pending := db.BudgetCharge.Query().Where(budgetcharge.KeyAccountIDEQ(restored.ID), budgetcharge.StatusEQ(budgetcharge.StatusUnknown)).OnlyX(ctx)
			require.Negative(t, pending.ExecutionID)
			require.Negative(t, pending.RequestID)
			if detached {
				require.Negative(t, restored.ProjectID)
				require.Negative(t, restored.OwnerID)
			}
			// The recovery path must settle the detached account, not create another one.
			logs := biz.NewUsageLogService(db, nil, nil)
			db.BudgetCharge.UpdateOneID(pending.ID).SetCheckAfter(now.Add(-time.Minute)).ExecX(ctx)
			require.NoError(t, logs.RecoverBudgets(ctx, nil))
			require.NoError(t, logs.RecoverBudgets(ctx, nil))
			restored = db.BudgetAccount.GetX(ctx, restored.ID)
			require.Zero(t, restored.Reserved)
			require.Equal(t, budget.Amount(400000000), restored.State.Counters[budget.Total].Amount)
			require.Equal(t, 4, db.BudgetCharge.Query().CountX(ctx))
			if !detached {
				count := db.BudgetAccount.Query().CountX(ctx)
				require.ErrorContains(t, svc.Restore(ctx, bytes, RestoreOptions{IncludeAPIKeys: true}), "budget history already exists")
				require.Equal(t, count, db.BudgetAccount.Query().CountX(ctx))
			}
			snapshot.BudgetAccounts[0].Reserved++
			bytes, err = json.Marshal(snapshot)
			require.NoError(t, err)
			require.ErrorContains(t, svc.Restore(ctx, bytes, RestoreOptions{IncludeAPIKeys: true}), "inconsistent")
		})
	}
}

func TestBudgetBackupPendingVideoRecoversWithoutRequestLogs(t *testing.T) {
	db, svc, ctx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	completed := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		status := "queued"
		if completed {
			status = "completed"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "task-budget", "object": "video", "status": status})
	}))
	t.Cleanup(server.Close)
	ch := createBackupTestChannel(t, db, ctx, "video-budget", channel.TypeOpenai)
	ch = ch.Update().SetBaseURL(server.URL).SaveX(ctx)
	owner, _ := contexts.GetUser(ctx)
	p := db.Project.Create().SetName("video-project").SaveX(ctx)
	key := db.APIKey.Create().SetName("video-key").SetKey("fixture-video").SetProjectID(p.ID).SetUserID(owner.ID).SaveX(ctx)
	state := budget.NewState()
	state.Policy.StartedAt = time.Now().UTC().Add(-time.Hour)
	state.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 1000000000}}
	db.BudgetAccount.Create().SetProjectID(p.ID).SetOwnerKind(budgetaccount.OwnerKindKey).SetOwnerID(key.ID).SetState(state).SaveX(ctx)
	accounting := biz.NewBudgetService(db)
	require.NoError(t, accounting.Reserve(ctx, p.ID, key.ID, 100, 100, 300000000, nil))
	require.NoError(t, accounting.MarkSent(ctx, 100))
	fee := decimal.NewFromInt(2)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &fee}}}}
	snapshot := &objects.RequestCostSnapshot{Price: &price, PriceReferenceID: "historical-video-price", PricedAt: time.Now(), ManualMultiplier: decimal.NewFromInt(1), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	require.NoError(t, accounting.RecordAsyncTask(ctx, &ent.RequestExecution{ID: 100, ChannelID: ch.ID, Format: llm.APIFormatOpenAIVideo.String(), CostSnapshot: snapshot}, "task-budget"))
	data, err := svc.Backup(ctx, BackupOptions{IncludeAPIKeys: true})
	require.NoError(t, err)
	var backup BackupData
	require.NoError(t, json.Unmarshal(data, &backup))
	require.NotNil(t, backup.BudgetCharges[0].AsyncTask.Price)
	target := db.Project.Create().SetName("video-restored").SaveX(ctx)
	backup.APIKeys[0].Key = "fixture-video-restored"
	backup.APIKeys[0].ProjectName = target.Name
	backup.BudgetAccounts[0].OwnerKey = backup.APIKeys[0].Key
	backup.BudgetAccounts[0].ProjectName = target.Name
	data, err = json.Marshal(backup)
	require.NoError(t, err)
	require.NoError(t, svc.Restore(ctx, data, RestoreOptions{IncludeAPIKeys: true}))
	charge := db.BudgetCharge.Query().Where(budgetcharge.ExecutionIDLT(0)).OnlyX(ctx)
	require.NotNil(t, charge.AsyncTask)
	require.NotNil(t, charge.CostSnapshot)
	require.Nil(t, charge.Actual)
	channels := biz.NewChannelServiceForTest(db)
	logs := biz.NewUsageLogService(db, nil, channels)
	video := biz.NewVideoService(channels, nil)
	run := func() {
		db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
		require.NoError(t, logs.RecoverBudgets(ctx, video))
	}
	run()
	require.Equal(t, int64(300000000), db.BudgetAccount.GetX(ctx, charge.KeyAccountID).Reserved)
	require.Equal(t, 1, calls)
	completed = true
	run()
	run()
	require.Equal(t, 2, calls)
	charge = db.BudgetCharge.GetX(ctx, charge.ID)
	require.Equal(t, budgetcharge.StatusSettled, charge.Status)
	require.Equal(t, int64(200000000), *charge.Actual)
	require.Zero(t, db.BudgetAccount.GetX(ctx, charge.KeyAccountID).Reserved)
	require.Equal(t, 1, db.BudgetPosting.Query().CountX(ctx))
	require.Zero(t, db.Request.Query().CountX(ctx))
}

func TestBudgetSettledVideoRestoreDoesNotRequireChannel(t *testing.T) {
	db, svc, ctx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	p := db.Project.Create().SetName("settled-video").SaveX(ctx)
	owner, _ := contexts.GetUser(ctx)
	key := db.APIKey.Create().SetName("settled").SetKey("fixture-settled-video").SetProjectID(p.ID).SetUserID(owner.ID).SaveX(ctx)
	state := budget.NewState()
	state.Policy.StartedAt = time.Now().UTC()
	state.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 1000000000}}
	state.Counters[budget.Total] = budget.Counter{Amount: 200000000}
	actual := int64(200000000)
	data := BackupData{Version: BackupVersion,
		BudgetAccounts: []*BackupBudgetAccount{{BudgetAccount: &ent.BudgetAccount{ID: 71, ProjectID: 72, OwnerID: 73, OwnerKind: budgetaccount.OwnerKindKey, State: state}, ProjectName: p.Name, OwnerKey: key.Key}},
		BudgetCharges:  []*ent.BudgetCharge{{ID: 74, ProjectID: 72, KeyAccountID: 71, KeyID: 73, ExecutionID: 75, RequestID: 76, Estimate: 300000000, Actual: &actual, Status: budgetcharge.StatusSettled, KeyVersions: map[budget.Period]int64{}, GroupVersions: map[budget.Period]int64{}, AsyncTask: &objects.BudgetAsyncTask{ChannelID: 77, ChannelName: "missing-old-channel", ExternalID: "old-task", Format: llm.APIFormatOpenAIVideo.String()}}},
		BudgetPostings: []*ent.BudgetPosting{{AccountID: 71, ChargeID: 74, Amount: actual, SettledAt: time.Now().UTC()}},
	}
	bytes, err := json.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, svc.Restore(ctx, bytes, RestoreOptions{IncludeAPIKeys: true}))
	restored := db.BudgetAccount.Query().OnlyX(ctx)
	require.Zero(t, restored.Reserved)
	require.Equal(t, budget.Amount(actual), restored.State.Counters[budget.Total].Amount)
	require.Zero(t, db.BudgetCharge.Query().OnlyX(ctx).AsyncTask.ChannelID)
}
