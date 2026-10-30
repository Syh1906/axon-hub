package backup

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupBackupRestore(t *testing.T) {
	db, service, ctx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	owner, _ := contexts.GetUser(ctx)
	p := db.Project.Create().SetName("source").SaveX(ctx)
	group := db.APIKeyGroup.Create().SetName("recipient").SetProjectID(p.ID).SaveX(ctx)
	db.APIKey.Create().SetName("first").SetKey("fixture-group-key").SetUserID(owner.ID).SetProjectID(p.ID).SetGroupRef(group.ID).SaveX(ctx)
	data, err := service.Backup(ctx, BackupOptions{IncludeAPIKeys: true})
	require.NoError(t, err)
	var backup BackupData
	require.NoError(t, json.Unmarshal(data, &backup))
	require.Len(t, backup.APIKeys, 1)
	require.Equal(t, "recipient", backup.APIKeys[0].GroupName)
	require.Nil(t, backup.APIKeys[0].GroupRef)
	target := db.Project.Create().SetName("target").SaveX(ctx)
	backup.APIKeys[0].ProjectName = target.Name
	backup.APIKeys[0].Key = "fixture-restored-key"
	data, err = json.Marshal(backup)
	require.NoError(t, err)
	require.NoError(t, service.Restore(ctx, data, RestoreOptions{IncludeAPIKeys: true}))
	restored := db.APIKey.Query().Where(apikey.KeyEQ("fixture-restored-key")).OnlyX(ctx)
	require.NotNil(t, restored.GroupRef)
	require.NotEqual(t, group.ID, *restored.GroupRef)
	require.Equal(t, target.ID, db.APIKeyGroup.GetX(ctx, *restored.GroupRef).ProjectID)
	backup.APIKeys[0].GroupName = ""
	data, err = json.Marshal(backup)
	require.NoError(t, err)
	require.NoError(t, service.Restore(ctx, data, RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategyOverwrite}))
	require.Nil(t, db.APIKey.GetX(ctx, restored.ID).GroupRef)
}

func TestAPIKeyGroupStateBackupRestore(t *testing.T) {
	source, service, ctx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, source.Close()) })
	owner, _ := contexts.GetUser(ctx)
	p := source.Project.Create().SetName("group-state").SaveX(ctx)
	group := source.APIKeyGroup.Create().SetName("disabled").SetProjectID(p.ID).SetNote("保留停用边界").SetEnabled(false).SaveX(ctx)
	source.APIKeyGroup.Create().SetName("empty").SetProjectID(p.ID).SetNote("空组也须恢复").SetEnabled(false).SaveX(ctx)
	source.APIKey.Create().SetName("Codex").SetKey("fixture-disabled-group-key").SetStatus(apikey.StatusEnabled).SetUserID(owner.ID).SetProjectID(p.ID).SetGroupRef(group.ID).SaveX(ctx)
	data, err := service.Backup(ctx, BackupOptions{IncludeAPIKeys: true})
	require.NoError(t, err)
	var snapshot BackupData
	require.NoError(t, json.Unmarshal(data, &snapshot))
	require.Len(t, snapshot.APIKeyGroups, 2)
	target, restore, targetCtx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, target.Close()) })
	target.Project.Create().SetName(p.Name).SaveX(targetCtx)
	require.NoError(t, restore.Restore(targetCtx, data, RestoreOptions{IncludeAPIKeys: true}))
	restored := target.APIKeyGroup.Query().Where(apikeygroup.NameEQ(group.Name)).OnlyX(targetCtx)
	require.False(t, restored.Enabled)
	require.Equal(t, group.Note, restored.Note)
	empty := target.APIKeyGroup.Query().Where(apikeygroup.NameEQ("empty")).OnlyX(targetCtx)
	require.False(t, empty.Enabled)
	require.Equal(t, "空组也须恢复", empty.Note)
	key := target.APIKey.Query().Where(apikey.KeyEQ("fixture-disabled-group-key")).OnlyX(targetCtx)
	require.Equal(t, apikey.StatusEnabled, key.Status)
	require.Equal(t, restored.ID, *key.GroupRef)
	// 跳过策略不能因备份里的启用值而解除目标组的停用状态。
	for _, entry := range snapshot.APIKeyGroups {
		entry.Enabled = true
		entry.Note = "更新备注"
	}
	data, err = json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, restore.Restore(targetCtx, data, RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategySkip}))
	require.False(t, target.APIKeyGroup.GetX(targetCtx, restored.ID).Enabled)
	require.Error(t, restore.Restore(targetCtx, data, RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategyError}))
	require.NoError(t, restore.Restore(targetCtx, data, RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategyOverwrite}))
	require.True(t, target.APIKeyGroup.GetX(targetCtx, restored.ID).Enabled)
	require.Equal(t, "更新备注", target.APIKeyGroup.GetX(targetCtx, restored.ID).Note)
	// 正式旧格式没有分组状态，沿用原有按组名恢复的含义。
	snapshot.Version = BackupVersionV6
	snapshot.APIKeyGroups = nil
	snapshot.APIKeys[0].GroupName = "legacy"
	snapshot.APIKeys[0].Key = "fixture-legacy-group-key"
	data, err = json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, restore.Restore(targetCtx, data, RestoreOptions{IncludeAPIKeys: true}))
	require.True(t, target.APIKeyGroup.Query().Where(apikeygroup.NameEQ("legacy")).OnlyX(targetCtx).Enabled)
}
