package datamigrate

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/trace"
)

type V1_0_0_RC1_Fork_1 struct{}

func NewV1_0_0_RC1_Fork_1() DataMigrator   { return &V1_0_0_RC1_Fork_1{} }
func (*V1_0_0_RC1_Fork_1) Version() string { return "v1.0.0-rc1-fork.1" }

// Backfill only the upstream sentinel. A new request may already have updated
// the trace; the write predicate protects that value. Missing history stays
// unknown. Batch updates avoid loading request bodies or querying every trace.
func (*V1_0_0_RC1_Fork_1) Migrate(ctx context.Context, client *ent.Client) error {
	ctx = authz.WithSystemBypass(ctx, "trace-last-request-backfill")
	sentinel := time.Date(1970, 1, 3, 0, 0, 0, 0, time.UTC)
	lastID := 0
	for {
		ids, err := client.Trace.Query().Where(trace.IDGT(lastID), trace.LastRequestAtLT(sentinel)).
			Order(ent.Asc(trace.FieldID)).Limit(256).IDs(ctx)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		lastID = ids[len(ids)-1]
		err = client.Trace.Update().Where(trace.IDIn(ids...), trace.LastRequestAtLT(sentinel)).
			Modify(func(update *sql.UpdateBuilder) {
				rows := sql.Dialect(client.Driver().Dialect()).Table(request.Table)
				matched := sql.And(sql.ColumnsEQ(rows.C(request.FieldTraceID), sql.Table(trace.Table).C(trace.FieldID)),
					sql.ColumnsEQ(rows.C(request.FieldProjectID), sql.Table(trace.Table).C(trace.FieldProjectID)))
				latest := sql.Dialect(client.Driver().Dialect()).Select(sql.Max(rows.C(request.FieldCreatedAt))).From(rows).Where(matched)
				query, args := latest.Query()
				update.Set(trace.FieldLastRequestAt, sql.Expr("("+query+")", args...))
				update.Set(trace.FieldUpdatedAt, sql.Expr(trace.FieldUpdatedAt))
				update.Where(sql.Exists(sql.Dialect(client.Driver().Dialect()).SelectExpr(sql.Expr("1")).From(rows).Where(matched)))
			}).Exec(ctx)
		if err != nil {
			return err
		}
	}
}
