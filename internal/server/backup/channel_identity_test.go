package backup

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestBackupDuplicateChannelsRoundTrip(t *testing.T) {
	source, svc, ctx := setupBackupTest(t)
	a := createBackupTestChannel(t, source, ctx, "same", channel.TypeOpenai)
	b := createBackupTestChannel(t, source, ctx, "same", channel.TypeOpenai)
	source.Channel.UpdateOne(a).SetTags([]string{"小为"}).SaveX(ctx)
	source.Channel.UpdateOne(b).SetTags([]string{"裂魂"}).SaveX(ctx)
	pa := createBackupTestChannelModelPrice(t, source, ctx, a.ID, "gpt-4")
	pa = source.ChannelModelPrice.UpdateOne(pa).SetReferenceID("ref-first-gpt-4").SaveX(ctx)
	pb := createBackupTestChannelModelPrice(t, source, ctx, b.ID, "gpt-4")
	project := createBackupTestProject(t, source, ctx, "duplicate-project", "fixture")
	user := source.User.Query().FirstX(ctx)
	key := createBackupTestAPIKey(t, source, ctx, user, project, "fixture", "not-a-real-key")
	source.APIKey.UpdateOne(key).SetProfiles(&objects.APIKeyProfiles{Profiles: []objects.APIKeyProfile{{Name: "default", ChannelIDs: []int{a.ID, b.ID}, ChannelPriorities: []objects.ChannelPriority{{ChannelID: b.ID, Priority: 2}}}}}).SaveX(ctx)
	m := createBackupTestModel(t, source, ctx, "openai", "gpt-4")
	source.Model.UpdateOne(m).SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{
		{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: a.ID, ModelID: "gpt-4"}},
		{Type: "channel_request", ChannelRequest: &objects.ChannelRequestAssociation{ChannelID: b.ID}},
	}}).SaveX(ctx)
	ra, ua := createBackupTestUsage(t, source, ctx, project, a, key)
	rb := source.Request.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).SetChannelID(b.ID).
		SetModelID(ra.ModelID).SetSource(ra.Source).SetFormat(ra.Format).SetStatus(ra.Status).SetStream(ra.Stream).
		SetClientIP(ra.ClientIP).SetRequestBody(ra.RequestBody).SetCreatedAt(ra.CreatedAt).SaveX(ctx)
	source.UsageLog.Create().SetRequestID(rb.ID).SetProjectID(project.ID).SetAPIKeyID(key.ID).SetChannelID(b.ID).
		SetModelID(ua.ModelID).SetSource(ua.Source).SetFormat(ua.Format).SetPromptTokens(100).SetCompletionTokens(50).SetTotalTokens(150).SetTotalCost(0.9).SaveX(ctx)
	data, err := svc.Backup(ctx, BackupOptions{IncludeChannels: true, IncludeModels: true, IncludeProjects: true, IncludeAPIKeys: true, IncludeModelPrices: true, IncludeUsageStats: true, IncludeRequestLogs: true})
	require.NoError(t, err)
	header := bytes.SplitN(data, []byte("\n"), 2)[0]
	require.NotContains(t, string(header), "credentials")
	var manifest BackupData
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Len(t, manifest.ChannelReferences, 2)
	require.NoError(t, source.Close())

	target, restore, targetCtx := setupBackupTest(t)
	defer target.Close()
	untouched := createBackupTestChannel(t, target, targetCtx, "unrelated", channel.TypeOpenai)
	require.NoError(t, restore.Restore(targetCtx, bytes.NewReader(data), RestoreOptions{IncludeChannels: true, IncludeModels: true, IncludeProjects: true, IncludeAPIKeys: true, IncludeModelPrices: true, IncludeUsageStats: true, IncludeRequestLogs: true, ChannelConflictStrategy: ConflictStrategyOverwrite, ModelConflictStrategy: ConflictStrategyOverwrite, APIKeyConflictStrategy: ConflictStrategyOverwrite}))
	channels := target.Channel.Query().Where(channel.NameEQ("same")).AllX(targetCtx)
	require.Len(t, channels, 2)
	require.Equal(t, "unrelated", target.Channel.GetX(targetCtx, untouched.ID).Name)
	ids := map[string]int{}
	for _, ch := range channels {
		ids[ch.Tags[0]] = ch.ID
	}
	require.NotEqual(t, ids["小为"], ids["裂魂"])
	for tag, expected := range map[string]float64{"小为": 0.42, "裂魂": 0.9} {
		logs := target.UsageLog.Query().Where(usagelog.ChannelIDEQ(ids[tag])).AllX(targetCtx)
		require.Len(t, logs, 1)
		require.Equal(t, expected, *logs[0].TotalCost)
	}
	require.Equal(t, pa.ReferenceID, target.ChannelModelPrice.Query().Where(channelmodelprice.ChannelIDEQ(ids["小为"])).OnlyX(targetCtx).ReferenceID)
	require.Equal(t, pb.ReferenceID, target.ChannelModelPrice.Query().Where(channelmodelprice.ChannelIDEQ(ids["裂魂"])).OnlyX(targetCtx).ReferenceID)
	restoredModel := target.Model.Query().OnlyX(targetCtx)
	require.Equal(t, ids["小为"], restoredModel.Settings.Associations[0].ChannelModel.ChannelID)
	require.Equal(t, ids["裂魂"], restoredModel.Settings.Associations[1].ChannelRequest.ChannelID)
	restoredKey := target.APIKey.Query().OnlyX(targetCtx)
	require.Equal(t, []int{ids["小为"], ids["裂魂"]}, restoredKey.Profiles.Profiles[0].ChannelIDs)
	require.Equal(t, ids["裂魂"], restoredKey.Profiles.Profiles[0].ChannelPriorities[0].ChannelID)
}

