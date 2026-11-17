package biz

import (
	"entgo.io/ent/dialect/sql"
)

// Correlate both retained and archived bills before their UNION. PostgreSQL
// otherwise sorts/scans the entire ledger for an hourly request range, even
// when the result has no extra-channel charges. Arguments are builder-owned
// column expressions and aliases, never user input.
func postgresConsumptionUsage(requestID, projectID, alias string) *sql.SelectTable {
	fields := "request_id, project_id, channel_id, model_id, prompt_tokens, completion_tokens, total_tokens, prompt_cached_tokens, prompt_write_cached_tokens, total_cost"
	return sql.Table("LATERAL (SELECT id, " + fields + " FROM usage_logs WHERE request_id = " + requestID + " AND project_id = " + projectID +
		" UNION ALL SELECT source_id AS id, " + fields + " FROM usage_archives WHERE kind = 'usage' AND request_id = " + requestID + " AND project_id = " + projectID + " OFFSET 0) AS " + alias).Unquote().As(alias)
}

func joinPostgresConsumptionUsage(s *sql.Selector, byChannel bool) *sql.Selector {
	rows := postgresConsumptionUsage(s.C("id"), s.C("project_id"), "usage_fact")
	q := sql.Dialect(s.Dialect()).Select().From(rows)
	if byChannel {
		q.Where(sql.ColumnsEQ(rows.C("channel_id"), s.C("channel_id")))
	}
	for _, field := range []string{"prompt_tokens", "completion_tokens", "total_tokens", "prompt_cached_tokens", "prompt_write_cached_tokens", "total_cost"} {
		q.AppendSelect(sql.As("COALESCE(SUM("+rows.C(field)+"),0)", field))
	}
	q.AppendSelect(sql.As(sql.Count("*"), "usage_count"), sql.As(sql.Count(rows.C("total_cost")), "priced_count"))
	q.Having(sql.ExprP("COUNT(*) > 0"))
	statement, _ := q.Query()
	u := sql.Table("LATERAL (" + statement + ") AS usage").Unquote().As("usage")
	s.LeftJoin(u).OnP(sql.ExprP("TRUE"))
	return sql.Dialect(s.Dialect()).Select().As("usage")
}
