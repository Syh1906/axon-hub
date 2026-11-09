package usageanalytics

// RequestResultMatchSQL applies only to the execution representing a logical
// request, never to all-attempt health or billing queries. Aliases are supplied
// by query builders, not request input. Serial legacy rows keep their existing
// ordering; completed legacy requests can identify the adopted channel.
func RequestResultMatchSQL(r, e func(string) string, dialect string) string {
	code := "COALESCE(JSON_EXTRACT(" + r("failure") + ", '$.code'), '')"
	if dialect == "postgres" {
		code = "COALESCE((CAST(" + r("failure") + " AS jsonb)->>'code'), '')"
	}
	legacy := "(" + r("channel_id") + " IS NULL OR " + r("channel_id") + " <= 0 OR NOT (" + r("status") + " = 'completed' OR " + code + " = 'stream_delivery_canceled') OR COALESCE(" + e("channel_id") + ", 0) = " + r("channel_id") + ")"
	return "((" + r("result_execution_id") + " IS NOT NULL AND " + r("result_execution_id") + " = " + e("id") + ") OR (" + r("result_execution_id") + " IS NULL AND " + legacy + "))"
}

// ResultExecutionFilterSQL also preserves old execution-only samples after
// request retention. No winner is invented when the request fact is absent.
func ResultExecutionFilterSQL(executionAlias, dialect string) string {
	return "NOT EXISTS (SELECT 1 FROM requests result_owner WHERE result_owner.id = " + executionAlias + ".request_id AND NOT " +
		RequestResultMatchSQL(func(field string) string { return "result_owner." + field },
			func(field string) string { return executionAlias + "." + field }, dialect) + ")"
}
