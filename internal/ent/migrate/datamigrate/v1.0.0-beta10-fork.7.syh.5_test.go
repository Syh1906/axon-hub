package datamigrate_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/migrate/datamigrate"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xversion"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/stretchr/testify/require"
)

func TestChannelAccountsUpgradePreservesOnlyProvenOwnership(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:account-upgrade?mode=memory&_fk=1")
	defer db.Close()
	ctx := authz.WithTestBypass(context.Background())
	ch := db.Channel.Create().SetName("legacy").SetType(channel.TypeOpenai).SetBaseURL("https://example.com/v1").SetCredentials(objects.ChannelCredentials{APIKeys: []string{"fixture-a", "fixture-b"}}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetUpstreamCredentials(&objects.ChannelUpstreamCredentials{AccountToken: "fixture-account", AccountID: 17}).SaveX(ctx)
	digest := sha256.Sum256([]byte(`{"BaseURL":"https://example.com/v1","Credentials":{"accountToken":"fixture-account","accountId":17}}`))
	snapshot := upstream.EmptySnapshot()
	snapshot.Account = &objects.UpstreamAccountSnapshot{AccountID: 17, UpdatedAt: time.Now()}
	db.ChannelUpstreamStatus.Create().SetChannelID(ch.ID).SetKeyFingerprint(upstream.KeyRef("fixture-a")).SetConfigFingerprint(hex.EncodeToString(digest[:])).SetSnapshot(snapshot).SaveX(ctx)
	migration := datamigrate.NewV1_0_0_Beta10_Fork_7_Syh_5()
	require.NoError(t, migration.Migrate(ctx, db))
	require.NoError(t, migration.Migrate(ctx, db))
	stored := db.Channel.GetX(ctx, ch.ID)
	require.Len(t, stored.UpstreamCredentials.Accounts, 1)
	require.Equal(t, "fixture-account", stored.UpstreamCredentials.Accounts[0].AccountToken)
	require.Empty(t, stored.UpstreamCredentials.AccountToken)
	require.Len(t, stored.Settings.KeySettings, 1)
	require.NotEmpty(t, stored.Settings.KeySettingsFor(upstream.KeyRef("fixture-a")).AccountRef)
	require.Empty(t, stored.Settings.KeySettingsFor(upstream.KeyRef("fixture-b")).AccountRef)
	require.Len(t, stored.Settings.UpstreamAddresses, 1)
	require.Equal(t, ch.BaseURL, stored.Settings.UpstreamAddresses[0].URL)
	require.Len(t, stored.Credentials.GetAllAPIKeys(), 2)
	previous, err := xversion.Parse("v1.0.0-beta10-fork.7.syh.4")
	require.NoError(t, err)
	current, err := xversion.Parse(migration.Version())
	require.NoError(t, err)
	next, err := xversion.Parse("v1.0.0-beta10-fork.8")
	require.NoError(t, err)
	require.True(t, previous.LessThan(current))
	require.True(t, current.LessThan(next))
}
