package biz

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/objects"
)

func TestModelRoutingSchemaUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name         string
		missingIndex bool
	}{
		{name: "old_index_present"},
		{name: "old_index_missing", missingIndex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite3", "file:model-routing-upgrade-"+tc.name+"?mode=memory&cache=shared&_fk=0")
			require.NoError(t, err)
			db.SetMaxOpenConns(1)
			client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
			t.Cleanup(func() { client.Close() })
			ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			oldTables, err := schema.CopyTables([]*schema.Table{migrate.ModelsTable})
			require.NoError(t, err)
			old := oldTables[0]
			old.Columns = slices.DeleteFunc(old.Columns, func(column *schema.Column) bool {
				return column.Name == "match_type" || column.Name == "match_priority" || column.Name == "public_model_ids"
			})
			for _, index := range old.Indexes {
				if index.Name == "models_by_match_identity" {
					index.Name = "models_by_model_id"
					index.Columns = slices.DeleteFunc(index.Columns, func(column *schema.Column) bool { return column.Name == "match_type" })
				}
			}
			if tc.missingIndex {
				old.Indexes = slices.DeleteFunc(old.Indexes, func(index *schema.Index) bool { return index.Name == "models_by_model_id" })
			}
			require.NoError(t, migrate.Create(ctx, client.Schema, oldTables))
			_, err = db.ExecContext(ctx, `INSERT INTO models (developer, model_id, name, type, icon, "group", model_card, settings, status) VALUES ('openai', 'gpt-legacy', 'Legacy', 'chat', 'openai', 'test', '{}', '{}', 'enabled')`)
			require.NoError(t, err)
			require.NoError(t, migrate.Create(ctx, client.Schema, []*schema.Table{migrate.ModelsTable}, migrate.WithDropIndex(true), migrate.WithDropColumn(true)))
			legacy := client.Model.Query().OnlyX(ctx)
			require.Equal(t, objects.ModelMatchExact, legacy.MatchType)
			require.Equal(t, 100, legacy.MatchPriority)
			require.Nil(t, legacy.PublicModelIds)
			require.Equal(t, []string{"gpt-legacy"}, PublicModelIDs(legacy))
			_, err = client.Model.Create().SetDeveloper("openai").SetModelID("gpt-legacy").SetMatchType(objects.ModelMatchPrefix).
				SetName("Prefix").SetIcon("openai").SetGroup("test").SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{}).Save(ctx)
			require.NoError(t, err, "the old unique model ID index must have been replaced")
			_, err = client.Model.Create().SetDeveloper("openai").SetModelID("gpt-legacy").SetMatchType(objects.ModelMatchExact).
				SetName("Duplicate").SetIcon("openai").SetGroup("test").SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{}).Save(ctx)
			require.Error(t, err, "same type and expression remain unique")

			require.NoError(t, client.Model.DeleteOne(legacy).Exec(ctx))
			deleted := client.Model.GetX(schematype.SkipSoftDelete(ctx), legacy.ID)
			require.Positive(t, deleted.DeletedAt, "the old row must remain soft-deleted")
			replacement, err := client.Model.Create().SetDeveloper("openai").SetModelID("gpt-legacy").SetMatchType(objects.ModelMatchExact).
				SetName("Legacy").SetIcon("openai").SetGroup("test").SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{}).Save(ctx)
			require.NoError(t, err, "a deleted identity and name must be reusable")
			require.NotEqual(t, legacy.ID, replacement.ID)
			require.Zero(t, replacement.DeletedAt)
		})
	}
}
