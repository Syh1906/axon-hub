package gql

import (
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/trace"
)

// 同页的会话历史只展开一次。窗口计算在筛选当前执行之前完成，保留页外历史。
func requestSessionHistoryQuery(q *sql.Selector, ids []int) {
	if q.Dialect() == "postgres" {
		requestSessionHistoryPostgresQuery(q)
		return
	}
	d := sql.Dialect(q.Dialect())
	target := sql.Table(requestexecution.Table).As("target_attempt")
	targetRequest := sql.Table(request.Table).As("target_request")
	targetTrace := sql.Table(trace.Table).As("target_trace")
	threadKey := func(t *sql.SelectTable) string { return "COALESCE(" + t.C(trace.FieldThreadID) + ", 0)" }
	traceKey := func(t *sql.SelectTable) string {
		return "CASE WHEN " + threadKey(t) + " > 0 THEN 0 ELSE " + t.C(trace.FieldID) + " END"
	}
	scopes := d.Select(targetRequest.C(request.FieldProjectID)).From(target).
		Join(targetRequest).On(target.C(requestexecution.FieldRequestID), targetRequest.C(request.FieldID)).
		Join(targetTrace).On(targetRequest.C(request.FieldTraceID), targetTrace.C(trace.FieldID)).
		Where(sql.InInts(target.C(requestexecution.FieldID), ids...)).
		AppendSelectAs(threadKey(targetTrace), "session_thread").AppendSelectAs(traceKey(targetTrace), "session_trace").
		AppendSelectAs(sql.Max(targetRequest.C(request.FieldID)), "last_request_id").
		GroupBy(targetRequest.C(request.FieldProjectID), threadKey(targetTrace), traceKey(targetTrace))
	scope := sql.Table("page_sessions")
	historical := sql.Table(request.Table).As("history_request")
	historicalTrace := sql.Table(trace.Table).As("history_trace")
	final := sql.Table(requestexecution.Table).As("history_attempt")
	history := d.Select(historical.C(request.FieldProjectID)).From(historical).
		Join(historicalTrace).On(historical.C(request.FieldTraceID), historicalTrace.C(trace.FieldID)).
		Join(scope).OnP(sql.And(
		sql.ColumnsEQ(historical.C(request.FieldProjectID), scope.C(request.FieldProjectID)),
		sql.ExprP(threadKey(historicalTrace)+" = "+scope.C("session_thread")),
		sql.ExprP(traceKey(historicalTrace)+" = "+scope.C("session_trace")),
		sql.ColumnsLTE(historical.C(request.FieldID), scope.C("last_request_id")),
	)).
		Join(final).OnP(sql.EQ(final.C(requestexecution.FieldID), finalSessionAttempt(d, historical, requestexecution.FieldID))).
		Where(sessionRequestFinished(historical)).
		AppendSelectAs(historical.C(request.FieldID), "request_id").
		AppendSelectAs(final.C(requestexecution.FieldID), "execution_id").
		AppendSelectAs(final.C(requestexecution.FieldChannelID), "channel_id").
		AppendSelect(scope.C("session_thread"), scope.C("session_trace"))
	h := sql.Table("session_history")
	partition := h.C(request.FieldProjectID) + ", " + h.C("session_thread") + ", " + h.C("session_trace")
	window := d.Select(h.C("execution_id"), h.C("request_id")).From(h).
		AppendSelectAs("LAG("+h.C("channel_id")+") OVER (PARTITION BY "+partition+" ORDER BY "+h.C("request_id")+")", "previous_channel").
		AppendSelectAs("MIN("+h.C("request_id")+") OVER (PARTITION BY "+partition+", "+h.C("channel_id")+")", "first_use_id").As("session_window")
	current := sql.Table(request.Table).As("current_request")
	q.Prefix(sql.With("page_sessions").As(scopes).With("session_history").As(history)).
		Join(current).On(q.C(requestexecution.FieldRequestID), current.C(request.FieldID)).
		LeftJoin(window).On(q.C(requestexecution.FieldID), window.C("execution_id")).
		Where(sql.And(sessionRequestFinished(current),
			sessionAttemptMatches(q.C(requestexecution.FieldID), finalSessionAttempt(d, current, requestexecution.FieldID)))).
		Select(q.C(requestexecution.FieldID), sql.As(q.C(requestexecution.FieldChannelID), "channel"), window.C("previous_channel")).
		AppendSelectAs("CASE WHEN "+window.C("first_use_id")+" < "+current.C(request.FieldID)+" THEN TRUE ELSE FALSE END", "used_before")
}
