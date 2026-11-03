package biz

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/objects"
)

func TestResponsesOriginSchemaUpgrade(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			db, err := sql.Open("sqlite3", "file:responses-origin-upgrade?mode=memory&cache=shared&_fk=0")
			require.NoError(t, err)
			db.SetMaxOpenConns(1)
			client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			ctx := authz.WithTestBypass(t.Context())
			tables, err := schema.CopyTables(migrate.Tables)
			require.NoError(t, err)
			for _, table := range tables {
				if table.Name == "requests" {
					table.Columns = slices.DeleteFunc(table.Columns, func(column *schema.Column) bool { return column.Name == "failure" })
				}
				if table.Name == "responses_origins" {
					table.Indexes = nil
				}
			}
			if !partial {
				tables = slices.DeleteFunc(tables, func(table *schema.Table) bool { return table.Name == "responses_origins" })
			}
			require.NoError(t, migrate.Create(ctx, client.Schema, tables, migrate.WithForeignKeys(false)))
			_, err = db.ExecContext(ctx, `INSERT INTO requests (model_id,request_body,status,external_id) VALUES ('legacy','{}','completed','resp_legacy')`)
			require.NoError(t, err)
			for range 2 {
				require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
			}
			legacy := client.Request.Query().OnlyX(ctx)
			require.Nil(t, legacy.Failure)
			require.Equal(t, "resp_legacy", legacy.ExternalID)
			require.NoError(t, client.Request.UpdateOneID(legacy.ID).SetFailure(&objects.RequestFailure{Stage: "routing", Message: "fixture"}).Exec(ctx))
			insert := func(fingerprint string) error {
				return client.ResponsesOrigin.Create().SetProjectID(1).SetAPIKeyID(0).SetFingerprint(fingerprint).
					SetOriginRef(responsesDigest("origin")).SetChannelID(1).SetKeyRef("").SetKeyHint("").SetUpstreamRef("").
					SetRequestID(legacy.ID).SetExecutionID(1).Exec(ctx)
			}
			require.NoError(t, insert(responsesDigest("one")))
			require.True(t, ent.IsConstraintError(insert(responsesDigest("one"))), "the upgraded unique index must reject duplicate origin evidence")
			require.NoError(t, insert(responsesDigest("two")))
		})
	}
}
