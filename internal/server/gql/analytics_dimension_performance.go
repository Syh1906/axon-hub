package gql

import (
	"entgo.io/ent/dialect/sql"
	"fmt"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

// Append performance to the existing usage scan. A missing or ineligible final
// execution removes only the timing sample, never the usage/cost row.
func appendAnalyticsPerformance(s *sql.Selector) {
	execution := sql.Table(requestexecution.Table).As("analytics_execution")
	latest := sql.Table(requestexecution.Table).As("latest_analytics_execution")
	finalID := sql.Dialect(s.Dialect()).Select(sql.Max(latest.C("id"))).From(latest).
		Where(sql.ColumnsEQ(latest.C("request_id"), s.C("request_id")))
	sameUsage := sql.Table(usagelog.Table).As("same_analytics_usage")
	usageCount := sql.Dialect(s.Dialect()).Select(sql.Count("*")).From(sameUsage).
		Where(sql.And(sql.ColumnsEQ(sameUsage.C("request_id"), s.C("request_id")),
			sql.ColumnsEQ(sameUsage.C("channel_id"), s.C("channel_id")),
			sql.ColumnsEQ(sameUsage.C("project_id"), s.C("project_id"))))
	usageCountSQL, _ := usageCount.Query()
	s.LeftJoin(execution).OnP(sql.And(
		sql.EQ(execution.C("id"), finalID),
		sql.ColumnsEQ(execution.C("channel_id"), s.C("channel_id")),
		sql.ColumnsEQ(execution.C("project_id"), s.C("project_id")),
		sql.In(execution.C("status"), "completed", "failed", "canceled"),
		sql.ExprP("("+usageCountSQL+") = 1"),
	))
	tokens := usageanalytics.TextTokensSQL(s.C("completion_tokens"), s.C("cost_snapshot"), s.C("completion_audio_tokens"), s.Dialect())
	speed := usageanalytics.OutputSpeedSQL(tokens, execution.C("metrics_latency_ms"), execution.C("stream"), execution.C("format"))
	firstToken := execution.C("metrics_first_token_latency_ms")
	s.AppendSelect(sql.As("AVG("+speed+")", "tokens_per_second"),
		sql.As(fmt.Sprintf("AVG(CASE WHEN %s = 'completed' AND %s AND %s > 0 THEN %s END)", execution.C("status"), execution.C("stream"), firstToken, firstToken), "ttft_ms"))
}
