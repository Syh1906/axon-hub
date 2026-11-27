package biz

import (
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/trace"
)

// 优先在相邻的一小段主键中查上一条已采用的执行。空闲会话可能跨过这个范围，
// 此时继续查边界之前的全部历史。这个宽度只影响查询成本。
const recentSessionRequestIDs = 128

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
	q.Where(sql.And(RequestSessionFinished(current),
		RequestSessionAttemptMatches(q.C(requestexecution.FieldID), FinalRequestChannelAttempt(q.Dialect(), current, requestexecution.FieldID)),
	))
	sameSession := sql.Or(
		sql.ColumnsEQ(priorTrace.C(trace.FieldID), current.C(request.FieldTraceID)),
		sql.And(sql.GT(currentTrace.C(trace.FieldThreadID), 0), sql.ColumnsEQ(priorTrace.C(trace.FieldThreadID), currentTrace.C(trace.FieldThreadID))),
	)
	finalPriorChannel := FinalRequestChannelAttempt(q.Dialect(), prior, requestexecution.FieldChannelID)
	boundedHistory := dialect.Select(prior.Columns(request.FieldID, request.FieldChannelID,
		request.FieldResultExecutionID, request.FieldStatus, request.FieldFailure)...).From(prior).
		Join(priorTrace).On(prior.C(request.FieldTraceID), priorTrace.C(trace.FieldID)).Where(sql.And(
		sql.ColumnsEQ(prior.C(request.FieldProjectID), current.C(request.FieldProjectID)),
		sql.ColumnsLT(prior.C(request.FieldID), current.C(request.FieldID)),
		sameSession, RequestSessionFinished(prior),
	))
	previousChannel := func(bound sql.Op) *sql.Selector {
		candidates := boundedHistory.Clone().Where(sql.P(func(b *sql.Builder) {
			b.Ident(prior.C(request.FieldID)).WriteOp(bound).Ident(current.C(request.FieldID)).
				WriteString(" - ").Arg(recentSessionRequestIDs)
		})).OrderBy(sql.Desc(prior.C(request.FieldID))).Offset(0).As("prior_request")
		// OFFSET 0 保留先选请求、再读采用渠道的执行顺序，避免给整段历史逐条查执行。
		adopted := dialect.Select(candidates.C(request.FieldID)).From(candidates).
			AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
				b.Wrap(func(b *sql.Builder) { b.Join(finalPriorChannel) })
			}), "channel").Offset(0).As("adopted")
		return dialect.Select(adopted.C("channel")).From(adopted).Where(sql.NotNull(adopted.C("channel"))).
			OrderBy(sql.Desc(adopted.C(request.FieldID))).Limit(1)
	}
	recent, older := previousChannel(sql.OpGTE), previousChannel(sql.OpLT)
	previous := dialect.Select().AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
		b.WriteString("COALESCE(").Wrap(func(b *sql.Builder) { b.Join(recent) }).Comma().
			Wrap(func(b *sql.Builder) { b.Join(older) }).WriteByte(')')
	}), "previous_channel").Offset(0).As("predecessor")
	history := boundedHistory.Offset(0).As("prior_request")
	usedBefore := dialect.Select(history.C(request.FieldID)).From(history).Where(RequestSessionAttemptMatches(channel, finalPriorChannel))
	previousChannelID := previous.C("previous_channel")
	state := dialect.SelectExpr(requestChannelSessionState(previousChannelID, channel,
		sql.And(sql.NotNull(previousChannelID), sql.Exists(usedBefore)))).From(previous)
	q.Select(q.C(requestexecution.FieldID)).AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
		b.Wrap(func(b *sql.Builder) { b.Join(state) })
	}), "session_state")
}
