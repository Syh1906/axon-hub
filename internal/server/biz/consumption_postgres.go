package biz

import (
	"entgo.io/ent/dialect/sql"
)

// Correlate both retained and archived bills before their UNION. PostgreSQL
// otherwise sorts/scans the entire ledger for an hourly request range, even
// when the result has no extra-channel charges. Arguments are builder-owned
// column expressions and aliases, never user input.
func postgresConsumptionUsage(requestID, projectID, alias string) *sql.SelectTable {
	fields := "request_id, project_id, channel_id, model_id, prompt_tokens, completion_tokens, total_tokens, prompt_cached_tokens, prompt_write_cached_tokens, total_cost, billed_cost, billing_group_id"
	return sql.Table("LATERAL (SELECT id, statistics_standard_cost, " + fields + " FROM usage_logs WHERE request_id = " + requestID + " AND project_id = " + projectID +
		" UNION ALL SELECT source_id AS id, NULL AS statistics_standard_cost, " + fields + " FROM usage_archives WHERE kind = 'usage' AND request_id = " + requestID + " AND project_id = " + projectID + " OFFSET 0) AS " + alias).Unquote().As(alias)
}

func joinPostgresConsumptionUsage(s *sql.Selector, byChannel bool) *sql.Selector {
	rows := postgresConsumptionUsage(s.C("id"), s.C("project_id"), "usage_fact")
	q := sql.Dialect(s.Dialect()).Select().From(rows)
	if byChannel {
		q.Where(sql.ColumnsEQ(rows.C("channel_id"), s.C("channel_id")))
	}
	amount := rows.C("total_cost")
	if !byChannel {
		amount = billedCostExpression(rows.C("billing_group_id"), rows.C("total_cost"), rows.C("billed_cost"))
	}
	for _, field := range []string{"prompt_tokens", "completion_tokens", "total_tokens", "prompt_cached_tokens", "prompt_write_cached_tokens"} {
		q.AppendSelect(sql.As("COALESCE(SUM("+rows.C(field)+"),0)", field))
	}
	q.AppendSelect(sql.As("COALESCE(SUM("+amount+"),0)", "total_cost"))
	q.AppendSelect(sql.As(sql.Count("*"), "usage_count"), sql.As("COUNT("+amount+")", "priced_count"))
	q.Having(sql.ExprP("COUNT(*) > 0"))
	statement, _ := q.Query()
	u := sql.Table("LATERAL (" + statement + ") AS usage").Unquote().As("usage")
	s.LeftJoin(u).OnP(sql.ExprP("TRUE"))
	return sql.Dialect(s.Dialect()).Select().As("usage")
}
