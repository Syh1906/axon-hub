package backup

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/apikey"
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
