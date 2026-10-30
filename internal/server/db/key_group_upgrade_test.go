package db

import (
	"context"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
)

func TestKeyGroupManagementSchemaUpgrade(t *testing.T) {
	dialect, raw, err := openDB("sqlite3", "file:key-group-upgrade?mode=memory&cache=shared", 1, 1, 0, 0)
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect, raw)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	old, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	tables := make([]*schema.Table, 0, len(old))
	for _, table := range old {
		if table.Name == "api_key_operations" {
			continue
		}
		if table.Name == "api_key_groups" {
			columns := table.Columns[:0]
			for _, column := range table.Columns {
				if column.Name != "note" && column.Name != "enabled" && column.Name != "revision" {
					columns = append(columns, column)
				}
			}
			table.Columns = columns
		}
		tables = append(tables, table)
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, tables, migrate.WithForeignKeys(false)))
	project := client.Project.Create().SetName("legacy-project").SaveX(ctx)
	_, err = raw.ExecContext(ctx, "INSERT INTO api_key_groups (name,project_id,created_at,updated_at) VALUES (?,?,?,?)", "existing", project.ID, time.Now(), time.Now())
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	group := client.APIKeyGroup.Query().OnlyX(ctx)
	require.Equal(t, "existing", group.Name)
	require.True(t, group.Enabled)
	require.Empty(t, group.Note)
	require.Zero(t, group.Revision)
	require.NoError(t, client.APIKeyOperation.Create().SetProjectID(project.ID).SetActorID(1).SetOperationID("batch").SetRowID("row").SetDigest("digest").Exec(ctx))
	require.Error(t, client.APIKeyOperation.Create().SetProjectID(project.ID).SetActorID(1).SetOperationID("batch").SetRowID("row").SetDigest("digest").Exec(ctx))
}
