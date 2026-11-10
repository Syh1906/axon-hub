package backup

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

func TestBackupUsageRestoresEveryBillAndIsIdempotent(t *testing.T) {
	for _, withRequests := range []bool{true, false} {
		for _, sameChannel := range []bool{true, false} {
			t.Run(map[bool]string{true: "request-log", false: "usage-only"}[withRequests]+"/"+map[bool]string{true: "same-channel", false: "cross-channel"}[sameChannel], func(t *testing.T) {
				source, svc, ctx := setupBackupTest(t)
				a := createBackupTestChannel(t, source, ctx, "bill-a", channel.TypeOpenai)
				b := a
				if !sameChannel {
					b = createBackupTestChannel(t, source, ctx, "bill-b", channel.TypeOpenai)
				}
				project := createBackupTestProject(t, source, ctx, "billing-project", "fixture")
				key := createBackupTestAPIKey(t, source, ctx, source.User.Query().FirstX(ctx), project, "fixture", "fixture-billing-key")
				req, first := createBackupTestUsage(t, source, ctx, project, a, key)
				first = first.Update().SetTotalCost(0.02).SaveX(ctx)
				secondTime := first.CreatedAt.Add(time.Millisecond)
				if sameChannel {
					secondTime = first.CreatedAt
				}
				source.UsageLog.Create().SetRequestID(req.ID).SetProjectID(project.ID).SetAPIKeyID(key.ID).SetChannelID(b.ID).
					SetCreatedAt(secondTime).SetModelID(first.ModelID).SetSource(first.Source).SetFormat(first.Format).
					SetPromptTokens(20).SetCompletionTokens(10).SetTotalTokens(30).SetTotalCost(0.03).SaveX(ctx)
				data, err := svc.Backup(ctx, BackupOptions{IncludeChannels: true, IncludeProjects: true, IncludeAPIKeys: true, IncludeUsageStats: true, IncludeRequestLogs: withRequests})
				require.NoError(t, err)
				var backup BackupData
				require.NoError(t, json.Unmarshal(data, &backup))
				require.Len(t, backup.UsageLogs, 2)
				// Repeated source IDs within one file are still duplicates.
				backup.UsageLogs = append(backup.UsageLogs, backup.UsageLogs[0])
				data, err = json.Marshal(backup)
				require.NoError(t, err)
				require.NoError(t, source.Close())

				target, restore, targetCtx := setupBackupTest(t)
				defer target.Close()
				createBackupTestChannel(t, target, targetCtx, "unrelated", channel.TypeOpenai)
				opts := RestoreOptions{
					IncludeChannels: true, IncludeProjects: true, IncludeAPIKeys: true, IncludeUsageStats: true, IncludeRequestLogs: withRequests,
					ChannelConflictStrategy: ConflictStrategySkip, ProjectConflictStrategy: ConflictStrategySkip, APIKeyConflictStrategy: ConflictStrategySkip,
				}
				require.NoError(t, restore.Restore(targetCtx, bytes.NewReader(data), opts))
				for _, original := range backup.Channels {
					current := target.Channel.Query().Where(channel.NameEQ(original.Name)).OnlyX(targetCtx)
					opts.ChannelMappings = append(opts.ChannelMappings, &ChannelRestoreMapping{SourceID: original.ID, TargetID: current.ID})
				}
				check := func() {
					logs := target.UsageLog.Query().Order(usagelog.ByID()).AllX(targetCtx)
					require.Len(t, logs, 2)
					require.InDelta(t, 0.05, *logs[0].TotalCost+*logs[1].TotalCost, 1e-12)
					require.Equal(t, logs[0].RequestID, logs[1].RequestID)
					require.Equal(t, sameChannel, logs[0].ChannelID == logs[1].ChannelID)
				}
				check()
				require.NoError(t, restore.Restore(targetCtx, bytes.NewReader(data), opts))
				check()
				// A partial old restore must be repairable without charging its remaining row again.
				firstRestored := target.UsageLog.Query().Order(usagelog.ByID()).AllX(targetCtx)[0]
				require.NoError(t, target.UsageLog.DeleteOne(firstRestored).Exec(targetCtx))
				require.NoError(t, restore.Restore(targetCtx, bytes.NewReader(data), opts))
				check()
			})
		}
	}
}

func TestBackupUsageKeepsIdenticalBillsAcrossBatchesAndLateSettlement(t *testing.T) {
	db, svc, ctx := setupBackupTest(t)
	defer db.Close()
	ch := createBackupTestChannel(t, db, ctx, "same-bill", channel.TypeOpenai)
	project := createBackupTestProject(t, db, ctx, "same-bill-project", "fixture")
	key := createBackupTestAPIKey(t, db, ctx, db.User.Query().FirstX(ctx), project, "fixture", "fixture-identical-key")
	_, original := createBackupTestUsage(t, db, ctx, project, ch, key)
	data, err := svc.Backup(ctx, BackupOptions{IncludeUsageStats: true, IncludeRequestLogs: true})
	require.NoError(t, err)
	var backup BackupData
	require.NoError(t, json.Unmarshal(data, &backup))
	for i := 1; i <= backupBatchSize; i++ {
		copy := *backup.UsageLogs[0]
		copy.ID = original.ID + i
		backup.UsageLogs = append(backup.UsageLogs, &copy)
	}
	data, err = json.Marshal(backup)
	require.NoError(t, err)
	opts := RestoreOptions{IncludeUsageStats: true, IncludeRequestLogs: true}
	require.NoError(t, svc.Restore(ctx, bytes.NewReader(data), opts))
	require.Equal(t, backupBatchSize+1, db.UsageLog.Query().CountX(ctx))
	// A later settlement is a change to the same bill, not an extra bill.
	original.Update().SetTotalCost(0.5).ExecX(ctx)
	require.NoError(t, svc.Restore(ctx, bytes.NewReader(data), opts))
	require.Equal(t, backupBatchSize+1, db.UsageLog.Query().CountX(ctx))
	require.Equal(t, 0.5, *db.UsageLog.GetX(ctx, original.ID).TotalCost)
}
