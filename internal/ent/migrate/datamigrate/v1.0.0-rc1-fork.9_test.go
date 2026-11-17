package datamigrate_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/migrate/datamigrate"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/pkg/xversion"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestBrowserLanguageUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite3", dialect.Postgres, dialect.MySQL} {
		t.Run(driver, func(t *testing.T) {
			db := languageUpgradeClient(t, driver)
			ctx := schematype.SkipSoftDelete(authz.WithTestBypass(ent.NewContext(t.Context(), db)))
			before := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			var users []*ent.User
			for i, language := range []string{"en", "zh", "zh-CN", "auto"} {
				users = append(users, db.User.Create().SetEmail(fmt.Sprintf("language-%d@example.test", i)).
					SetPassword("fixture").SetPreferLanguage(language).SetIsOwner(i == 0).
					SetScopes([]string{"read_requests"}).SetUpdatedAt(before).SaveX(ctx))
			}
			db.User.UpdateOne(users[2]).SetDeletedAt(123).SaveX(ctx)
			users[2] = db.User.GetX(ctx, users[2].ID)
			db.System.Create().SetKey(biz.SystemKeyInitialized).SetValue("true").SaveX(ctx)
			db.System.Create().SetKey(biz.SystemKeyVersion).SetValue("v1.0.0-rc1-fork.8").SaveX(ctx)
			// Exercise the registered upgrade path, not only a standalone migration.
			require.NoError(t, datamigrate.NewMigrator(db).Run(ctx))
			for _, old := range users {
				row := db.User.GetX(ctx, old.ID)
				require.Equal(t, "auto", row.PreferLanguage)
				require.Equal(t, old.IsOwner, row.IsOwner)
				require.Equal(t, old.Scopes, row.Scopes)
				require.Equal(t, old.DeletedAt, row.DeletedAt)
				require.True(t, old.UpdatedAt.Equal(row.UpdatedAt))
			}
			db.User.UpdateOne(users[0]).SetPreferLanguage("zh").SaveX(ctx)
			db.User.UpdateOne(users[1]).SetPreferLanguage("en").SaveX(ctx)
			fresh := db.User.Create().SetEmail("new@example.test").SetPassword("fixture").SaveX(ctx)
			require.Equal(t, "auto", fresh.PreferLanguage)
			require.NoError(t, datamigrate.NewV1_0_0_RC1_Fork_9().Migrate(ctx, db))
			require.NoError(t, datamigrate.NewMigrator(db).Run(ctx))
			require.Equal(t, "zh", db.User.GetX(ctx, users[0].ID).PreferLanguage)
			require.Equal(t, "en", db.User.GetX(ctx, users[1].ID).PreferLanguage)
		})
	}
}

func TestBrowserLanguageUpgradeRollback(t *testing.T) {
	db := languageUpgradeClient(t, "sqlite3")
	ctx := authz.WithTestBypass(t.Context())
	old := db.User.Create().SetEmail("rollback@example.test").SetPassword("fixture").SetPreferLanguage("zh").SaveX(ctx)
	fail := true
	db.User.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if fail && m.Op().Is(ent.OpUpdate) {
				return nil, errors.New("fixture update failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	migration := datamigrate.NewV1_0_0_RC1_Fork_9()
	require.ErrorContains(t, migration.Migrate(ctx, db), "fixture update failure")
	require.Equal(t, "zh", db.User.GetX(ctx, old.ID).PreferLanguage)
	require.Zero(t, db.System.Query().CountX(ctx), "failed reset must not commit the marker")
	fail = false
	require.NoError(t, migration.Migrate(ctx, db))
	require.Equal(t, "auto", db.User.GetX(ctx, old.ID).PreferLanguage)
}

func TestBrowserLanguageMigrationVersionOrder(t *testing.T) {
	versions := []string{"v1.0.0-rc1", "v1.0.0-rc1-fork.1", "v1.0.0-rc1-fork.8", datamigrate.NewV1_0_0_RC1_Fork_9().Version(), "v1.0.0-rc2", "v1.0.0"}
	for i := 1; i < len(versions); i++ {
		a, err := xversion.Parse(versions[i-1])
		require.NoError(t, err)
		b, err := xversion.Parse(versions[i])
		require.NoError(t, err)
		require.True(t, a.LessThan(b), versions[i-1]+" < "+versions[i])
	}
}

// External DSNs must refer to dedicated disposable local databases. Only these
// two tables are needed; no shared development or production database is used.
func languageUpgradeClient(t *testing.T, driver string) *ent.Client {
	t.Helper()
	if driver == "sqlite3" {
		db := enttest.NewEntClient(t, driver, "file:ent?mode=memory&_fk=0")
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		return db
	}
	key, sqlDriver := "AXONHUB_TEST_LANGUAGE_MYSQL_DSN", "mysql"
	if driver == dialect.Postgres {
		key, sqlDriver = "AXONHUB_TEST_LANGUAGE_PG_DSN", "pgx"
	}
	dsn := os.Getenv(key)
	if dsn == "" {
		t.Skip(key + " not set")
	}
	raw, err := sql.Open(sqlDriver, dsn)
	require.NoError(t, err)
	db := ent.NewClient(ent.Driver(entsql.OpenDB(driver, raw)))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	tables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	kept := tables[:0]
	for _, table := range tables {
		if table.Name == "users" || table.Name == "systems" {
			kept = append(kept, table)
		}
	}
	require.NoError(t, migrate.Create(t.Context(), db.Schema, kept, migrate.WithForeignKeys(false)))
	return db
}
