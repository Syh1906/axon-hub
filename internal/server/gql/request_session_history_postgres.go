package gql

import (
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/trace"
)

// PostgreSQL 对有界会话历史的索引查找更快；保留 OFFSET 0 约束，
// 避免规划器把执行子查询提前到全项目历史上。SQLite 使用批量窗口查询。
func requestSessionHistoryPostgresQuery(q *sql.Selector) {
	q.Where(sql.GT(q.C(requestexecution.FieldChannelID), 0))
	dialect := sql.Dialect(q.Dialect())
	current := sql.Table(request.Table).As("current_request")
	currentTrace := sql.Table(trace.Table).As("current_trace")
	prior := sql.Table(request.Table).As("prior_request")
	priorTrace := sql.Table(trace.Table).As("prior_trace")
	channel := q.C(requestexecution.FieldChannelID)
	q.Join(current).On(q.C(requestexecution.FieldRequestID), current.C(request.FieldID)).
		LeftJoin(currentTrace).On(current.C(request.FieldTraceID), currentTrace.C(trace.FieldID))
	q.Where(sql.And(sessionRequestFinished(current),
		sessionAttemptMatches(q.C(requestexecution.FieldID), finalSessionAttempt(dialect, current, requestexecution.FieldID)),
	))
	sameSession := sql.Or(
		sql.ColumnsEQ(priorTrace.C(trace.FieldID), current.C(request.FieldTraceID)),
		sql.And(sql.GT(currentTrace.C(trace.FieldThreadID), 0), sql.ColumnsEQ(priorTrace.C(trace.FieldThreadID), currentTrace.C(trace.FieldThreadID))),
	)
	finalPriorChannel := finalSessionAttempt(dialect, prior, requestexecution.FieldChannelID)
	boundedHistory := dialect.Select(prior.C(request.FieldID), prior.C(request.FieldChannelID)).From(prior).
		Join(priorTrace).On(prior.C(request.FieldTraceID), priorTrace.C(trace.FieldID)).Where(sql.And(
		sql.ColumnsEQ(prior.C(request.FieldProjectID), current.C(request.FieldProjectID)),
		sql.ColumnsLT(prior.C(request.FieldID), current.C(request.FieldID)),
		sameSession, sessionRequestFinished(prior),
	))
	history := boundedHistory
	if q.Dialect() == "postgres" {
		// 先筛出同一会话的历史，阻止 PostgreSQL 将昂贵的最终执行
		// 子查询提前到全项目历史上执行；OFFSET 0 不截断任何记录。
		history = dialect.Select().From(boundedHistory.Offset(0).As("prior_request"))
	} else {
		history.Select()
	}
	previous := history.Clone().AppendSelectExpr(sql.ExprFunc(func(b *sql.Builder) {
		b.Wrap(func(b *sql.Builder) { b.Join(finalPriorChannel) })
	})).Where(sql.P(func(b *sql.Builder) {
		b.Wrap(func(b *sql.Builder) { b.Join(finalPriorChannel) }).WriteOp(sql.OpNotNull)
	})).OrderBy(sql.Desc(prior.C(request.FieldID))).Limit(1)
	usedBefore := history.Select(prior.C(request.FieldID)).Where(sessionAttemptMatches(channel, finalPriorChannel))
	q.Select(q.C(requestexecution.FieldID), sql.As(channel, "channel")).
		AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) { b.Wrap(func(b *sql.Builder) { b.Join(previous) }) }), "previous_channel").
		AppendSelectExprAs(sql.Exists(usedBefore), "used_before")
}
