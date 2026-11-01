package usageanalytics

import "fmt"

// OutputSpeedSQL uses CCH's per-request first-body-byte denominator. Only text
// streams are comparable; a non-stream body measures download, not generation.
// Arguments are trusted SQL column expressions, never request values.
func OutputSpeedSQL(tokens, duration, firstByte, stream, format string) string {
	return fmt.Sprintf("CASE WHEN %s AND COALESCE(%s, '') NOT LIKE '%%image%%' AND COALESCE(%s, '') NOT LIKE '%%audio%%' AND COALESCE(%s, '') NOT LIKE '%%video%%' AND %s > 0 AND %s >= 0 AND (%s - %s) >= 100 THEN %s * 1000.0 / (%s - %s) ELSE NULL END", stream, format, format, format, tokens, firstByte, duration, firstByte, tokens, duration, firstByte)
}

// TextTokensSQL rejects persisted image outputs even when they travelled over a
// text-capable protocol such as Gemini or Responses. Audio output is separate.
func TextTokensSQL(tokens, snapshot, audio, dialect string) string {
	image := "JSON_EXTRACT(" + snapshot + ", '$.images[0]')"
	if dialect == "postgres" {
		image = "(CAST(" + snapshot + " AS jsonb)->'images'->0)"
	}
	return "CASE WHEN " + image + " IS NULL AND COALESCE(" + audio + ", 0) = 0 THEN " + tokens + " END"
}
