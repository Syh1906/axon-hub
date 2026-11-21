package biz

import (
	"context"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

// For records without an explicit winner, attribute effort only when exactly
// one dispatched attempt matches the request's outcome and adopted channel.
func requestLogResultID(db string, req *sql.SelectTable) sql.Querier {
	attempt := sql.Table(requestexecution.Table).As("effort_attempt")
	req.SetDialect(db)
	attempt.SetDialect(db)
	dispatch := sqljson.Path("dispatch")
	// Client delivery can fail after the provider has completed successfully.
	// In that case the completed attempt on the final channel owns the response.
	delivery := sqljson.ValueEQ(req.C(request.FieldFailure), "delivery", sqljson.Path("stage"))
	outcome := sql.Or(
		sql.And(delivery, sql.EQ(attempt.C(requestexecution.FieldStatus), requestexecution.StatusCompleted)),
		sql.And(sql.Or(sql.Not(sqljson.HasKey(req.C(request.FieldFailure), sqljson.Path("stage"))),
			sqljson.ValueNEQ(req.C(request.FieldFailure), "delivery", sqljson.Path("stage"))),
			sql.ColumnsEQ(attempt.C(requestexecution.FieldStatus), req.C(request.FieldStatus))),
	)
	legacy := sql.Dialect(db).Select(sql.Min(attempt.C(requestexecution.FieldID))).From(attempt).
		Where(sql.And(
			sql.ColumnsEQ(attempt.C(requestexecution.FieldRequestID), req.C(request.FieldID)),
			outcome,
			sql.Or(sql.IsNull(req.C(request.FieldChannelID)),
				sql.EQ(req.C(request.FieldChannelID), 0), sql.ColumnsEQ(attempt.C(requestexecution.FieldChannelID), req.C(request.FieldChannelID))),
			sql.Or(sql.Not(sqljson.HasKey(attempt.C(requestexecution.FieldSelection), dispatch)),
				sqljson.ValueEQ(attempt.C(requestexecution.FieldSelection), objects.RequestExecutionDispatchStarted, dispatch)),
		)).Having(sql.EQ(sql.Count("*"), 1))
	return sql.ExprFunc(func(b *sql.Builder) {
		b.WriteString("COALESCE(").Ident(req.C(request.FieldResultExecutionID)).Comma().Wrap(func(b *sql.Builder) { b.Join(legacy) }).WriteByte(')')
	})
}

// Fetches only the public result metadata for the already-authorized page.
func RequestLogResultExecutions(ctx context.Context, client *ent.Client, ids []int) (map[int]*ent.RequestExecution, error) {
	rows, err := client.RequestExecution.Query().Where(requestexecution.RequestIDIn(ids...), func(q *sql.Selector) {
		req := sql.Table(request.Table).As("effort_owner")
		q.Join(req).On(q.C(requestexecution.FieldRequestID), req.C(request.FieldID)).
			Where(sql.EQ(q.C(requestexecution.FieldID), requestLogResultID(q.Dialect(), req)))
	}).Select(requestexecution.FieldRequestID, requestexecution.FieldReasoningEffort, requestexecution.FieldReasoningEvidence,
		requestexecution.FieldResponseStatusCode).All(ctx)
	if err != nil {
		return nil, err
	}
	results := make(map[int]*ent.RequestExecution, len(ids))
	for _, id := range ids {
		results[id] = nil
	}
	for _, row := range rows {
		results[row.RequestID] = row
	}
	return results, nil
}
