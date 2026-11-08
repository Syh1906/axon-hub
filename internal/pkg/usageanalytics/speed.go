package usageanalytics

import "fmt"

// OutputSpeedVersion identifies the formula persisted in usage archives.
const OutputSpeedVersion = 2

// OutputSpeedSQL measures average output over the entire final execution,
// including waiting and reasoning. A forwarded stream can omit early events,
// so the first body byte is not a comparable start time across gateways.
// Arguments are trusted SQL column expressions, never request values.
func OutputSpeedSQL(tokens, duration, stream, format string) string {
	return fmt.Sprintf("CASE WHEN %s AND COALESCE(%s, '') NOT LIKE '%%image%%' AND COALESCE(%s, '') NOT LIKE '%%audio%%' AND COALESCE(%s, '') NOT LIKE '%%video%%' AND %s > 0 AND %s > 0 THEN %s * 1000.0 / %s ELSE NULL END", stream, format, format, format, tokens, duration, tokens, duration)
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
