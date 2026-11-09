package usageanalytics

// ExecutionUsageMatchSQL 优先使用账目保存的执行与用量关联。已发布旧记录
// 没有关联时，只接受同请求、项目和渠道唯一的用量，并归到最后一次执行。
// 参数是内部 SQL 字段表达式，不接受用户输入。
func ExecutionUsageMatchSQL(executionID, requestID, channelID, projectID, usageID string) string {
	return usageID + " = " + ExecutionUsageIDSQL(executionID, requestID, channelID, projectID)
}

// Budget charges have a unique execution_id. Resolve the known usage by that
// index before joining usage_logs by its primary key; only legacy attempts need
// the ambiguity checks. COALESCE preserves a recorded association even when its
// usage row is unavailable, instead of guessing a replacement.
func ExecutionUsageIDSQL(executionID, requestID, channelID, projectID string) string {
	known := "SELECT speed_charge.usage_log_id FROM budget_charges speed_charge WHERE speed_charge.execution_id = " + executionID +
		" AND speed_charge.project_id = " + projectID + " AND speed_charge.request_id = " + requestID +
		" AND speed_charge.usage_log_id IS NOT NULL"
	legacy := "SELECT legacy_speed_usage.id FROM usage_logs legacy_speed_usage WHERE legacy_speed_usage.request_id = " + requestID +
		" AND legacy_speed_usage.channel_id = " + channelID + " AND legacy_speed_usage.project_id = " + projectID +
		" AND NOT EXISTS (SELECT 1 FROM budget_charges other_speed_charge WHERE other_speed_charge.project_id = " + projectID +
		" AND other_speed_charge.request_id = " + requestID + " AND other_speed_charge.usage_log_id = legacy_speed_usage.id)" +
		" AND " + executionID + " = (SELECT MAX(last_speed_attempt.id) FROM request_executions last_speed_attempt WHERE last_speed_attempt.request_id = " + requestID +
		" AND last_speed_attempt.channel_id = " + channelID + " AND last_speed_attempt.project_id = " + projectID + ")" +
		" AND NOT EXISTS (SELECT 1 FROM usage_logs duplicate_speed_usage WHERE duplicate_speed_usage.request_id = " + requestID +
		" AND duplicate_speed_usage.channel_id = " + channelID + " AND duplicate_speed_usage.project_id = " + projectID + " AND duplicate_speed_usage.id <> legacy_speed_usage.id)"
	return "COALESCE((" + known + "), (" + legacy + "))"
}
