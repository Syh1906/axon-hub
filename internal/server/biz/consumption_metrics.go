package biz

import (
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"strings"
)

// Keep archival and live success/latency calculations identical.
func consumptionOutcomeAndSpeed(u *sql.Selector, execution *sql.SelectTable) (string, string) {
	tokens := "CASE WHEN " + u.C("usage_count") + " = 1 THEN " + u.C("text_completion_tokens") + " ELSE NULL END"
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
