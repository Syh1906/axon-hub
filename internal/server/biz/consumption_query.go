package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/ent/user"
)

type consumptionGroup struct {
	EntityID          int     `json:"entity_id"`
	Name              string  `json:"name"`
	FirstName         string  `json:"first_name"`
	LastName          string  `json:"last_name"`
	Email             string  `json:"email"`
	Model             string  `json:"model"`
	Requests          int     `json:"requests"`
	Tokens            int64   `json:"tokens"`
	Cost              float64 `json:"cost"`
	CacheRequests     int     `json:"cache_requests"`
	CacheRead         int64   `json:"cache_read"`
	CacheInput        int64   `json:"cache_input"`
	CacheCreationCost float64 `json:"cache_creation_cost"`
	SuccessCount      int64   `json:"success_count"`
	OutcomeCount      int64   `json:"outcome_count"`
	FirstTokenSum     float64 `json:"first_token_sum"`
	FirstTokenCount   int64   `json:"first_token_count"`
	SpeedSum          float64 `json:"speed_sum"`
	SpeedCount        int64   `json:"speed_count"`
}

// usageForConsumption collapses usage rows before joining attempts, so neither
// retries nor a multi-row usage join can multiply request counts or token sums.
func usageForConsumption(projectID int, dialect string, start *time.Time, end time.Time) *sql.Selector {
	table := sql.Table(usagelog.Table).As("ul")
	query := sql.Dialect(dialect).Select(table.C("request_id"), table.C("channel_id"), table.C("project_id")).From(table).
		GroupBy(table.C("request_id"), table.C("channel_id"), table.C("project_id"))
	if projectID > 0 {
		query.Where(sql.EQ(table.C("project_id"), projectID))
	}
	requests := sql.Table(request.Table).As("usage_request")
	boundedRequests := sql.Dialect(dialect).Select(requests.C("id")).From(requests).Where(sql.LT(requests.C("created_at"), end))
	if start != nil {
		boundedRequests.Where(sql.GTE(requests.C("created_at"), *start))
	}
	if projectID > 0 {
		boundedRequests.Where(sql.EQ(requests.C("project_id"), projectID))
	}
	query.Where(sql.In(table.C("request_id"), boundedRequests))
	for _, field := range []string{"prompt_tokens", "completion_tokens", "total_tokens", "prompt_cached_tokens", "prompt_write_cached_tokens", "total_cost"} {
		query.AppendSelect(sql.As("COALESCE(SUM("+table.C(field)+"), 0)", field))
	}
	query.AppendSelect(sql.As(sql.Count("*"), "usage_count"))
	return query.As("usage")
}

func joinConsumption(s *sql.Selector, projectID int, start *time.Time, end time.Time) (*sql.Selector, *sql.SelectTable) {
	u := usageForConsumption(projectID, s.Dialect(), start, end)
	s.LeftJoin(u).OnP(sql.And(sql.ColumnsEQ(s.C("id"), u.C("request_id")), sql.ColumnsEQ(s.C("channel_id"), u.C("channel_id")), sql.ColumnsEQ(s.C("project_id"), u.C("project_id"))))
	execution := sql.Table(requestexecution.Table).As("execution")
	latest := sql.Table(requestexecution.Table).As("latest")
	s.LeftJoin(execution).OnP(sql.EQ(execution.C("id"), sql.Dialect(s.Dialect()).Select(sql.Max(latest.C("id"))).From(latest).
		Where(sql.And(sql.ColumnsEQ(latest.C("request_id"), s.C("id")), sql.ColumnsEQ(latest.C("project_id"), s.C("project_id")), sql.ColumnsEQ(latest.C("channel_id"), s.C("channel_id"))))))
	return u, execution
}

