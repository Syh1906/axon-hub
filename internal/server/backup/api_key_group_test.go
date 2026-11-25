package backup

import (
	"encoding/json"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"testing"
	"time"

	bytesreader "bytes"
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
	require.NoError(t, service.Restore(ctx, bytesreader.NewReader(data), RestoreOptions{IncludeAPIKeys: true}))
	restored := db.APIKey.Query().Where(apikey.KeyEQ("fixture-restored-key")).OnlyX(ctx)
	require.NotNil(t, restored.GroupRef)
	require.NotEqual(t, group.ID, *restored.GroupRef)
	require.Equal(t, target.ID, db.APIKeyGroup.GetX(ctx, *restored.GroupRef).ProjectID)
	backup.APIKeys[0].GroupName = ""
	data, err = json.Marshal(backup)
	require.NoError(t, err)
	require.NoError(t, service.Restore(ctx, bytesreader.NewReader(data), RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategyOverwrite}))
	require.Nil(t, db.APIKey.GetX(ctx, restored.ID).GroupRef)
}

func TestAPIKeyGroupStateBackupRestore(t *testing.T) {
	source, service, ctx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, source.Close()) })
	owner, _ := contexts.GetUser(ctx)
	p := source.Project.Create().SetName("group-state").SaveX(ctx)
	group := source.APIKeyGroup.Create().SetName("disabled").SetProjectID(p.ID).SetNote("保留停用边界").SetEnabled(false).SetShowChannelInSelfUsage(true).SaveX(ctx)
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
	require.NoError(t, restore.Restore(targetCtx, bytesreader.NewReader(data), RestoreOptions{IncludeAPIKeys: true}))
	restored := target.APIKeyGroup.Query().Where(apikeygroup.NameEQ(group.Name)).OnlyX(targetCtx)
	require.False(t, restored.Enabled)
	require.True(t, restored.ShowChannelInSelfUsage)
	require.Equal(t, group.Note, restored.Note)
	empty := target.APIKeyGroup.Query().Where(apikeygroup.NameEQ("empty")).OnlyX(targetCtx)
	require.False(t, empty.Enabled)
	require.False(t, empty.ShowChannelInSelfUsage)
	require.Equal(t, "空组也须恢复", empty.Note)
	key := target.APIKey.Query().Where(apikey.KeyEQ("fixture-disabled-group-key")).OnlyX(targetCtx)
	require.Equal(t, apikey.StatusEnabled, key.Status)
	require.Equal(t, restored.ID, *key.GroupRef)
	// 跳过策略不能因备份里的启用值而解除目标组的停用状态。
	for _, entry := range snapshot.APIKeyGroups {
		entry.Enabled = true
		entry.ShowChannelInSelfUsage = false
		entry.Note = "更新备注"
	}
	data, err = json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, restore.Restore(targetCtx, bytesreader.NewReader(data), RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategySkip}))
	require.False(t, target.APIKeyGroup.GetX(targetCtx, restored.ID).Enabled)
	require.True(t, target.APIKeyGroup.GetX(targetCtx, restored.ID).ShowChannelInSelfUsage)
	require.Error(t, restore.Restore(targetCtx, bytesreader.NewReader(data), RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategyError}))
	require.NoError(t, restore.Restore(targetCtx, bytesreader.NewReader(data), RestoreOptions{IncludeAPIKeys: true, APIKeyConflictStrategy: ConflictStrategyOverwrite}))
	require.True(t, target.APIKeyGroup.GetX(targetCtx, restored.ID).Enabled)
	require.False(t, target.APIKeyGroup.GetX(targetCtx, restored.ID).ShowChannelInSelfUsage)
	require.Equal(t, "更新备注", target.APIKeyGroup.GetX(targetCtx, restored.ID).Note)
	// 正式旧格式没有分组状态，沿用原有按组名恢复的含义。
	snapshot.Version = BackupVersionV6
	snapshot.APIKeyGroups = nil
	snapshot.APIKeys[0].GroupName = "legacy"
	snapshot.APIKeys[0].Key = "fixture-legacy-group-key"
	data, err = json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, restore.Restore(targetCtx, bytesreader.NewReader(data), RestoreOptions{IncludeAPIKeys: true}))
	require.True(t, target.APIKeyGroup.Query().Where(apikeygroup.NameEQ("legacy")).OnlyX(targetCtx).Enabled)
	require.False(t, target.APIKeyGroup.Query().Where(apikeygroup.NameEQ("legacy")).OnlyX(targetCtx).ShowChannelInSelfUsage)
}

