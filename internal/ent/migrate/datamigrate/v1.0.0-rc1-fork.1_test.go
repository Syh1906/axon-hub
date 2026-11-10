package datamigrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/migrate/datamigrate"
	"github.com/looplj/axonhub/internal/ent/trace"
	_ "github.com/looplj/axonhub/internal/pkg/sqlite"
	"github.com/looplj/axonhub/internal/pkg/xversion"
)

func TestTraceLastRequestUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite3", dialect.Postgres, dialect.MySQL} {
		t.Run(driver, func(t *testing.T) { checkTraceLastRequestUpgrade(t, traceUpgradeClient(t, driver)) })
	}
}

func checkTraceLastRequestUpgrade(t *testing.T, db *ent.Client) {
	ctx := authz.WithTestBypass(context.Background())
	sentinel := time.Date(1970, 1, 2, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	newest := before.Add(time.Hour)
	ids := []int{}
	for i := 0; i < 257; i++ {
		tr := db.Trace.Create().SetProjectID(1).SetTraceID(fmt.Sprint("legacy-", i)).SetLastRequestAt(sentinel).SetUpdatedAt(before).SaveX(ctx)
		ids = append(ids, tr.ID)
		for _, at := range []time.Time{before, newest} {
			db.Request.Create().SetProjectID(1).SetTraceID(tr.ID).SetAPIKeyID(1).SetSource("api").SetStatus("completed").SetStream(false).SetModelID("fixture").SetRequestBody([]byte(`{}`)).SetCreatedAt(at).SaveX(ctx)
		}
	}
	unknown := db.Trace.Create().SetProjectID(1).SetTraceID("no-history").SetLastRequestAt(sentinel).SaveX(ctx)
	// A cross-project row must not supply this trace's historical activity.
	db.Request.Create().SetProjectID(2).SetTraceID(unknown.ID).SetAPIKeyID(1).SetSource("api").SetStatus("completed").SetStream(false).SetModelID("fixture").SetRequestBody([]byte(`{}`)).SetCreatedAt(newest).SaveX(ctx)
	current := db.Trace.Create().SetProjectID(1).SetTraceID("already-current").SetLastRequestAt(newest.Add(time.Hour)).SaveX(ctx)
	migration := datamigrate.NewV1_0_0_RC1_Fork_1()
	require.NoError(t, migration.Migrate(ctx, db))
	require.NoError(t, migration.Migrate(ctx, db))
	for _, id := range ids {
		row := db.Trace.GetX(ctx, id)
		require.True(t, newest.Equal(row.LastRequestAt))
		require.True(t, before.Equal(row.UpdatedAt))
	}
	require.True(t, sentinel.Equal(db.Trace.GetX(ctx, unknown.ID).LastRequestAt))
	require.True(t, current.LastRequestAt.Equal(db.Trace.GetX(ctx, current.ID).LastRequestAt))
	versions := []string{"v1.0.0-beta12-fork.5", "v1.0.0-rc1", migration.Version(), "v1.0.0-rc2", "v1.0.0"}
	for i := 1; i < len(versions); i++ {
		a, err := xversion.Parse(versions[i-1])
		require.NoError(t, err)
		b, err := xversion.Parse(versions[i])
		require.NoError(t, err)
		require.True(t, a.LessThan(b), versions[i-1]+" < "+versions[i])
	}
}

// Optional dialect tests always allocate their own schema/database; supplied
// DSNs must identify an authorized disposable test server.
func traceUpgradeClient(t *testing.T, driver string) *ent.Client {
	t.Helper()
	if driver == "sqlite3" {
		raw, err := sql.Open(driver, "file:trace-last-request-upgrade?mode=memory&_fk=0")
		require.NoError(t, err)
		raw.SetMaxOpenConns(1)
		return createTraceUpgradeSchema(t, driver, raw)
	}
	var raw *sql.DB
	name := "trace_rc1_" + uuid.NewString()[:8]
	if driver == dialect.Postgres {
		dsn := os.Getenv("AXONHUB_TEST_TRACE_PG_DSN")
		if dsn == "" {
			t.Skip("AXONHUB_TEST_TRACE_PG_DSN not set")
		}
		cfg, err := pgx.ParseConfig(dsn)
		require.NoError(t, err)
		admin := stdlib.OpenDB(*cfg.Copy())
		t.Cleanup(func() { require.NoError(t, admin.Close()) })
		_, err = admin.ExecContext(t.Context(), fmt.Sprintf("CREATE SCHEMA %q", name))
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := admin.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA %q CASCADE", name))
			require.NoError(t, err)
		})
		cfg.RuntimeParams["search_path"] = name
		raw = stdlib.OpenDB(*cfg)
	} else {
		dsn := os.Getenv("AXONHUB_TEST_TRACE_MYSQL_DSN")
		if dsn == "" {
			t.Skip("AXONHUB_TEST_TRACE_MYSQL_DSN not set")
		}
		cfg, err := mysql.ParseDSN(dsn)
		require.NoError(t, err)
		admin, err := sql.Open("mysql", cfg.FormatDSN())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, admin.Close()) })
		_, err = admin.ExecContext(t.Context(), "CREATE DATABASE "+name)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name)
			require.NoError(t, err)
		})
		cfg.DBName, cfg.ParseTime = name, true
		raw, err = sql.Open("mysql", cfg.FormatDSN())
		require.NoError(t, err)
	}
	return createTraceUpgradeSchema(t, driver, raw)
}

func createTraceUpgradeSchema(t *testing.T, driver string, raw *sql.DB) *ent.Client {
	t.Helper()
	db := ent.NewClient(ent.Driver(entsql.OpenDB(driver, raw)))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(t.Context())
	old, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	kept := old[:0]
	for _, table := range old {
		if table.Name == "plugins" || table.Name == "plugin_storages" {
			continue
		}
		if table.Name == trace.Table {
			columns := table.Columns[:0]
			for _, col := range table.Columns {
				if col.Name != trace.FieldLastRequestAt {
					columns = append(columns, col)
				}
			}
			table.Columns = columns
			indices := table.Indexes[:0]
			for _, index := range table.Indexes {
				keep := true
				for _, col := range index.Columns {
					if col.Name == trace.FieldLastRequestAt {
						keep = false
					}
				}
				if keep {
					indices = append(indices, index)
				}
			}
			table.Indexes = indices
		}
		kept = append(kept, table)
	}
	require.NoError(t, migrate.Create(ctx, db.Schema, kept, migrate.WithForeignKeys(false)))
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	query, args := entsql.Dialect(driver).Insert(trace.Table).
		Columns(trace.FieldProjectID, trace.FieldTraceID, trace.FieldStatus, trace.FieldCreatedAt, trace.FieldUpdatedAt).
		Values(1, "pre-rc1-schema", "retained", at, at).Query()
	var result entsql.Result
	require.NoError(t, db.Driver().Exec(ctx, query, args, &result))
	for range 2 {
		require.NoError(t, db.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	}
	row := db.Trace.Query().Where(trace.TraceIDEQ("pre-rc1-schema")).OnlyX(ctx)
	require.Equal(t, trace.StatusRetained, row.Status)
	require.True(t, at.Equal(row.CreatedAt))
	require.True(t, row.LastRequestAt.Before(time.Date(1970, 1, 3, 0, 0, 0, 0, time.UTC)), "old trace receives the unknown-time sentinel")
	return db
}
