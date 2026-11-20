package server

import (
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"
	"slices"
	"testing"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
)

func TestGeminiCacheSchemaUpgrade(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:gemini-cache-upgrade?mode=memory&_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(t.Context())
	tables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	oldTables := slices.DeleteFunc(tables, func(table *schema.Table) bool { return table.Name == "gemini_caches" })
	require.NoError(t, migrate.Create(ctx, client.Schema, oldTables, migrate.WithForeignKeys(false)))
	old := client.Project.Create().SetName("pre-cache project").SaveX(ctx)
	for range 2 {
		require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	}
	require.Equal(t, old.Name, client.Project.GetX(ctx, old.ID).Name)
	require.Zero(t, client.GeminiCache.Query().CountX(ctx))
}
