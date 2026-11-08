package datamigrate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/migrate/datamigrate"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xversion"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
	"github.com/stretchr/testify/require"
)

func TestRequestContinuationIdentityUpgrade(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:continuation-upgrade?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := db.Project.Create().SetName("continuation").SaveX(ctx)
	headers := http.Header{}
	headers.Set(codex.TurnMetadataHeader, `{"session_id":"s","thread_id":"t","turn_id":"turn","request_kind":"turn"}`)
	raw, err := json.Marshal(headers)
	require.NoError(t, err)
	at := time.Date(2026, 11, 7, 18, 25, 42, 0, time.UTC)
	var ids []int
	// Cross a migration page boundary; malformed and absent headers are harmless.
	for i := 0; i < 130; i++ {
		create := db.Request.Create().SetProjectID(project.ID).SetAPIKeyID(1).SetTraceID(1).
			SetModelID("model").SetStream(true).SetSource("api").SetStatus("canceled").
			SetRequestBody(objects.JSONRawMessage(`{}`)).SetCreatedAt(at).SetUpdatedAt(at.Add(time.Second)).SetRequestHeaders(raw)
		if i == 128 {
			create.SetRequestHeaders(objects.JSONRawMessage(`{"X-Codex-Turn-Metadata":["invalid-json"]}`))
		}
		if i == 129 {
			create.SetContinuationKey("already-normalized")
		}
		ids = append(ids, create.SaveX(ctx).ID)
	}
	concurrentAt := at.Add(2 * time.Second)
	completedDuringBackfill := false
	db.Request.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if !completedDuringBackfill {
				completedDuringBackfill = true
				_, err := db.Request.UpdateOneID(ids[0]).SetStatus("completed").SetUpdatedAt(concurrentAt).Save(ctx)
				if err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
	migration := datamigrate.NewV1_0_0_Beta12_Fork_3()
	require.NoError(t, migration.Migrate(ctx, db))
	require.NoError(t, migration.Migrate(ctx, db), "the backfill is idempotent")
	for i, id := range ids {
		row := db.Request.GetX(ctx, id)
		if i == 0 {
			require.True(t, concurrentAt.Equal(row.UpdatedAt))
			require.Equal(t, "completed", string(row.Status))
		} else {
			require.True(t, at.Add(time.Second).Equal(row.UpdatedAt))
			require.Equal(t, "canceled", string(row.Status))
		}
		if i == 128 {
			require.Empty(t, row.ContinuationKey)
		} else if i == 129 {
			require.Equal(t, "already-normalized", row.ContinuationKey)
		} else {
			require.Equal(t, biz.RequestContinuationKey(headers), row.ContinuationKey)
		}
	}
	previous, err := xversion.Parse("v1.0.0-beta12-fork.2")
	require.NoError(t, err)
	current, err := xversion.Parse(migration.Version())
	require.NoError(t, err)
	next, err := xversion.Parse("v1.0.0-beta13")
	require.NoError(t, err)
	require.True(t, previous.LessThan(current))
	require.True(t, current.LessThan(next))
}

func TestRequestContinuationSchemaUpgrade(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:continuation-schema-upgrade?mode=memory&_fk=0")
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := db.Project.Create().SetName("upgrade").SaveX(ctx)
	at := time.Date(2026, 11, 7, 18, 25, 42, 0, time.UTC)
	headers := http.Header{}
	headers.Set(codex.TurnMetadataHeader, `{"session_id":"s","thread_id":"t","turn_id":"turn","request_kind":"turn"}`)
	raw, err := json.Marshal(headers)
	require.NoError(t, err)
	row := db.Request.Create().SetProjectID(project.ID).SetAPIKeyID(1).SetTraceID(1).
		SetModelID("model").SetStream(true).SetSource("api").SetStatus("canceled").
		SetRequestBody(objects.JSONRawMessage(`{}`)).SetCreatedAt(at).SetUpdatedAt(at).
		SetRequestHeaders(raw).SaveX(ctx)
	var result sql.Result
	// Reconstruct the pre-upgrade table, retaining its real historical row.
	require.NoError(t, db.Driver().Exec(ctx, "DROP INDEX requests_by_trace_continuation_created_at", []any{}, &result))
	require.NoError(t, db.Driver().Exec(ctx, "ALTER TABLE requests DROP COLUMN continuation_key", []any{}, &result))
	require.NoError(t, db.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, db.Schema.Create(ctx, migrate.WithForeignKeys(false)))
	require.NoError(t, datamigrate.NewV1_0_0_Beta12_Fork_3().Migrate(ctx, db))
	upgraded := db.Request.GetX(ctx, row.ID)
	require.Equal(t, biz.RequestContinuationKey(headers), upgraded.ContinuationKey)
	require.Equal(t, row.Status, upgraded.Status)
	require.True(t, upgraded.UpdatedAt.Equal(at))
	require.Equal(t, row.RequestHeaders, upgraded.RequestHeaders)
}
