package db

import (
	"context"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/stretchr/testify/require"
)

func TestChannelStatisticsSchemaUpgrade(t *testing.T) {
	dialect, raw, err := openDB("sqlite3", "file:channel-statistics-upgrade?mode=memory&cache=shared", 1, 1, 0, 0)
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect, raw)))
	defer client.Close()
	ctx := authz.WithTestBypass(context.Background())
	old, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	for _, table := range old {
		if table.Name == "channel_probes" {
			columns := table.Columns[:0]
			for _, col := range table.Columns {
				if col.Name != "speed_sample_count" && col.Name != "first_token_sample_count" {
					columns = append(columns, col)
				}
			}
			table.Columns = columns
		}
		if table.Name == "usage_archives" {
			for _, col := range table.Columns {
				if col.Name == "kind" {
					col.Enums = []string{"request", "usage"}
				}
			}
		}
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, old, migrate.WithForeignKeys(false)))
	_, err = raw.ExecContext(ctx, "INSERT INTO channel_probes(channel_id,total_request_count,success_request_count,avg_tokens_per_second,speed_version,avg_time_to_first_token_ms,timestamp) VALUES(1,10,9,20,2,2000,1)")
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	probe := client.ChannelProbe.Query().OnlyX(ctx)
	require.Equal(t, 10, probe.TotalRequestCount)
	require.Equal(t, 9, probe.SuccessRequestCount)
	require.Zero(t, probe.SpeedSampleCount)
	require.Zero(t, probe.FirstTokenSampleCount)
	require.NoError(t, client.UsageArchive.Create().SetKind(usagearchive.KindExecution).SetSourceID(1).SetRequestID(1).
		SetProjectID(1).SetModelID("model").SetOccurredAt(time.Now()).Exec(ctx))
}
