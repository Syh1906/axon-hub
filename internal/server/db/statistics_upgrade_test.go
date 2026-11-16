package db

import (
	"context"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/stretchr/testify/require"
)

func TestStatisticsRevisionSchemaUpgrade(t *testing.T) {
	dbDialect, raw, err := openDB("sqlite3", "file:statistics-refresh-upgrade?mode=memory&_fk=0", 1, 1, 0, 0)
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dbDialect, raw)))
	defer client.Close()
	tables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	old := tables[:0]
	for _, table := range tables {
		if table.Name == "statistics_revisions" {
			continue
		}
		columns := table.Columns[:0]
		for _, col := range table.Columns {
			if !strings.HasPrefix(col.Name, "statistics_") {
				columns = append(columns, col)
			}
		}
		table.Columns = columns
		indexes := table.Indexes[:0]
		for _, index := range table.Indexes {
			if !strings.Contains(index.Name, "statistics") && index.Name != "request_executions_by_usage_match" {
				indexes = append(indexes, index)
			}
		}
		table.Indexes = indexes
		old = append(old, table)
	}
	ctx := authz.WithTestBypass(context.Background())
	require.NoError(t, migrate.Create(ctx, client.Schema, old, migrate.WithForeignKeys(false)))
	_, err = raw.ExecContext(ctx, "INSERT INTO usage_logs(request_id,project_id,channel_id,model_id,total_cost) VALUES(1,1,1,'historical',2.5)")
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	bill := client.UsageLog.Query().OnlyX(ctx)
	require.Equal(t, 2.5, *bill.TotalCost)
	require.False(t, bill.StatisticsStandardReady)
	require.Zero(t, bill.StatisticsVersion)
	require.Nil(t, bill.StatisticsStandardCost)
	require.NoError(t, client.StatisticsRevision.Create().SetProjectID(1).SetOccurredAt(time.Now()).SetVersion("upgrade").Exec(ctx))
}
