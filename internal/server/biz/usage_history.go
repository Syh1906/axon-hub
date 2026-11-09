package biz

import (
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

// IncludeArchivedUsage merges numeric facts only; price breakdowns still need
// retained usage logs. Both sources retain the outer query's
// authorization and time predicates; the archive insert and delete are atomic.
func IncludeArchivedUsage(s *sql.Selector) {
	s.From(usageHistory(s.Dialect()).As(usagelog.Table))
}

func usageHistory(db string) *sql.Selector {
	fields := []string{"id", "request_id", "project_id", "api_key_id", "channel_id", "model_id", "created_at",
		"prompt_tokens", "completion_tokens", "total_tokens", "prompt_cached_tokens", "prompt_write_cached_tokens", "completion_reasoning_tokens", "total_cost"}
	live := sql.Table(usagelog.Table)
	archive := sql.Table(usagearchive.Table).As("history_archive")
	live.SetDialect(db)
	archive.SetDialect(db)
	liveCols, archivedCols := make([]string, len(fields)), make([]string, len(fields))
	for i, field := range fields {
		liveCols[i] = live.C(field)
		archivedField := field
		if field == "id" {
			archivedField = "source_id"
		}
		if field == "created_at" {
			archivedField = "occurred_at"
		}
		archivedCols[i] = sql.As(archive.C(archivedField), field)
		if field == usagelog.FieldAPIKeyID {
			// Archive rows use zero for absent keys. Preserve SQL NULL at the
			// analytics boundary so unassigned usage cannot consume a top-key slot.
			archivedCols[i] = sql.As("NULLIF("+archive.C(archivedField)+", 0)", field)
		}
	}
	return sql.Dialect(db).Select(liveCols...).From(live).
		UnionAll(sql.Dialect(db).Select(archivedCols...).From(archive).Where(sql.EQ(archive.C("kind"), string(usagearchive.KindUsage))))
}

func requestHistory(dialect string) *sql.Selector {
	fields := []string{"id", "project_id", "api_key_id", "channel_id", "model_id", "created_at", "status", "source", "failure", "result_execution_id", "attribution_model_id"}
	live := sql.Table(request.Table)
	archive := sql.Table(usagearchive.Table).As("history_request_archive")
	live.SetDialect(dialect)
	archive.SetDialect(dialect)
	liveCols, archivedCols := make([]string, len(fields)), make([]string, len(fields))
	for i, field := range fields {
		liveCols[i] = live.C(field)
		column := field
		switch field {
		case "attribution_model_id":
			liveCols[i] = sql.As(live.C("model_id"), field)
			column = "model_id"
		case "failure", "result_execution_id":
			archivedCols[i] = "NULL AS " + field
			continue
		case "id":
			column = "request_id"
		case "model_id":
			column = "request_model_id"
		case "created_at":
			column = "occurred_at"
		case "status":
			column = "request_status"
		case "source":
			column = "request_source"
		}
		archivedCols[i] = sql.As(archive.C(column), field)
	}
	union := sql.Dialect(dialect).Select(liveCols...).From(live)
	excludeArchivedRequests(union)
	return union.UnionAll(sql.Dialect(dialect).Select(archivedCols...).From(archive).Where(sql.EQ(archive.C("kind"), string(usagearchive.KindRequest)))).As(request.Table)
}

func IncludeArchivedRequests(s *sql.Selector) { s.From(requestHistory(s.Dialect())) }

func (scope selfUsageScope) joinHistory(s *sql.Selector) *sql.Selector {
	r := requestHistory(s.Dialect())
	// Request ownership is authoritative, including historical usage rows whose
	// denormalized project/key fields differ. The request ID is globally unique.
	s.Join(r).On(s.C("request_id"), r.C("id"))
	s.Where(sql.And(sql.InInts(r.C("api_key_id"), scope.keyIDs...), sql.EQ(r.C("project_id"), scope.projectID),
		sql.EQ(r.C("source"), string(request.SourceAPI)), sql.GTE(r.C("created_at"), scope.start), sql.LT(r.C("created_at"), scope.end)))
	return r
}
