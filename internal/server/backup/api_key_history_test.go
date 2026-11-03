package backup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestDeletedAPIKeyBackupKeepsHistoryAndDeletion(t *testing.T) {
	db, svc, ctx := setupBackupTest(t)
	owner, _ := contexts.GetUser(ctx)
	project := db.Project.Create().SetName("history-project").SaveX(ctx)
	group := db.APIKeyGroup.Create().SetName("history-group").SetProjectID(project.ID).SaveX(ctx)
	key := db.APIKey.Create().SetName("deleted-key").SetKey(biz.DeletedAPIKeyPrefix + "123:api_key:v3:456").SetDeletedAt(1).SetStatus(apikey.StatusDisabled).SetGroupRef(group.ID).SetProjectID(project.ID).SetUserID(owner.ID).SaveX(ctx)
	// Legacy soft-deleted rows still contain their secrets and must stay excluded.
	legacy := db.APIKey.Create().SetName("legacy").SetKey("legacy-secret").SetProjectID(project.ID).SaveX(ctx)
	require.NoError(t, db.APIKey.DeleteOne(legacy).Exec(ctx))
	req := db.Request.Create().SetAPIKeyID(key.ID).SetProjectID(project.ID).SetModelID("fixture").SetStatus(request.StatusCompleted).SetRequestBody([]byte(`{}`)).SaveX(ctx)
	db.UsageLog.Create().SetAPIKeyID(key.ID).SetRequestID(req.ID).SetProjectID(project.ID).SetModelID("fixture").SetTotalTokens(12).SetTotalCost(1.25).SaveX(ctx)
	state := budget.NewState()
	state.Policy.StartedAt = time.Now().UTC()
	state.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 1000000000}}
	db.BudgetAccount.Create().SetProjectID(project.ID).SetOwnerKind(budgetaccount.OwnerKindKey).SetOwnerID(key.ID).SetState(state).SaveX(ctx)
	payload, err := svc.Backup(ctx, BackupOptions{IncludeProjects: true, IncludeAPIKeys: true, IncludeRequestLogs: true, IncludeUsageStats: true})
	require.NoError(t, err)
	var snapshot BackupData
	require.NoError(t, json.Unmarshal(payload, &snapshot))
	require.Len(t, snapshot.APIKeys, 1)
	require.Equal(t, 1, snapshot.APIKeys[0].DeletedAt)
	require.NotContains(t, string(payload), "legacy-secret")
	require.Equal(t, key.Key, snapshot.UsageRequests[0].APIKeyKey)
	require.Equal(t, key.Key, snapshot.UsageLogs[0].APIKeyKey)
	require.Equal(t, key.Key, snapshot.BudgetAccounts[0].OwnerKey)
	require.NoError(t, db.Close())

	target, restore, targetCtx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, target.Close()) })
	require.NoError(t, restore.Restore(targetCtx, payload, RestoreOptions{IncludeProjects: true, IncludeAPIKeys: true, IncludeRequestLogs: true, IncludeUsageStats: true}))
	require.Zero(t, target.APIKey.Query().CountX(targetCtx))
	deleted := target.APIKey.Query().Where(apikey.KeyEQ(key.Key)).OnlyX(schematype.SkipSoftDelete(targetCtx))
	require.Equal(t, 1, deleted.DeletedAt)
	require.Equal(t, apikey.StatusDisabled, deleted.Status)
	require.Equal(t, deleted.ID, target.Request.Query().OnlyX(targetCtx).APIKeyID)
	require.Equal(t, deleted.ID, target.UsageLog.Query().OnlyX(targetCtx).APIKeyID)
	require.Equal(t, deleted.ID, target.BudgetAccount.Query().OnlyX(targetCtx).OwnerID)
	_, err = deleted.Update().SetStatus(apikey.StatusEnabled).Save(targetCtx)
	require.Error(t, err, "restoration must not make a deletion record editable")
}
