package datamigrate

import (
	"context"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/server/biz"
)

type V1_0_0_RC1_Fork_9 struct{}

func NewV1_0_0_RC1_Fork_9() DataMigrator   { return &V1_0_0_RC1_Fork_9{} }
func (*V1_0_0_RC1_Fork_9) Version() string { return "v1.0.0-rc1-fork.9" }

func (*V1_0_0_RC1_Fork_9) Migrate(ctx context.Context, client *ent.Client) error {
	ctx = authz.WithSystemBypass(ctx, "language-preference-migration")
	ctx = schematype.SkipSoftDelete(ent.NewContext(ctx, client))
	service := &biz.AbstractService{}
	return service.RunInTransaction(ctx, func(ctx context.Context) error {
		db := ent.FromContext(ctx)
		const marker = "migration_rc1_fork9_browser_language"
		done, err := db.System.Query().Where(system.KeyEQ(marker)).Exist(ctx)
		if err != nil || done {
			return err
		}
		// The marker and reset commit together. Even development builds whose version
		// has not advanced must not reset a language chosen after the first upgrade.
		if _, err := db.System.Create().SetKey(marker).SetValue("true").Save(ctx); err != nil {
			return err
		}
		return db.User.Update().SetPreferLanguage("auto").Modify(func(update *sql.UpdateBuilder) {
			update.Set(user.FieldUpdatedAt, sql.Expr(user.FieldUpdatedAt))
		}).Exec(ctx)
	})
}
