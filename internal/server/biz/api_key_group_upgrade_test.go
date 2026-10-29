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
	tables = slices.DeleteFunc(tables, func(table *schema.Table) bool { return table.Name == "api_key_groups" })
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
