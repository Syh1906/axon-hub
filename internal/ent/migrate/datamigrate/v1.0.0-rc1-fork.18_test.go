package datamigrate_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/migrate/datamigrate"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xversion"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestClientSessionAndEffortUpgrade(t *testing.T) {
	for _, driver := range []string{dialect.SQLite, dialect.Postgres, dialect.MySQL} {
		t.Run(driver, func(t *testing.T) {
			var db *ent.Client
			if driver == dialect.SQLite {
				db = enttest.NewEntClient(t, driver, "file:request-session-upgrade?mode=memory&_fk=0")
			} else {
				key, sqlDriver := "AXONHUB_TEST_SESSION_MYSQL_DSN", "mysql"
				if driver == dialect.Postgres {
					key, sqlDriver = "AXONHUB_TEST_SESSION_PG_DSN", "pgx"
				}
				dsn := os.Getenv(key)
				if dsn == "" {
					t.Skip(key + " not set")
				}
				raw, err := sql.Open(sqlDriver, dsn)
				require.NoError(t, err)
				db = ent.NewClient(ent.Driver(entsql.OpenDB(driver, raw)))
				tables, err := schema.CopyTables(migrate.Tables)
				require.NoError(t, err)
				var needed []*schema.Table
				for _, table := range tables {
					if table.Name == "requests" || table.Name == "request_executions" {
						table.ForeignKeys = nil
						needed = append(needed, table)
					}
				}
				require.NoError(t, migrate.Create(t.Context(), db.Schema, needed, migrate.WithForeignKeys(false)))
			}
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			ctx := authz.WithTestBypass(t.Context())
			h := http.Header{}
			h.Set("X-Codex-Turn-Metadata", `{"session_id":"Session-A"}`)
			raw, err := json.Marshal(h)
			require.NoError(t, err)
			row := db.Request.Create().SetStatus("completed").SetSource("api").SetModelID("m").SetRequestHeaders(raw).SetRequestBody([]byte(`{}`)).SaveX(ctx)
			unlinked := db.Request.Create().SetStatus("completed").SetSource("api").SetModelID("m").SetRequestBody([]byte(`{"_axon_payload":{"state":"expired"}}`)).SaveX(ctx)
			claude := db.Request.Create().SetStatus("completed").SetSource("api").SetFormat("anthropic/messages").SetModelID("m").SetRequestBody([]byte(`{"metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"Claude-S\"}"}}`)).SaveX(ctx)
			exec := db.RequestExecution.Create().SetStatus("completed").SetRequestID(row.ID).SetChannelID(1).SetModelID("m").SetReasoningEffort("high").SetFormat("openai/responses").
				SetRequestBody([]byte(`{"reasoning":{"effort":"high"},"input":[{"type":"configuration_update","reasoning":{"effort":"max"}}]}`)).
				SetResponseChunks([]objects.JSONRawMessage{[]byte(`{"event":"response.completed","data":{"response":{"reasoning":{"effort":"low"}}}}`)}).SaveX(ctx)
			gemini := db.RequestExecution.Create().SetStatus("completed").SetRequestID(claude.ID).SetChannelID(1).SetModelID("g").SetFormat("gemini/contents").
				SetRequestBody([]byte(`{"generationConfig":{"thinkingConfig":{"thinkingBudget":3072}}}`)).SaveX(ctx)
			db.RequestExecution.Create().SetStatus("failed").SetRequestID(row.ID).SetChannelID(1).SetModelID("m").
				SetRequestBody([]byte(`{}`)).SaveX(ctx)
			storedAt := db.Request.GetX(ctx, row.ID).UpdatedAt
			m := datamigrate.NewV1_0_0_RC1_Fork_18()
			require.NoError(t, m.Migrate(ctx, db))
			require.NoError(t, m.Migrate(ctx, db))
			got := db.Request.GetX(ctx, row.ID)
			require.Equal(t, "Session-A", *got.ClientSessionID)
			require.True(t, storedAt.Equal(got.UpdatedAt))
			require.False(t, db.Request.Query().Where(request.IDEQ(row.ID), request.ClientSessionIDEQ("session-a")).ExistX(ctx))
			require.Equal(t, "3072", db.RequestExecution.GetX(ctx, gemini.ID).ReasoningEvidence.Requested)
			results, err := biz.RequestLogResultExecutions(ctx, db, []int{row.ID})
			require.NoError(t, err)
			require.NotNil(t, results[row.ID])
			require.Equal(t, exec.ID, results[row.ID].ID)
			require.Nil(t, db.Request.GetX(ctx, unlinked.ID).ClientSessionID)
			require.Equal(t, "Claude-S", *db.Request.GetX(ctx, claude.ID).ClientSessionID)
			require.Equal(t, &objects.RequestReasoningEvidence{Requested: "max", Reported: "low"}, db.RequestExecution.GetX(ctx, exec.ID).ReasoningEvidence)
		})
	}
	previous, _ := xversion.Parse("v1.0.0-rc1-fork.17")
	current, err := xversion.Parse(datamigrate.NewV1_0_0_RC1_Fork_18().Version())
	require.NoError(t, err)
	next, _ := xversion.Parse("v1.0.0-rc2")
	require.True(t, previous.LessThan(current) && current.LessThan(next))
}
