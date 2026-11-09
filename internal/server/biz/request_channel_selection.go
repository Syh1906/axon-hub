package biz

import (
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

// Legacy requests without dispatch evidence retain the previous rule: only
// their last recorded attempt can qualify, and it must match the request channel.
func sessionDispatchEligible(dialect *sql.DialectBuilder, req *sql.SelectTable, executionID, channelID, selection string) *sql.Predicate {
	dispatch := sqljson.Path("dispatch")
	newer := sql.Table(requestexecution.Table).As("newer_attempt")
	return sql.And(sql.GT(channelID, 0), sql.Or(
		sqljson.ValueEQ(selection, objects.RequestExecutionDispatchStarted, dispatch),
		sql.And(sql.Not(sqljson.HasKey(selection, dispatch)),
			sql.ColumnsEQ(channelID, req.C(request.FieldChannelID)),
			sql.Not(sql.Exists(dialect.Select(newer.C(requestexecution.FieldID)).From(newer).Where(sql.And(
				sql.ColumnsEQ(newer.C(requestexecution.FieldRequestID), req.C(request.FieldID)),
				sql.ColumnsGT(newer.C(requestexecution.FieldID), executionID),
			)))),
		),
	))
}

// FinalRequestChannelAttempt selects the same adopted/dispatched attempt for all request-log views.
func FinalRequestChannelAttempt(db string, req *sql.SelectTable, field string) *sql.Selector {
	dialect := sql.Dialect(db)
	attempt := sql.Table(requestexecution.Table).As("session_attempt")
	req.SetDialect(db)
	attempt.SetDialect(db)
	return dialect.Select(attempt.C(field)).From(attempt).Where(sql.And(
		sql.ColumnsEQ(attempt.C(requestexecution.FieldRequestID), req.C(request.FieldID)),
		sql.ExprP(usageanalytics.RequestResultMatchSQL(req.C, attempt.C, db)),
		sql.Or(sql.ColumnsEQ(attempt.C(requestexecution.FieldID), req.C(request.FieldResultExecutionID)),
			sessionDispatchEligible(dialect, req, attempt.C(requestexecution.FieldID),
				attempt.C(requestexecution.FieldChannelID), attempt.C(requestexecution.FieldSelection))),
	)).OrderBy(sql.Desc(attempt.C(requestexecution.FieldID))).Limit(1)
}

func RequestSessionAttemptMatches(column string, subquery *sql.Selector) *sql.Predicate {
	return sql.P(func(b *sql.Builder) {
		b.Ident(column).WriteOp(sql.OpEQ).Wrap(func(b *sql.Builder) { b.Join(subquery) })
	})
}

func RequestSessionFinished(req *sql.SelectTable) *sql.Predicate {
	return sql.In(req.C(request.FieldStatus), request.StatusCompleted, request.StatusFailed, request.StatusCanceled)
}
