package db

import (
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
)

func TestRequestResultSchemaUpgrade(t *testing.T) {
	dialect, raw, err := openDB("sqlite3", "file:request-result-upgrade?mode=memory&cache=shared", 1, 1, 0, 0)
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect, raw)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(t.Context())
	old, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	for _, table := range old {
		if table.Name != "requests" {
			continue
		}
		columns := table.Columns[:0]
		for _, col := range table.Columns {
			if col.Name != "result_execution_id" {
				columns = append(columns, col)
			}
		}
		table.Columns = columns
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, old, migrate.WithForeignKeys(false)))
	record, err := client.Request.Create().SetProjectID(1).SetChannelID(7).SetModelID("preserved").
		SetStatus("completed").SetRequestBody([]byte(`{}`)).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	loaded := client.Request.GetX(ctx, record.ID)
	require.Nil(t, loaded.ResultExecutionID, "old requests must not acquire an invented winner")
	require.Equal(t, 7, loaded.ChannelID)
	require.Equal(t, "preserved", loaded.ModelID)
	require.Equal(t, "completed", string(loaded.Status))
	require.NoError(t, client.Request.UpdateOneID(record.ID).SetResultExecutionID(11).Exec(ctx))
	require.Equal(t, 11, *client.Request.GetX(ctx, record.ID).ResultExecutionID)
}

func TestRequestBillingSchemaUpgrade(t *testing.T) {
	db, raw, err := openDB("sqlite3", "file:request-billing-upgrade?mode=memory&cache=shared", 1, 1, 0, 0)
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(db, raw)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(t.Context())
	old, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	for _, table := range old {
		if table.Name != "usage_archives" {
			continue
		}
		columns := table.Columns[:0]
		for _, col := range table.Columns {
			if col.Name != "billing_version" {
				columns = append(columns, col)
			}
		}
		table.Columns = columns
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, old, migrate.WithForeignKeys(false)))
	// Insert through the old schema; the new Ent builder already writes defaults.
	_, err = raw.ExecContext(ctx, "INSERT INTO usage_archives (kind, source_id, request_id, project_id, model_id, occurred_at, total_cost, pricing_complete) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", "request", 7, 7, 1, "old-model", time.Now(), 0.03, true)
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	row := client.UsageArchive.Query().Where(usagearchive.SourceIDEQ(7)).OnlyX(ctx)
	require.Zero(t, row.BillingVersion, "legacy snapshots must retain their original billing meaning")
	require.Equal(t, 0.03, *row.TotalCost)
	require.True(t, *row.PricingComplete)
	client.UsageArchive.UpdateOne(row).SetBillingVersion(1).ExecX(ctx)
	require.Equal(t, 1, client.UsageArchive.GetX(ctx, row.ID).BillingVersion)
}
