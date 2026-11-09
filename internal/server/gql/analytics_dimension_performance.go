package gql

import (
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

// Append performance to the existing usage scan. A missing or ineligible final
// execution removes only the timing sample, never the usage/cost row.
func appendAnalyticsPerformance(s *sql.Selector) {
	execution := sql.Table(requestexecution.Table).As("analytics_execution")
	latest := sql.Table(requestexecution.Table).As("latest_analytics_execution")
	finalID := sql.Dialect(s.Dialect()).Select(sql.Max(latest.C("id"))).From(latest).
		Where(sql.And(sql.ColumnsEQ(latest.C("request_id"), s.C("request_id")), sql.ColumnsEQ(latest.C("project_id"), s.C("project_id"))))
	s.LeftJoin(execution).OnP(sql.And(
		sql.EQ(execution.C("id"), finalID),
		sql.ColumnsEQ(execution.C("channel_id"), s.C("channel_id")),
		sql.ColumnsEQ(execution.C("project_id"), s.C("project_id")),
		sql.In(execution.C("status"), "completed", "failed", "canceled"),
		sql.ExprP(usageanalytics.ExecutionUsageMatchSQL(execution.C("id"), execution.C("request_id"), execution.C("channel_id"), execution.C("project_id"), s.C("id"))),
	))
	tokens := usageanalytics.TextTokensSQL(s.C("completion_tokens"), s.C("cost_snapshot"), s.C("completion_audio_tokens"), s.Dialect())
	speed := usageanalytics.OutputSpeedSQL(tokens, execution.C("metrics_latency_ms"), execution.C("metrics_first_byte_latency_ms"), execution.C("stream"), execution.C("format"))
	firstToken := usageanalytics.FirstTokenSQL(execution.C("metrics_first_token_latency_ms"), execution.C("metrics_latency_ms"), execution.C("stream"), execution.C("status"))
	s.AppendSelect(sql.As("AVG("+speed+")", "tokens_per_second"),
		sql.As("AVG("+firstToken+")", "ttft_ms"))
}