func TestBackupDuplicateChannelsPartialRestoreRequiresTargets(t *testing.T) {
	client, svc, ctx := setupBackupTest(t)
	defer client.Close()
	a := createBackupTestChannel(t, client, ctx, "same", channel.TypeOpenai)
	b := createBackupTestChannel(t, client, ctx, "same", channel.TypeOpenai)
	pa := createBackupTestChannelModelPrice(t, client, ctx, a.ID, "model-a")
	pb := createBackupTestChannelModelPrice(t, client, ctx, b.ID, "model-b")
	data, err := svc.Backup(ctx, BackupOptions{IncludeModelPrices: true})
	require.NoError(t, err)
	var decoded BackupData
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Empty(t, decoded.Channels)
	require.Len(t, decoded.ChannelReferences, 2)
	opts := RestoreOptions{IncludeModelPrices: true, ModelPriceConflictStrategy: ConflictStrategyOverwrite}
	require.ErrorContains(t, svc.Restore(ctx, bytes.NewReader(data), opts), "ambiguous")
	require.Equal(t, 2, client.ChannelModelPrice.Query().CountX(ctx), "failure must roll back")
	opts.ChannelMappings = []*ChannelRestoreMapping{{SourceID: a.ID, TargetID: b.ID}, {SourceID: b.ID, TargetID: a.ID}}
	require.NoError(t, svc.Restore(ctx, bytes.NewReader(data), opts))
	require.Equal(t, 4, client.ChannelModelPrice.Query().CountX(ctx))
	require.NotEqual(t, pa.ReferenceID, client.ChannelModelPrice.Query().Where(channelmodelprice.ChannelIDEQ(b.ID), channelmodelprice.ModelIDEQ("model-a")).OnlyX(ctx).ReferenceID)
	require.NotEqual(t, pb.ReferenceID, client.ChannelModelPrice.Query().Where(channelmodelprice.ChannelIDEQ(a.ID), channelmodelprice.ModelIDEQ("model-b")).OnlyX(ctx).ReferenceID)
	// An unrelated ambiguous channel must not block system-only restore.
	require.NoError(t, svc.Restore(ctx, bytes.NewReader(data), RestoreOptions{IncludeSystemConfigs: true}))
}

func TestBackupDuplicateChannelsLegacyAmbiguity(t *testing.T) {
	client, svc, ctx := setupBackupTest(t)
	defer client.Close()
	a := createBackupTestChannel(t, client, ctx, "same", channel.TypeOpenai)
	b := createBackupTestChannel(t, client, ctx, "same", channel.TypeOpenai)
	backup := BackupData{Version: BackupVersionV7, Channels: []*BackupChannel{{Channel: ent.Channel{ID: 100, Name: "same", Type: channel.TypeOpenai, Status: channel.StatusEnabled}, Credentials: objects.ChannelCredentials{APIKey: "fixture"}}}}
	data, err := json.Marshal(backup)
	require.NoError(t, err)
	opts := RestoreOptions{IncludeChannels: true, ChannelConflictStrategy: ConflictStrategyOverwrite}
	require.ErrorContains(t, svc.Restore(ctx, bytes.NewReader(data), opts), "ambiguous")
	opts.ChannelMappings = []*ChannelRestoreMapping{{SourceID: 100, TargetID: b.ID}}
	require.NoError(t, svc.Restore(ctx, bytes.NewReader(data), opts))
	require.NotEqual(t, "fixture", client.Channel.GetX(ctx, a.ID).Credentials.APIKey)
	require.Equal(t, "fixture", client.Channel.GetX(ctx, b.ID).Credentials.APIKey)
}

