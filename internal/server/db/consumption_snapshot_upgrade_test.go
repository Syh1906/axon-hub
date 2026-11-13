package db

import (
	"context"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
)

func TestConsumptionSnapshotSchemaUpgrade(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		checkConsumptionSnapshotUpgrade(t, "sqlite3", "file:consumption_snapshot_upgrade?mode=memory&_fk=0")
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("AXONHUB_TEST_CONSUMPTION_PG_DSN")
		if dsn == "" {
			t.Skip("AXONHUB_TEST_CONSUMPTION_PG_DSN not set")
		}
		checkConsumptionSnapshotUpgrade(t, "postgres", dsn)
	})
}

func checkConsumptionSnapshotUpgrade(t *testing.T, dialect, dsn string) {
	t.Helper()
	dbDialect, raw, err := openDB(dialect, dsn, 1, 1, 0, 0)
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dbDialect, raw)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	tables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	old := tables[:0]
	for _, table := range tables {
		if table.Name != "consumption_snapshots" {
			old = append(old, table)
		}
	}
	require.NoError(t, migrate.Create(t.Context(), client.Schema, old))
	ctx := authz.WithTestBypass(context.Background())
	p := client.Project.Create().SetName("历史项目").SaveX(ctx)
	require.NoError(t, client.Schema.Create(ctx))
	now := time.Now().UTC()
	client.ConsumptionSnapshot.Create().SetCacheKey("upgrade").SetProjectID(p.ID).SetRangeStart(now).SetRangeEnd(now.Add(time.Hour)).SetGeneratedAt(now).SetPayload([]byte(`{"entries":[]}`)).SaveX(ctx)
	require.NoError(t, client.Schema.Create(ctx))
	require.Equal(t, 1, client.ConsumptionSnapshot.Query().CountX(ctx))
	require.Equal(t, "历史项目", client.Project.GetX(ctx, p.ID).Name)
	// rc1-fork.3 与新版现有表定义相同；按旧表集合初始化不应伤及历史数据。
	require.NoError(t, migrate.Create(ctx, client.Schema, old, migrate.WithDropColumn(true), migrate.WithDropIndex(true)))
	require.Equal(t, "历史项目", client.Project.GetX(ctx, p.ID).Name)
	require.Equal(t, 1, client.ConsumptionSnapshot.Query().CountX(ctx))
}
