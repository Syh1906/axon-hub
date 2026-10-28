package biz

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/role"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
)

func TestRoleNameIndexUpgrade(t *testing.T) {
	for _, withOldIndex := range []bool{true, false} {
		name := "旧索引存在"
		if !withOldIndex {
			name = "索引缺失后重试"
		}
		t.Run(name, func(t *testing.T) {
			db, err := sql.Open("sqlite3", "file:role-index-upgrade?mode=memory&cache=shared&_fk=0")
			require.NoError(t, err)
			db.SetMaxOpenConns(1)
			client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))
			t.Cleanup(func() { client.Close() })
			ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			oldTables, err := schema.CopyTables(migrate.Tables)
			require.NoError(t, err)
			for _, table := range oldTables {
				if table.Name != "roles" {
					continue
				}
				if !withOldIndex {
					table.Indexes = slices.DeleteFunc(table.Indexes, func(idx *schema.Index) bool {
						return idx.Name == "roles_by_project_id_name"
					})
					continue
				}
				for _, idx := range table.Indexes {
					if idx.Name == "roles_by_project_id_name" {
						idx.Columns = slices.DeleteFunc(idx.Columns, func(column *schema.Column) bool {
							return column.Name == "deleted_at"
						})
					}
				}
			}
			require.NoError(t, migrate.Create(ctx, client.Schema, oldTables, migrate.WithForeignKeys(false)))
			archived, err := client.Role.Create().SetName("项目管理员").SetDeletedAt(1700000000).Save(ctx)
			require.NoError(t, err)
			active, err := client.Role.Create().SetName("现有角色").Save(ctx)
			require.NoError(t, err)
			if withOldIndex {
				_, err = client.Role.Create().SetName(archived.Name).Save(ctx)
				require.True(t, ent.IsConstraintError(err), "旧索引应复现已删除角色仍占用名称的问题")
			}

			require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false), migrate.WithDropIndex(true)))
			require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false), migrate.WithDropIndex(true)), "重复升级应保持安全")
			recreated, err := client.Role.Create().SetName(archived.Name).Save(ctx)
			require.NoError(t, err, "升级后旧删除记录不应占用名称")
			require.NotEqual(t, archived.ID, recreated.ID)
			_, err = client.Role.Create().SetName(recreated.Name).Save(ctx)
			require.True(t, ent.IsConstraintError(err), "数据库仍须阻止有效角色重名")
			require.Equal(t, active.Name, client.Role.GetX(ctx, active.ID).Name)
			require.Equal(t, archived.DeletedAt, client.Role.GetX(schematype.SkipSoftDelete(ctx), archived.ID).DeletedAt)
			count, err := client.Role.Query().Where(role.NameEQ(archived.Name)).Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, count)
		})
	}
}
