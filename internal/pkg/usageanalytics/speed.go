package usageanalytics

import "fmt"

// OutputSpeedVersion identifies the formula persisted in usage archives.
const OutputSpeedVersion = 3

// OutputSpeedSQL 与 CCH 一致：扣除本次执行等待首字节的时间。
// 缺失计时、过短窗口及明显缓冲后集中交付的样本不用于单次值或平均值。
// Arguments are trusted SQL column expressions, never request values.
func OutputSpeedSQL(tokens, duration, firstByte, stream, format string) string {
	window := "(" + duration + " - " + firstByte + ")"
	rate := "(" + tokens + " * 1000.0 / NULLIF(" + window + ", 0))"
	return fmt.Sprintf("CASE WHEN %s AND COALESCE(%s, '') NOT LIKE '%%image%%' AND COALESCE(%s, '') NOT LIKE '%%audio%%' AND COALESCE(%s, '') NOT LIKE '%%video%%' AND %s > 0 AND %s > 0 AND %s >= 0 AND %s >= 100 AND NOT (%s < %s * 0.1 AND %s > 5000) THEN %s ELSE NULL END", stream, format, format, format, tokens, duration, firstByte, window, window, duration, rate, rate)
}

// TextTokensSQL rejects persisted image outputs even when they were sent over a
// text-capable protocol such as Gemini or Responses. Audio output is separate.
func TextTokensSQL(tokens, snapshot, audio, dialect string) string {
	image := "JSON_EXTRACT(" + snapshot + ", '$.images[0]')"
	if dialect == "postgres" {
		image = "(CAST(" + snapshot + " AS jsonb)->'images'->0)"
	}
	return "CASE WHEN " + image + " IS NULL AND COALESCE(" + audio + ", 0) = 0 THEN " + tokens + " END"
}