func TestBackupSkippedModelDoesNotRequireChannelTarget(t *testing.T) {
	client, svc, ctx := setupBackupTest(t)
	defer client.Close()
	existing := createBackupTestModel(t, client, ctx, "openai", "model")
	source := *existing
	source.Settings = &objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: 999, ModelID: "model"}}}}
	data, err := json.Marshal(BackupData{Version: BackupVersion, Models: []*BackupModel{{Model: source}}})
	require.NoError(t, err)
	require.NoError(t, svc.Restore(ctx, bytes.NewReader(data), RestoreOptions{IncludeModels: true, ModelConflictStrategy: ConflictStrategySkip}))
	require.Empty(t, client.Model.GetX(ctx, existing.ID).Settings.Associations)
}

func TestBackupPartialCannotCreateMissingChannel(t *testing.T) {
	client, svc, ctx := setupBackupTest(t)
	defer client.Close()
	ch := createBackupTestChannel(t, client, ctx, "partial", channel.TypeOpenai)
	createBackupTestChannelModelPrice(t, client, ctx, ch.ID, "model")
	data, err := svc.Backup(ctx, BackupOptions{IncludeModelPrices: true})
	require.NoError(t, err)
	var decoded BackupData
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Empty(t, decoded.Channels)
	require.False(t, decoded.ChannelReferences[0].HasConfig)
	err = svc.Restore(ctx, bytes.NewReader(data), RestoreOptions{IncludeChannels: true, IncludeModelPrices: true, ChannelMappings: []*ChannelRestoreMapping{{SourceID: ch.ID, TargetID: 0}}})
	require.ErrorContains(t, err, "no restorable configuration")
	require.Equal(t, 1, client.Channel.Query().CountX(ctx))
	require.Equal(t, 1, client.ChannelModelPrice.Query().CountX(ctx))
}

func TestBackupLegacyPriceOnlyMapsByName(t *testing.T) {
	client, svc, ctx := setupBackupTest(t)
	defer client.Close()
	a := createBackupTestChannel(t, client, ctx, "same", channel.TypeOpenai)
	b := createBackupTestChannel(t, client, ctx, "same", channel.TypeOpenai)
	price := createBackupTestChannelModelPrice(t, client, ctx, a.ID, "model")
	data, err := json.Marshal(BackupData{Version: BackupVersionV7, ChannelModelPrices: []*BackupChannelModelPrice{{ChannelName: "same", ModelID: "model", Price: price.Price, ReferenceID: "legacy-price"}}})
	require.NoError(t, err)
	opts := RestoreOptions{IncludeChannels: true, IncludeModelPrices: true, ModelPriceConflictStrategy: ConflictStrategyOverwrite}
	require.ErrorContains(t, svc.Restore(ctx, bytes.NewReader(data), opts), "ambiguous")
	opts.ChannelMappings = []*ChannelRestoreMapping{{SourceName: lo.ToPtr("same"), TargetID: b.ID}}
	require.NoError(t, svc.Restore(ctx, bytes.NewReader(data), opts))
	restored := client.ChannelModelPrice.Query().Where(channelmodelprice.ChannelIDEQ(b.ID)).OnlyX(ctx)
	require.True(t, restored.Price.Equals(price.Price))
	require.Equal(t, "legacy-price", restored.ReferenceID)
	require.Equal(t, price.ReferenceID, client.ChannelModelPrice.Query().Where(channelmodelprice.ChannelIDEQ(a.ID)).OnlyX(ctx).ReferenceID)
	opts.ChannelMappings[0].TargetID = 0
	require.ErrorContains(t, svc.Restore(ctx, bytes.NewReader(data), opts), "requires an existing target")
}
