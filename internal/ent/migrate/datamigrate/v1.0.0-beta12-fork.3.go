package datamigrate

import (
	"context"
	"encoding/json"
	"net/http"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/server/biz"
)

type V1_0_0_Beta12_Fork_3 struct{}

func NewV1_0_0_Beta12_Fork_3() DataMigrator   { return &V1_0_0_Beta12_Fork_3{} }
func (*V1_0_0_Beta12_Fork_3) Version() string { return "v1.0.0-beta12-fork.3" }

// JSON is parsed in Go so malformed optional client headers do not depend on
// database-specific JSON casts. Never rewrite statuses, payloads or timestamps.
func (*V1_0_0_Beta12_Fork_3) Migrate(ctx context.Context, client *ent.Client) error {
	ctx = authz.WithSystemBypass(ctx, "request-continuation-identity-migration")
	lastID := 0
	for {
		rows, err := client.Request.Query().Where(request.IDGT(lastID), request.SourceEQ(request.SourceAPI),
			request.StreamEQ(true), request.TraceIDNEQ(0), request.ContinuationKeyIsNil()).
			Select(request.FieldID, request.FieldRequestHeaders).
			Order(ent.Asc(request.FieldID)).Limit(128).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			lastID = row.ID
			var headers http.Header
			if json.Unmarshal(row.RequestHeaders, &headers) != nil {
				continue
			}
			if key := biz.RequestContinuationKey(headers); key != "" {
				if err := client.Request.Update().Where(request.IDEQ(row.ID), request.ContinuationKeyIsNil()).
					SetContinuationKey(key).Modify(func(update *sql.UpdateBuilder) {
					// Preserve the current timestamp atomically, including a concurrent
					// request completion after the migration read this row.
					update.Set(request.FieldUpdatedAt, sql.Expr(request.FieldUpdatedAt))
				}).Exec(ctx); err != nil {
					return err
				}
			}
		}
	}
}
