package usageanalytics

import "fmt"

// ChannelOutcomeSQL 统计已发送的渠道尝试；换渠道成功不会抹掉前一渠道的失败。
// 客户端取消及未发送的本地拒绝不用于判断供应商成功率。
func ChannelOutcomeSQL(status, selection, code, message, dialect string) string {
	dispatch := "JSON_EXTRACT(" + selection + ", '$.dispatch')"
	if dialect == "postgres" {
		dispatch = "(CAST(" + selection + " AS jsonb)->>'dispatch')"
	}
	local := "(" + code + " = 429 AND (LOWER(" + message + ") LIKE 'channel % is at capacity (queue!_full); please retry shortly' ESCAPE '!' OR LOWER(" + message + ") LIKE 'channel % is at capacity (queue!_timeout); please retry shortly' ESCAPE '!' OR LOWER(" + message + ") LIKE 'channel % exhausted local rpm limit %; please retry shortly'))"
	return "CASE WHEN " + dispatch + " = 'pending' OR " + code + " = 499 OR " + local + " THEN NULL WHEN " + status + " = 'completed' THEN 1 WHEN " + status + " = 'failed' THEN 0 ELSE NULL END"
}

// FirstTokenSQL 只接受实际流式内容的计时，不把未知时间当零值样本。
func FirstTokenSQL(first, duration, stream, status string) string {
	return fmt.Sprintf("CASE WHEN %s AND %s IN ('completed', 'failed', 'canceled') AND %s >= 0 AND %s >= %s THEN %s END", stream, status, first, duration, first, first)
}
