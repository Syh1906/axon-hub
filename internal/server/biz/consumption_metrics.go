package biz

import (
	"strings"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

type consumptionPerformanceSQL struct {
	outcome, firstToken, speed string
}

func consumptionPerformanceExpressions(u *sql.Selector, execution *sql.SelectTable) consumptionPerformanceSQL {
	outcome, speed := consumptionOutcomeAndSpeed(u, execution)
	return consumptionPerformanceSQL{
		outcome: outcome, speed: speed,
		firstToken: usageanalytics.FirstTokenSQL(execution.C("metrics_first_token_latency_ms"), execution.C("metrics_latency_ms"), execution.C("stream"), execution.C("status")),
	}
}

// PostgreSQL's aggregate otherwise decodes the price snapshot and computes
// speed again for SUM and COUNT. Each lateral projection returns exactly one
// row per selected request, including unknown samples. OFFSET 0 preserves this
// calculation boundary without obstructing the request/execution join plan.
// Raw table expressions contain only internal identifiers and SQL formulas.
func projectConsumptionPerformance(q *sql.Selector, execution *sql.SelectTable) consumptionPerformanceSQL {
	usage := joinExecutionUsage(q, execution)
	textTokens := usageanalytics.TextTokensSQL(usage.C("completion_tokens"), usage.C("cost_snapshot"), usage.C("completion_audio_tokens"), q.Dialect())
	tokens := sql.Table("LATERAL (SELECT " + textTokens + " AS text_tokens OFFSET 0) AS consumption_tokens").Unquote().As("consumption_tokens")
	q.LeftJoin(tokens).OnP(sql.ExprP("TRUE"))
	first := usageanalytics.FirstTokenSQL(execution.C("metrics_first_token_latency_ms"), execution.C("metrics_latency_ms"), execution.C("stream"), execution.C("status"))
	speed := usageanalytics.OutputSpeedSQL(tokens.C("text_tokens"), execution.C("metrics_latency_ms"), execution.C("metrics_first_byte_latency_ms"), execution.C("stream"), execution.C("format"))
	sample := sql.Table("LATERAL (SELECT " + consumptionOutcome(execution) + " AS outcome, " + first +
		" AS first_token, " + speed + " AS speed OFFSET 0) AS consumption_sample").Unquote().As("consumption_sample")
	q.LeftJoin(sample).OnP(sql.ExprP("TRUE"))
	return consumptionPerformanceSQL{outcome: sample.C("outcome"), firstToken: sample.C("first_token"), speed: sample.C("speed")}
}

// Keep archival and live success/latency calculations identical.
func consumptionOutcomeAndSpeed(u *sql.Selector, execution *sql.SelectTable) (string, string) {
	matched := sql.Table("usage_logs").As("execution_usage")
	matched.SetDialect(u.Dialect())
	tokens := usageanalytics.TextTokensSQL(matched.C("completion_tokens"), matched.C("cost_snapshot"), matched.C("completion_audio_tokens"), u.Dialect())
	speed := usageanalytics.OutputSpeedSQL(tokens, execution.C("metrics_latency_ms"), execution.C("metrics_first_byte_latency_ms"), execution.C("stream"), execution.C("format"))
	return consumptionOutcome(execution), speed
}

func consumptionOutcome(execution *sql.SelectTable) string {
	status, message, code := execution.C("status"), "LOWER(COALESCE("+execution.C("error_message")+", ''))", execution.C("response_status_code")
	byteLatency := execution.C("metrics_first_byte_latency_ms")
	// CCH excludes client aborts after a first byte, 404s, and quota/rate limits.
	exclusions := []string{code + " = 404", message + " LIKE '%no available provider%'", "((" + status + " = 'canceled' OR " + code + " = 499) AND (" + byteLatency + " IS NOT NULL OR " + execution.C("metrics_first_byte_recorded") + " IS NULL))"}
	for _, pattern := range []string{"insufficient quota", "quota exceeded", "rate limit", "rate_limit", "concurrency limit", "concurrent limit", "limit exceeded"} {
		exclusions = append(exclusions, message+" LIKE '%"+strings.ReplaceAll(pattern, "_", "!_")+"%' ESCAPE '!'")
	}
	// AxonHub's local admission errors are synthetic 429s raised before dispatch.
	// Match their persisted messages without excluding unrelated upstream 429s.
	localAdmission := []string{
		"channel % is at capacity (queue!_full); please retry shortly",
		"channel % is at capacity (queue!_timeout); please retry shortly",
		"channel % exhausted local rpm limit %; please retry shortly",
	}
	for _, pattern := range localAdmission {
		exclusions = append(exclusions, "("+code+" = 429 AND "+message+" LIKE '"+pattern+"' ESCAPE '!')")
	}
	return "CASE WHEN " + strings.Join(exclusions, " OR ") + " THEN NULL WHEN " + status + " = 'completed' THEN 1 WHEN " + status + " IN ('failed', 'canceled') THEN 0 ELSE NULL END"
}

func consumptionUnpriced(u *sql.Selector) string {
	return "CASE WHEN " + u.C("usage_count") + " IS NULL OR " + u.C("priced_count") + " < " + u.C("usage_count") + " THEN 1 ELSE 0 END"
}
