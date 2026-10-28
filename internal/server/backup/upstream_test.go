package backup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

func TestBackupUpstreamConfigurationAndCostSnapshot(t *testing.T) {
	db, svc, ctx := setupBackupTest(t)
	defer db.Close()
	ch := db.Channel.Create().SetName("upstream backup").SetType(channel.TypeOpenai).SetBaseURL("http://127.0.0.1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture-key"}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetUpstreamCredentials(&objects.ChannelUpstreamCredentials{AccountToken: "fixture-account", AccountID: 17}).SaveX(ctx)
	data, err := svc.Backup(ctx, BackupOptions{IncludeChannels: true})
	require.NoError(t, err)
	ch.Update().ClearUpstreamCredentials().SaveX(ctx)
	require.NoError(t, svc.Restore(ctx, data, RestoreOptions{IncludeChannels: true, ChannelConflictStrategy: ConflictStrategyOverwrite}))
	stored := db.Channel.GetX(ctx, ch.ID)
	require.Equal(t, 17, stored.UpstreamCredentials.AccountID)
	require.Equal(t, "fixture-account", stored.UpstreamCredentials.AccountToken)
	// The normal channel JSON still omits the private authentication field.
	public, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NotContains(t, string(public), "fixture-account")
	entry := BackupUsageLog{UsageLog: ent.UsageLog{CostSnapshot: &objects.RequestCostSnapshot{PriceReferenceID: "price-fixture", PricedAt: time.Now().UTC(), Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.5"), Source: objects.CostMultiplierUpstream}, ManualMultiplier: decimal.NewFromInt(1)}}}
	encoded, err := json.Marshal(entry)
	require.NoError(t, err)
	var restored BackupUsageLog
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.NotNil(t, restored.CostSnapshot)
	require.Equal(t, "0.5", restored.CostSnapshot.Applied.Value.String())
	require.Equal(t, objects.CostMultiplierUpstream, restored.CostSnapshot.Applied.Source)
}