func TestChannelRateBackupRestoreRemapsBothOwners(t *testing.T) {
	db, service, ctx := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	p := db.Project.Create().SetName("rates").SaveX(ctx)
	group := db.APIKeyGroup.Create().SetName("partner").SetProjectID(p.ID).SetGlobalRate(decimal.RequireFromString("0.9")).SaveX(ctx)
	key := db.APIKey.Create().SetKey("fixture-rate").SetName("member").SetProjectID(p.ID).SetGroupRef(group.ID).SetGlobalRate(decimal.Zero).SaveX(ctx)
	ch := createBackupTestChannel(t, db, ctx, "upstream", "openai")
	db.APIKeyGroupChannelRate.Create().SetGroupID(group.ID).SetChannelID(ch.ID).SetMultiplier(decimal.RequireFromString("0.8")).ExecX(ctx)
	db.APIKeyChannelRate.Create().SetKeyID(key.ID).SetChannelID(ch.ID).SetMultiplier(decimal.Zero).ExecX(ctx)
	_, usage := createBackupTestUsage(t, db, ctx, p, ch, key)
	usage.Update().SetBillingGroupID(group.ID).SetBilledCost(0).ExecX(ctx)
	source, err := service.Backup(ctx, BackupOptions{IncludeAPIKeys: true, IncludeUsageStats: true, IncludeRequestLogs: true})
	require.NoError(t, err)
	target, restore, tc := setupBackupTest(t)
	t.Cleanup(func() { require.NoError(t, target.Close()) })
	tp := target.Project.Create().SetName("rates").SaveX(tc)
	target.APIKeyGroup.Create().SetName("other").SetProjectID(tp.ID).ExecX(tc)
	createBackupTestChannel(t, target, tc, "other", "openai")
	mapped := createBackupTestChannel(t, target, tc, ch.Name, "openai")
	require.NotEqual(t, ch.ID, mapped.ID)
	opts := RestoreOptions{IncludeAPIKeys: true, IncludeUsageStats: true, IncludeRequestLogs: true, APIKeyConflictStrategy: ConflictStrategyOverwrite}
	for range 2 {
		require.NoError(t, restore.Restore(tc, bytesreader.NewReader(source), opts))
	}
	gRate := target.APIKeyGroupChannelRate.Query().OnlyX(tc)
	kRate := target.APIKeyChannelRate.Query().OnlyX(tc)
	require.Equal(t, "0.9", target.APIKeyGroup.GetX(tc, gRate.GroupID).GlobalRate.String())
	require.Equal(t, "0", target.APIKey.GetX(tc, kRate.KeyID).GlobalRate.String())
	require.Equal(t, mapped.ID, gRate.ChannelID)
	require.Equal(t, mapped.ID, kRate.ChannelID)
	require.True(t, kRate.Multiplier.IsZero())
	log := target.UsageLog.Query().OnlyX(tc)
	require.Equal(t, gRate.GroupID, *log.BillingGroupID)
	require.Equal(t, 0.0, *log.BilledCost)

	for _, owner := range []string{"group", "key"} {
		for _, create := range []bool{true, false} {
			var invalid BackupData
			require.NoError(t, json.Unmarshal(source, &invalid))
			negative := lo.ToPtr(decimal.NewFromInt(-1))
			if owner == "group" {
				invalid.APIKeyGroups[0].GlobalRate = negative
				if create {
					invalid.APIKeyGroups[0].Name = "invalid-new-group"
				}
			} else {
				invalid.APIKeys[0].GlobalRate = negative
				if create {
					invalid.APIKeys[0].Key = "invalid-new-key"
				}
			}
			data, err := json.Marshal(invalid)
			require.NoError(t, err)
			require.ErrorContains(t, restore.Restore(tc, bytesreader.NewReader(data), opts), "global rate must be nonnegative")
		}
	}
	require.Equal(t, "0.9", target.APIKeyGroup.GetX(tc, gRate.GroupID).GlobalRate.String())
	require.Equal(t, "0", target.APIKey.GetX(tc, kRate.KeyID).GlobalRate.String())
}

