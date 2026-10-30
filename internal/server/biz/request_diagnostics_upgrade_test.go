package biz

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/stretchr/testify/require"
	"slices"
	"testing"
)

func TestRequestDiagnosticsSchemaUpgradeKeepsLegacyData(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:request-diagnostics-upgrade?mode=memory&cache=shared&_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	tables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	added := map[string][]string{"channels": {"website_url"}, "requests": {"client_status_code"}, "request_executions": {"selection", "failure", "retry_decision"}}
	for _, table := range tables {
		table.Columns = slices.DeleteFunc(table.Columns, func(c *schema.Column) bool { return slices.Contains(added[table.Name], c.Name) })
	}
	require.NoError(t, migrate.Create(ctx, client.Schema, tables, migrate.WithForeignKeys(false)))
	_, err = db.ExecContext(ctx, `INSERT INTO channels (type,name,credentials,supported_models,default_test_model) VALUES ('openai','legacy','{}','["model"]','model')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO requests (model_id,request_body,status) VALUES ('model','{}','failed')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO request_executions (request_id,model_id,request_body,status,error_message,response_status_code) VALUES (1,'model','{}','failed','legacy failure',429)`)
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	execution := client.RequestExecution.Query().OnlyX(ctx)
	require.Nil(t, execution.Selection)
	require.Nil(t, execution.Failure)
	require.Empty(t, execution.RetryDecision)
	require.Equal(t, "legacy failure", execution.ErrorMessage)
	require.Equal(t, 429, *execution.ResponseStatusCode)
	require.Nil(t, client.Request.Query().OnlyX(ctx).ClientStatusCode)
	require.Empty(t, client.Channel.Query().OnlyX(ctx).WebsiteURL)
	_, err = db.ExecContext(ctx, `INSERT INTO channels (type,name,credentials,supported_models,default_test_model) VALUES ('anthropic','legacy','{}','["model"]','model')`)
	require.Error(t, err, "channel names remain globally unique")
}
