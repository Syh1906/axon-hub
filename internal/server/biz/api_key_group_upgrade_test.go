package biz

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupSchemaUpgrade(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:group-upgrade?mode=memory&cache=shared&_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	tables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	tables = slices.DeleteFunc(tables, func(table *schema.Table) bool {
		return table.Name == "api_key_groups" || table.Name == "api_key_group_channel_rates" || table.Name == "api_key_channel_rates"
	})
	for _, table := range tables {
		if table.Name != "api_keys" {
			continue
		}
		table.Indexes = slices.DeleteFunc(table.Indexes, func(index *schema.Index) bool {
			return slices.ContainsFunc(index.Columns, func(column *schema.Column) bool { return column.Name == "group_ref" })
		})
		table.Columns = slices.DeleteFunc(table.Columns, func(column *schema.Column) bool { return column.Name == "group_ref" })
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, tables, migrate.WithForeignKeys(false)))
	_, err = db.ExecContext(ctx, `INSERT INTO api_keys (created_at, updated_at, name, key, user_id, project_id, type, status) VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'legacy', 'fixture-legacy', 1, 1, 'user', 'enabled')`)
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	key := client.APIKey.Query().OnlyX(ctx)
	require.Equal(t, "legacy", key.Name)
	require.Nil(t, key.GroupRef)
	group := client.APIKeyGroup.Create().SetName("recipient").SetProjectID(1).SaveX(ctx)
	_, err = client.APIKeyGroup.Create().SetName("recipient").SetProjectID(1).Save(ctx)
	require.True(t, ent.IsConstraintError(err))
	require.NoError(t, client.APIKeyGroup.Create().SetName("recipient").SetProjectID(2).Exec(ctx))
	require.NoError(t, key.Update().SetGroupRef(group.ID).Exec(ctx))
}

func TestKeyChannelRatesUpgradePreservesLegacyCost(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:key-channel-rate-upgrade?mode=memory&cache=shared&_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	tables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	tables = slices.DeleteFunc(tables, func(table *schema.Table) bool {
		return table.Name == "api_key_channel_rates" || table.Name == "api_key_group_channel_rates"
	})
	for _, t := range tables {
		if t.Name != "requests" && t.Name != "usage_logs" && t.Name != "usage_archives" && t.Name != "request_executions" {
			continue
		}
		remove := func(c *schema.Column) bool {
			return slices.Contains([]string{"billing_group_id", "billing_group_ref", "billed_cost", "billed_items", "billing_estimated", "billing_evidence", "restore_ref", "billing_dispatched"}, c.Name)
		}
		t.Indexes = slices.DeleteFunc(t.Indexes, func(i *schema.Index) bool { return slices.ContainsFunc(i.Columns, remove) })
		t.Columns = slices.DeleteFunc(t.Columns, remove)
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, tables, migrate.WithForeignKeys(false)))
	_, err = db.ExecContext(ctx, "INSERT INTO usage_logs(created_at,updated_at,model_id,request_id,total_cost) VALUES(CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy',1,2.5)")
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	}
	row := client.UsageLog.Query().OnlyX(ctx)
	require.Nil(t, row.BillingGroupID)
	require.Nil(t, row.BilledCost)
	require.Nil(t, row.RestoreRef)
	require.Equal(t, 2.5, *row.TotalCost)
	require.Zero(t, client.APIKeyChannelRate.Query().CountX(ctx))
	require.Zero(t, client.APIKeyGroupChannelRate.Query().CountX(ctx))
}