func TestArchivedChannelRateBackup(t *testing.T) {
	db, svc, ctx := setupBackupTest(t)
	defer db.Close()
	p := db.Project.Create().SetName("archive-project").SaveX(ctx)
	g := db.APIKeyGroup.Create().SetName("former group").SetProjectID(p.ID).SaveX(ctx)
	k := db.APIKey.Create().SetName("member").SetKey("fixture").SetProjectID(p.ID).SaveX(ctx)
	ch := createBackupTestChannel(t, db, ctx, "archive-channel", "openai")
	req, l := createBackupTestUsage(t, db, ctx, p, ch, k)
	requestTime := l.CreatedAt.AddDate(0, 0, -1).UTC()
	require.NoError(t, db.Driver().Exec(ctx, "UPDATE requests SET created_at = ? WHERE id = ?", []any{requestTime, req.ID}, nil))
	req = db.Request.GetX(ctx, req.ID)

	evidence := &objects.RequestCostSnapshot{Billing: &objects.ExecutionBilling{GroupID: &g.ID, GroupName: g.Name, Rate: lo.ToPtr(decimal.Zero), Source: "key"}}
	l = l.Update().SetBillingGroupID(g.ID).SetBilledCost(0).SetCostSnapshot(evidence).SaveX(ctx)
	second := db.UsageLog.Create().SetRequestID(req.ID).SetProjectID(p.ID).SetAPIKeyID(k.ID).SetChannelID(ch.ID).SetModelID(l.ModelID).SetFormat(l.Format).SetSource(l.Source).SetTotalCost(2).SaveX(ctx)
	db.BudgetCharge.Create().SetProjectID(p.ID).SetRequestID(req.ID).SetExecutionID(71).SetKeyID(k.ID).SetKeyAccountID(1).SetGroupID(g.ID).SetUsageLogID(second.ID).SetEstimate(0).SetStatus("settled").SetCheckAfter(time.Now()).SetKeyVersions(map[budget.Period]int64{}).SetGroupVersions(map[budget.Period]int64{}).ExecX(ctx)
	opts := BackupOptions{IncludeUsageStats: true, IncludeAPIKeys: true, IncludeRequestLogs: true}
	retained, err := svc.Backup(ctx, opts)
	require.NoError(t, err)
	require.NoError(t, biz.ArchiveUsageStatistics(ctx, db, []*ent.UsageLog{l, second}))
	require.NoError(t, biz.ArchiveRequestStatistics(ctx, db, []int{req.ID}))
	db.UsageLog.Delete().ExecX(ctx)
	db.Request.DeleteOneID(req.ID).ExecX(ctx)
	data, err := svc.Backup(ctx, BackupOptions{IncludeUsageStats: true, IncludeAPIKeys: true, IncludeRequestLogs: true})
	require.NoError(t, err)
	// A pre-upgrade native archive has no portable reference.
	err = db.Driver().Exec(ctx, "UPDATE usage_archives SET restore_ref = NULL", []any{}, nil)
	require.NoError(t, err)
	require.NoError(t, svc.Restore(ctx, bytesreader.NewReader(retained), RestoreOptions{IncludeUsageStats: true, IncludeRequestLogs: true}))
	require.Zero(t, db.UsageLog.Query().CountX(ctx))
	require.Zero(t, db.Request.Query().CountX(ctx))
	var file BackupData
	require.NoError(t, json.Unmarshal(data, &file))
	require.Len(t, file.UsageArchives, 3)
	target, restore, tc := setupBackupTest(t)
	defer target.Close()
	target.Project.Create().SetName("other").ExecX(tc)
	dest := target.Project.Create().SetName(p.Name).SaveX(tc)
	current := target.APIKeyGroup.Create().SetName("current").SetProjectID(dest.ID).SaveX(tc)
	mapped := target.APIKeyGroup.Create().SetName(g.Name).SetProjectID(dest.ID).SaveX(tc)
	target.APIKey.Create().SetName("other").SetKey("ignore").SetProjectID(dest.ID).ExecX(tc)
	member := target.APIKey.Create().SetName(k.Name).SetKey("fixture").SetGroupRef(current.ID).SetProjectID(dest.ID).SaveX(tc)
	createBackupTestChannel(t, target, tc, "other", "openai")
	channel := createBackupTestChannel(t, target, tc, ch.Name, "openai")
	for _, snapshot := range [][]byte{data, data, retained} {
		require.NoError(t, restore.Restore(tc, bytesreader.NewReader(snapshot), RestoreOptions{IncludeUsageStats: true, IncludeRequestLogs: true}))
	}
	require.Zero(t, target.Request.Query().CountX(tc), "archived parents must not be recreated")
	rows := target.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage)).Order(ent.Asc(usagearchive.FieldID)).AllX(tc)
	require.Len(t, rows, 2)
	parent := target.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindRequest)).OnlyX(tc)
	require.Equal(t, parent.RequestID, parent.SourceID)
	require.True(t, parent.OccurredAt.Before(rows[0].OccurredAt))
	require.Zero(t, target.UsageLog.Query().CountX(tc))
	for _, row := range rows {
		require.Equal(t, parent.RequestID, row.RequestID)
		require.Equal(t, dest.ID, row.ProjectID)
		require.Equal(t, member.ID, row.APIKeyID)
		require.Equal(t, mapped.ID, *row.BillingGroupID)
		require.Equal(t, channel.ID, row.ChannelID)
	}
	require.Equal(t, 0.42, *rows[0].TotalCost)
	require.Zero(t, *rows[0].BilledCost)
	require.Equal(t, "key", rows[0].BillingEvidence.Source)
	require.Equal(t, 2.0, *rows[1].TotalCost)
	require.Equal(t, 2.0, *rows[1].BilledCost)
	otherTarget, otherRestore, oc := setupBackupTest(t)
	defer otherTarget.Close()
	otherTarget.Project.Create().SetName(p.Name).SaveX(oc)
	createBackupTestChannel(t, otherTarget, oc, ch.Name, "openai")
	require.NoError(t, otherRestore.Restore(oc, bytesreader.NewReader(retained), RestoreOptions{IncludeUsageStats: true}))
	billParent := otherTarget.Request.Query().OnlyIDX(oc)
	for _, snapshot := range [][]byte{retained, data, retained} {
		require.NoError(t, otherRestore.Restore(oc, bytesreader.NewReader(snapshot), RestoreOptions{IncludeRequestLogs: true, IncludeUsageStats: true}))
	}
	require.Equal(t, 2, otherTarget.UsageLog.Query().CountX(oc))
	require.Zero(t, otherTarget.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage)).CountX(oc))
	restoredRequest := otherTarget.Request.Query().OnlyX(oc)
	require.Equal(t, billParent, restoredRequest.ID)
	require.True(t, req.CreatedAt.Equal(restoredRequest.CreatedAt))
	require.JSONEq(t, string(req.RequestBody), string(restoredRequest.RequestBody))
	parentID := restoredRequest.ID
	all := otherTarget.UsageLog.Query().AllX(oc)
	require.NoError(t, biz.ArchiveUsageStatistics(oc, otherTarget, all))
	require.NoError(t, biz.ArchiveRequestStatistics(oc, otherTarget, []int{parentID}))
	otherTarget.UsageLog.Delete().ExecX(oc)
	otherTarget.Request.Delete().ExecX(oc)
	require.NoError(t, otherRestore.Restore(oc, bytesreader.NewReader(retained), RestoreOptions{IncludeRequestLogs: true, IncludeUsageStats: true}))
	require.Zero(t, otherTarget.UsageLog.Query().CountX(oc))
	require.Zero(t, otherTarget.Request.Query().CountX(oc))
	require.Equal(t, 2, otherTarget.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage)).CountX(oc))
}
