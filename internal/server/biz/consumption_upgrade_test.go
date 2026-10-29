package biz

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/stretchr/testify/require"
)

func TestConsumptionSchemaUpgradePreservesMissingSamples(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:consumption-upgrade?mode=memory&cache=shared&_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	oldTables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	oldTables = slices.DeleteFunc(oldTables, func(table *schema.Table) bool {
		return table.Name == "cache_effectiveness_windows"
	})
	for _, table := range oldTables {
		if table.Name == "request_executions" {
			table.Columns = slices.DeleteFunc(table.Columns, func(column *schema.Column) bool {
				return slices.Contains([]string{"metrics_first_byte_latency_ms", "metrics_first_byte_recorded", "cache_compatibility_key", "theoretical_cache_tokens", "cache_ttl_bucket"}, column.Name)
			})
		}
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, oldTables, migrate.WithForeignKeys(false)))
	_, err = db.ExecContext(ctx, `INSERT INTO request_executions (created_at, updated_at, project_id, request_id, channel_id, model_id, request_body, status, metrics_latency_ms, metrics_first_token_latency_ms) VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1, 1, 1, 'legacy-model', '{}', 'completed', 1200, 700)`)
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)), "repeated upgrades are safe")
	legacy := client.RequestExecution.Query().OnlyX(ctx)
	require.Equal(t, int64(1200), *legacy.MetricsLatencyMs)
	require.Equal(t, int64(700), *legacy.MetricsFirstTokenLatencyMs)
	require.Nil(t, legacy.MetricsFirstByteLatencyMs)
	require.Nil(t, legacy.MetricsFirstByteRecorded)
	require.Nil(t, legacy.CacheCompatibilityKey)
	require.Nil(t, legacy.TheoreticalCacheTokens)
	require.Nil(t, legacy.CacheTTLBucket)

	start := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	createWindow := func(projectID int) error {
		return client.CacheEffectivenessWindow.Create().SetProjectID(projectID).SetChannelID(1).
			SetModelID("model").SetCacheTTLBucket("5m").SetWindowStart(start).SetWindowEnd(start.Add(5 * time.Minute)).
			SetSampleCount(1).SetEligibleCount(1).SetTheoreticalTokens(100).SetObservedTokens(50).Exec(ctx)
	}
	require.NoError(t, createWindow(1))
	require.True(t, ent.IsConstraintError(createWindow(1)), "a settled window cannot be duplicated")
	require.NoError(t, createWindow(2), "projects keep independent window histories")
}
