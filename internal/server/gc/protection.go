package gc

import (
	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/predicate"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/thread"
	"github.com/looplj/axonhub/internal/ent/trace"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

func unsettledCharge(s *sql.Selector, requestColumn string) *sql.Predicate {
	b := sql.Table(budgetcharge.Table).As("gc_charge")
	return sql.Exists(sql.Dialect(s.Dialect()).Select(b.C("id")).From(b).Where(sql.And(
		sql.ColumnsEQ(b.C("request_id"), s.C(requestColumn)),
		sql.ColumnsEQ(b.C("project_id"), s.C("project_id")),
		sql.In(b.C("status"), "prepared", "sent", "unknown"),
	)))
}

func safeRequests() []predicate.Request {
	return []predicate.Request{
		request.StatusIn(request.StatusCompleted, request.StatusFailed, request.StatusCanceled),
		request.Not(request.HasTraceWith(trace.Or(trace.StatusEQ(trace.StatusRetained), trace.HasThreadWith(thread.StatusEQ(thread.StatusRetained))))),
		request.Not(request.HasExecutionsWith(requestexecution.StatusIn(requestexecution.StatusPending, requestexecution.StatusProcessing))),
		func(s *sql.Selector) { s.Where(sql.Not(unsettledCharge(s, request.FieldID))) },
	}
}

func safeExecutions() []predicate.RequestExecution {
	return []predicate.RequestExecution{
		requestexecution.StatusIn(requestexecution.StatusCompleted, requestexecution.StatusFailed, requestexecution.StatusCanceled),
		requestexecution.Or(requestexecution.HasRequestWith(safeRequests()...), requestexecution.Not(requestexecution.HasRequestWith())),
		func(s *sql.Selector) { s.Where(sql.Not(unsettledCharge(s, requestexecution.FieldRequestID))) },
	}
}

func safeUsageLogs() []predicate.UsageLog {
	return []predicate.UsageLog{
		usagelog.Or(usagelog.HasRequestWith(safeRequests()...), usagelog.Not(usagelog.HasRequestWith())),
		func(s *sql.Selector) { s.Where(sql.Not(unsettledCharge(s, usagelog.FieldRequestID))) },
	}
}

func afterCheckpoint(s *sql.Selector, stage *CleanupStage, timeColumn string) {
	if !stage.CursorTime.IsZero() {
		s.Where(sql.Or(sql.GT(s.C(timeColumn), stage.CursorTime), sql.And(sql.EQ(s.C(timeColumn), stage.CursorTime), sql.GT(s.C("id"), stage.CursorID))))
	}
}

// Keep the exclusion marker while its original request or usage is retained.
// Otherwise deleting a statistics row could make partial live totals reappear.
func protectReferencedArchive(s *sql.Selector) {
	r := sql.Table(request.Table).As("archive_source_request")
	u := sql.Table(usagelog.Table).As("archive_source_usage")
	liveRequest := sql.Dialect(s.Dialect()).Select(r.C("id")).From(r).Where(sql.And(
		sql.ColumnsEQ(r.C("id"), s.C("request_id")), sql.ColumnsEQ(r.C("project_id"), s.C("project_id"))))
	liveUsage := sql.Dialect(s.Dialect()).Select(u.C("id")).From(u).Where(sql.And(
		sql.ColumnsEQ(u.C("request_id"), s.C("request_id")), sql.ColumnsEQ(u.C("project_id"), s.C("project_id"))))
	s.Where(sql.Or(sql.NEQ(s.C("kind"), string(usagearchive.KindRequest)), sql.And(sql.Not(sql.Exists(liveRequest)), sql.Not(sql.Exists(liveUsage)))))
}
