package usageanalytics

// ExecutionUsageMatchSQL 优先使用账目保存的执行与用量关联。已发布旧记录
// 没有关联时，只接受同请求、项目和渠道唯一的用量，并归到最后一次执行。
// 参数是内部 SQL 字段表达式，不接受用户输入。
func ExecutionUsageMatchSQL(executionID, requestID, channelID, projectID, usageID string) string {
	known := "SELECT 1 FROM budget_charges speed_charge WHERE speed_charge.execution_id = " + executionID +
		" AND speed_charge.project_id = " + projectID + " AND speed_charge.request_id = " + requestID +
		" AND speed_charge.usage_log_id IS NOT NULL"
	return "(EXISTS (" + known + " AND speed_charge.usage_log_id = " + usageID + ") OR (NOT EXISTS (" + known + ")" +
		" AND NOT EXISTS (SELECT 1 FROM budget_charges other_speed_charge WHERE other_speed_charge.project_id = " + projectID +
		" AND other_speed_charge.request_id = " + requestID + " AND other_speed_charge.usage_log_id = " + usageID + ")" +
		" AND " + executionID + " = (SELECT MAX(last_speed_attempt.id) FROM request_executions last_speed_attempt WHERE last_speed_attempt.request_id = " + requestID +
		" AND last_speed_attempt.channel_id = " + channelID + " AND last_speed_attempt.project_id = " + projectID + ")" +
		" AND NOT EXISTS (SELECT 1 FROM usage_logs duplicate_speed_usage WHERE duplicate_speed_usage.request_id = " + requestID +
		" AND duplicate_speed_usage.channel_id = " + channelID + " AND duplicate_speed_usage.project_id = " + projectID + " AND duplicate_speed_usage.id <> " + usageID + ")))"
}
