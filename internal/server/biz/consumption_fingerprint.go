package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
)

// 定期核对窄事实字段以恢复丢失的提交后失效通知；不重跑费用/性能聚合，
// 不读取请求正文、凭据、完整价格快照。流式 hash 的内存与历史行数无关。
func (s *ConsumptionService) consumptionFingerprint(ctx context.Context, query consumptionDayQuery) (string, error) {
	db := s.client.Driver().Dialect()
	d := sql.Dialect(db)
	bounded := func(table, at, id string) *sql.Selector {
		t := sql.Table(table)
		t.SetDialect(db)
		q := d.Select(t.C(id)).From(t).Where(sql.And(sql.GTE(t.C(at), query.Start), sql.LT(t.C(at), query.End)))
		if query.Project > 0 {
			q.Where(sql.EQ(t.C("project_id"), query.Project))
		}
		return q
	}
	owners := func() *sql.Selector {
		return bounded("requests", "created_at", "id").UnionAll(bounded("request_executions", "created_at", "request_id")).
			UnionAll(bounded("usage_archives", "occurred_at", "request_id"))
	}
	reference := func(table, field, owner string) *sql.Selector {
		t := sql.Table(table)
		t.SetDialect(db)
		q := d.Select(t.C(field)).From(t).Where(sql.In(t.C(owner), owners()))
		if query.Project > 0 {
			q.Where(sql.EQ(t.C("project_id"), query.Project))
		}
		return q
	}
	keyIDs := func() *sql.Selector {
		return reference("requests", "api_key_id", "id").UnionAll(reference("usage_archives", "api_key_id", "request_id"))
	}
	keyField := func(field string) *sql.Selector {
		t := sql.Table("api_keys")
		t.SetDialect(db)
		return d.Select(t.C(field)).From(t).Where(sql.In(t.C("id"), keyIDs()))
	}
	channelIDs := func() *sql.Selector {
		return reference("requests", "channel_id", "id").
			UnionAll(reference("request_executions", "channel_id", "request_id")).
			UnionAll(reference("usage_logs", "channel_id", "request_id")).
			UnionAll(reference("usage_archives", "channel_id", "request_id"))
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for _, source := range []struct {
		table  string
		fields []string
		owner  string
	}{
		{"requests", []string{"id", "project_id", "api_key_id", "channel_id", "model_id", "status", "result_execution_id", "created_at", "updated_at"}, "id"},
		{"request_executions", []string{"id", "request_id", "project_id", "channel_id", "model_id", "status", "metrics_latency_ms", "metrics_first_byte_latency_ms", "metrics_first_byte_recorded", "metrics_first_token_latency_ms", "stream", "format", "error_message", "response_status_code", "created_at", "updated_at"}, "request_id"},
		{"usage_logs", []string{"id", "request_id", "project_id", "channel_id", "model_id", "prompt_tokens", "completion_tokens", "prompt_cached_tokens", "prompt_write_cached_tokens", "completion_audio_tokens", "total_cost", "updated_at"}, "request_id"},
		{"budget_charges", []string{"id", "project_id", "request_id", "execution_id", "usage_log_id"}, "request_id"},
		{"usage_archives", []string{"id", "kind", "source_id", "request_id", "project_id", "api_key_id", "channel_id", "model_id", "request_model_id", "request_status", "occurred_at", "prompt_tokens", "completion_tokens", "prompt_cached_tokens", "prompt_write_cached_tokens", "total_cost", "pricing_complete", "billing_version", "speed_version", "speed_count", "speed_sum", "first_token_count", "first_token_sum", "success_count", "outcome_count", "cache_requests", "cache_input", "cache_creation_cost"}, "request_id"},
		{"cache_effectiveness_windows", []string{"id", "project_id", "channel_id", "model_id", "window_end", "sample_count", "eligible_count", "theoretical_tokens", "observed_tokens"}, ""},
		{"api_keys", []string{"id", "project_id", "user_id", "group_ref", "name", "deleted_at"}, ""},
		{"api_key_groups", []string{"id", "project_id", "name"}, ""},
		{"users", []string{"id", "first_name", "last_name", "email", "deleted_at"}, ""},
		{"channels", []string{"id", "name", "type", "deleted_at", "tags", "remark"}, ""},
		{"projects", []string{"id", "name"}, ""},
	} {
		t := sql.Table(source.table)
		t.SetDialect(db)
		q := d.Select(t.Columns(source.fields...)...).From(t).OrderBy(t.C("id"))
		if source.owner != "" {
			q.Where(sql.In(t.C(source.owner), owners()))
		}
		switch source.table {
		case "api_keys":
			q.Where(sql.In(t.C("id"), keyIDs()))
		case "api_key_groups":
			q.Where(sql.In(t.C("id"), keyField("group_ref")))
		case "users":
			q.Where(sql.In(t.C("id"), keyField("user_id")))
		case "channels":
			if query.Filter.Dimension == ConsumptionDimensionChannel {
				// 同名渠道决定辨认标签；不同名的无关渠道不影响该日结果。
				names := sql.Table("channels")
				names.SetDialect(db)
				q.Where(sql.Or(sql.In(t.C("id"), channelIDs()), sql.In(t.C("name"), d.Select(names.C("name")).From(names).Where(sql.In(names.C("id"), channelIDs())))))
			} else {
				q.Where(sql.In(t.C("id"), channelIDs()))
			}
		case "projects":
			q.Where(sql.In(t.C("id"), keyField("project_id")))
		}
		if query.Project > 0 && source.table != "users" && source.table != "channels" && source.table != "projects" {
			q.Where(sql.EQ(t.C("project_id"), query.Project))
		}
		if source.table == "cache_effectiveness_windows" {
			q.Where(sql.And(sql.GT(t.C("window_end"), query.Start), sql.LTE(t.C("window_end"), query.End)))
		}
		if source.table == "requests" || source.table == "request_executions" || source.table == "usage_logs" {
			field, path := "failure", "code"
			if source.table == "request_executions" {
				field, path = "selection", "dispatch"
			}
			if source.table == "usage_logs" {
				field, path = "cost_snapshot", "images[0]"
			}
			expr := "JSON_EXTRACT(" + t.C(field) + ", '$." + path + "')"
			if db == dialect.Postgres {
				expr = "CAST(" + t.C(field) + " AS jsonb)->'" + path + "'"
				if source.table == "usage_logs" {
					expr = "CAST(" + t.C(field) + " AS jsonb)->'images'->0"
				}
			}
			if source.table == "usage_logs" {
				expr = "CASE WHEN " + expr + " IS NULL THEN 0 ELSE 1 END"
			}
			q.AppendSelect(sql.As(expr, "evidence"))
		}
		stmt, args := q.Query()
		var rows sql.Rows
		if err := s.client.Driver().Query(ctx, stmt, args, &rows); err != nil {
			return "", fmt.Errorf("fingerprint %s: %w", source.table, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		_ = encoder.Encode(source.table)
		for rows.Next() {
			values := make([]any, len(cols))
			targets := make([]any, len(cols))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				return "", err
			}
			if err := encoder.Encode(values); err != nil {
				rows.Close()
				return "", err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
