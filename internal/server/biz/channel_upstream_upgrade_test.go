package biz

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
)

func TestUpstreamSchemaUpgradeAndRepeat(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:upstream-schema-upgrade?mode=memory&cache=shared&_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	oldTables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	oldTables = slices.DeleteFunc(oldTables, func(table *schema.Table) bool { return table.Name == "channel_upstream_statuses" })
	for _, table := range oldTables {
		table.Columns = slices.DeleteFunc(table.Columns, func(column *schema.Column) bool {
			return column.Name == "upstream_credentials" || column.Name == "cost_snapshot"
		})
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, oldTables, migrate.WithForeignKeys(false)))
	_, err = db.ExecContext(ctx, `INSERT INTO channels (created_at,updated_at,type,name,base_url,credentials,supported_models,status,settings,default_test_model) VALUES (CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'openai','legacy','http://127.0.0.1','{"apiKey":"fixture-legacy"}','["model"]','disabled','{"modelPriceMultiplier":"0.3"}','model')`)
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)), "repeat migration after a partial deployment is safe")
	ch := client.Channel.Query().OnlyX(ctx)
	require.False(t, ch.Settings.UpstreamSettings().Enabled)
	require.Equal(t, "0.3", channelManualMultiplier(ch).String())
	require.Nil(t, ch.UpstreamCredentials)
	_, err = client.ChannelUpstreamStatus.Create().SetChannelID(ch.ID).SetKeyFingerprint(upstream.KeyRef("fixture-legacy")).SetConfigFingerprint(upstreamConfigFingerprint(ch)).SetSnapshot(upstream.EmptySnapshot()).Save(ctx)
	require.NoError(t, err)
	_, err = client.ChannelUpstreamStatus.Create().SetChannelID(ch.ID).SetKeyFingerprint(upstream.KeyRef("fixture-legacy")).SetConfigFingerprint(upstreamConfigFingerprint(ch)).SetSnapshot(upstream.EmptySnapshot()).Save(ctx)
	require.True(t, ent.IsConstraintError(err), "each channel/key pair has exactly one snapshot")
}