func (s *ConsumptionService) queryGroups(ctx context.Context, projectID int, input ConsumptionFilter, start *time.Time, end time.Time) ([]consumptionGroup, error) {
	query := s.client.Request.Query().Where(request.CreatedAtLT(end), request.StatusIn(request.StatusCompleted, request.StatusFailed, request.StatusCanceled))
	if projectID > 0 {
		query.Where(request.ProjectIDEQ(projectID))
	}
	if start != nil {
		query.Where(request.CreatedAtGTE(*start))
	}
	var rows []consumptionGroup
	err := query.Modify(func(q *sql.Selector) {
		u, execution := joinConsumption(q, projectID, start, end)
		model := "COALESCE(NULLIF(TRIM(" + execution.C("model_id") + "), ''), " + q.C("model_id") + ")"
		var entityID string
		var names []string
		var searchColumns []string
		switch input.Dimension {
		case ConsumptionDimensionUser, ConsumptionDimensionAPIKey, ConsumptionDimensionAPIKeyGroup:
			key := sql.Table(apikey.Table).As("ak")
			q.Join(key).OnP(sql.And(sql.ColumnsEQ(q.C("api_key_id"), key.C("id")), sql.ColumnsEQ(key.C("project_id"), q.C("project_id"))))
			group := sql.Table("api_key_groups").As("key_group")
			q.LeftJoin(group).OnP(sql.And(sql.ColumnsEQ(key.C("group_ref"), group.C("id")), sql.ColumnsEQ(key.C("project_id"), group.C("project_id"))))
			if input.GroupRef != nil {
				q.Where(sql.EQ(group.C("id"), *input.GroupRef))
			}
			if input.Dimension == ConsumptionDimensionAPIKeyGroup {
				q.Where(sql.And(sql.NotNull(group.C("id")), sql.EQ(key.C("deleted_at"), 0)))
				entityID = group.C("id")
				names = []string{group.C("name")}
				searchColumns = names
			} else if input.Dimension == ConsumptionDimensionUser {
				owner := sql.Table(user.Table).As("owner")
				q.Join(owner).On(key.C("user_id"), owner.C("id"))
				q.Where(sql.EQ(owner.C("deleted_at"), 0))
				entityID = owner.C("id")
				names = []string{owner.C("first_name"), owner.C("last_name"), owner.C("email")}
				fullName := "TRIM(" + names[0] + " || ' ' || " + names[1] + ")"
				if q.Dialect() == dialect.MySQL {
					fullName = "TRIM(CONCAT(" + names[0] + ", ' ', " + names[1] + "))"
				}
				searchColumns = append(append([]string{}, names...), fullName)
			} else {
				q.Where(sql.EQ(key.C("deleted_at"), 0))
				entityID = key.C("id")
				names = []string{key.C("name")}
				searchColumns = names
			}
		case ConsumptionDimensionChannel:
			ch := sql.Table(channel.Table).As("channel")
			q.Join(ch).On(q.C("channel_id"), ch.C("id"))
			q.Where(sql.EQ(ch.C("deleted_at"), 0))
			entityID = ch.C("id")
			names = []string{ch.C("name")}
			searchColumns = names
		}
		if input.Search != nil && *input.Search != "" {
			predicates := make([]*sql.Predicate, 0, len(searchColumns))
			for _, column := range searchColumns {
				predicates = append(predicates, sql.ContainsFold(column, *input.Search))
			}
			q.Where(sql.Or(predicates...))
		}
		cacheRead, cacheWrite := "COALESCE("+u.C("prompt_cached_tokens")+", 0)", "COALESCE("+u.C("prompt_write_cached_tokens")+", 0)"
		cacheCondition := "(" + cacheRead + " > 0 OR " + cacheWrite + " > 0)"
		if input.Metric == ConsumptionMetricCache {
			q.Where(sql.ExprP(cacheCondition))
		}
		status, message, code := execution.C("status"), "LOWER(COALESCE("+execution.C("error_message")+", ''))", execution.C("response_status_code")
		byteLatency, duration := execution.C("metrics_first_byte_latency_ms"), execution.C("metrics_latency_ms")
		firstToken := execution.C("metrics_first_token_latency_ms")
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
		outcome := "CASE WHEN " + strings.Join(exclusions, " OR ") + " THEN NULL WHEN " + status + " = 'completed' THEN 1 WHEN " + status + " IN ('failed', 'canceled') THEN 0 ELSE NULL END"
		speed := fmt.Sprintf("CASE WHEN %s > 0 AND %s IS NOT NULL AND %s IS NOT NULL AND %s < %s AND (%s - %s) >= 100 THEN %s * 1000.0 / (%s - %s) ELSE NULL END", u.C("completion_tokens"), duration, byteLatency, byteLatency, duration, duration, byteLatency, u.C("completion_tokens"), duration, byteLatency)
		q.Select(sql.As(entityID, "entity_id"), sql.As(model, "model"))
		if input.Dimension == ConsumptionDimensionUser {
			q.AppendSelect(sql.As(names[0], "first_name"), sql.As(names[1], "last_name"), sql.As(names[2], "email"), "'' AS name")
		} else {
			q.AppendSelect(sql.As(names[0], "name"), "'' AS first_name", "'' AS last_name", "'' AS email")
		}
		q.AppendSelect(
			sql.As(sql.Count("*"), "requests"),
			sql.As("COALESCE(SUM("+u.C("prompt_tokens")+" + "+u.C("completion_tokens")+"), 0)", "tokens"),
			sql.As("COALESCE(SUM("+u.C("total_cost")+"), 0)", "cost"),
			sql.As("SUM(CASE WHEN "+cacheCondition+" THEN 1 ELSE 0 END)", "cache_requests"),
			sql.As("SUM(CASE WHEN "+cacheCondition+" THEN "+cacheRead+" ELSE 0 END)", "cache_read"),
			sql.As("COALESCE(SUM(CASE WHEN "+cacheCondition+" THEN "+u.C("prompt_tokens")+" ELSE 0 END), 0)", "cache_input"),
			sql.As("COALESCE(SUM(CASE WHEN "+cacheWrite+" > 0 THEN "+u.C("total_cost")+" ELSE 0 END), 0)", "cache_creation_cost"),
			sql.As("COALESCE(SUM("+outcome+"), 0)", "success_count"),
			sql.As("COUNT("+outcome+")", "outcome_count"),
			sql.As("COALESCE(SUM("+firstToken+"), 0)", "first_token_sum"),
			sql.As("COUNT("+firstToken+")", "first_token_count"),
			sql.As("COALESCE(SUM("+speed+"), 0)", "speed_sum"),
			sql.As("COUNT("+speed+")", "speed_count"),
		).GroupBy(append([]string{entityID, "model"}, names...)...)
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("query consumption groups: %w", err)
	}
	return rows, nil
}
