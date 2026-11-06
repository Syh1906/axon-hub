package biz

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

type consumptionGroup struct {
	EntityID            int     `json:"entity_id"`
	Name                string  `json:"name"`
	FirstName           string  `json:"first_name"`
	LastName            string  `json:"last_name"`
	Email               string  `json:"email"`
	Model               string  `json:"model"`
	Requests            int     `json:"requests"`
	Tokens              int64   `json:"tokens"`
	Cost                float64 `json:"cost"`
	UnpricedRequests    int     `json:"unpriced_requests"`
	PricedRequests      int     `json:"priced_requests"`
	PricedCost          float64 `json:"priced_cost"`
	TokenPricedRequests int     `json:"token_priced_requests"`
	TokenPricedCost     float64 `json:"token_priced_cost"`
	PricedTokens        int64   `json:"priced_tokens"`
	CacheRequests       int     `json:"cache_requests"`
	CacheRead           int64   `json:"cache_read"`
	CacheInput          int64   `json:"cache_input"`
	CacheCreationCost   float64 `json:"cache_creation_cost"`
	SuccessCount        int64   `json:"success_count"`
	OutcomeCount        int64   `json:"outcome_count"`
	FirstTokenSum       float64 `json:"first_token_sum"`
	FirstTokenCount     int64   `json:"first_token_count"`
	SpeedSum            float64 `json:"speed_sum"`
	SpeedCount          int64   `json:"speed_count"`
}

// usageForConsumption collapses usage rows before joining attempts, so neither
// retries nor a multi-row usage join can multiply request counts or token sums.
func usageForConsumption(projectID int, dialect string, start *time.Time, end time.Time, channelIDs ...int) *sql.Selector {
	table := sql.Table(usagelog.Table).As("ul")
	query := sql.Dialect(dialect).Select(table.C("request_id"), table.C("channel_id"), table.C("project_id")).From(table).
		GroupBy(table.C("request_id"), table.C("channel_id"), table.C("project_id"))
	if projectID > 0 {
		query.Where(sql.EQ(table.C("project_id"), projectID))
	}
	requests := sql.Table(request.Table).As("usage_request")
	boundedRequests := sql.Dialect(dialect).Select(requests.C("id")).From(requests).Where(sql.LT(requests.C("created_at"), end))
	if len(channelIDs) > 0 {
		query.Where(sql.InInts(table.C("channel_id"), channelIDs...))
		boundedRequests.Where(sql.InInts(requests.C("channel_id"), channelIDs...))
	}
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
	query.AppendSelect(sql.As("SUM("+usageanalytics.TextTokensSQL(table.C("completion_tokens"), table.C("cost_snapshot"), table.C("completion_audio_tokens"), dialect)+")", "text_completion_tokens"))
	query.AppendSelect(sql.As(sql.Count("*"), "usage_count"))
	query.AppendSelect(sql.As(sql.Count(table.C("total_cost")), "priced_count"))
	return query.As("usage")
}

func joinConsumption(s *sql.Selector, projectID int, start *time.Time, end time.Time) (*sql.Selector, *sql.SelectTable) {
	return joinConsumptionUsage(s, projectID, start, end), joinLatestConsumptionExecution(s)
}

func joinConsumptionUsage(s *sql.Selector, projectID int, start *time.Time, end time.Time) *sql.Selector {
	u := usageForConsumption(projectID, s.Dialect(), start, end)
	s.LeftJoin(u).OnP(sql.And(sql.ColumnsEQ(s.C("id"), u.C("request_id")), sql.ColumnsEQ(s.C("channel_id"), u.C("channel_id")), sql.ColumnsEQ(s.C("project_id"), u.C("project_id"))))
	return u
}

// PostgreSQL can select each request's final execution and its metrics together.
// Keep bounded detail/archive reads on the indexed per-request lookup instead.
func joinConsumptionGroups(s *sql.Selector, projectID int, input ConsumptionFilter, start *time.Time, end time.Time) (*sql.Selector, *sql.SelectTable) {
	// Search and group filters can select very few requests. Keep their indexed
	// lookup instead of sorting executions from the entire time range.
	if s.Dialect() != dialect.Postgres || input.GroupRef != nil || (input.Search != nil && *input.Search != "") {
		return joinConsumption(s, projectID, start, end)
	}
	u := joinConsumptionUsage(s, projectID, start, end)
	r := sql.Table(request.Table).As("execution_request")
	bounded := sql.Dialect(s.Dialect()).Select(r.C("id")).From(r).Where(sql.LT(r.C("created_at"), end))
	if start != nil {
		bounded.Where(sql.GTE(r.C("created_at"), *start))
	}
	if projectID > 0 {
		bounded.Where(sql.EQ(r.C("project_id"), projectID))
	}
	e := sql.Table(requestexecution.Table).As("candidate_execution")
	keys := []string{e.C("request_id"), e.C("project_id"), e.C("channel_id")}
	columns := e.Columns("id", "request_id", "project_id", "channel_id", "model_id", "status", "error_message",
		"response_status_code", "metrics_latency_ms", "metrics_first_byte_latency_ms", "metrics_first_byte_recorded",
		"metrics_first_token_latency_ms", "stream", "format")
	final := sql.Dialect(s.Dialect()).SelectExpr(sql.ExprFunc(func(b *sql.Builder) {
		b.WriteString("DISTINCT ON ").Wrap(func(b *sql.Builder) { b.IdentComma(keys...) }).Pad().IdentComma(columns...)
	})).From(e).Where(sql.In(e.C("request_id"), bounded)).
		OrderBy(append(keys, sql.Desc(e.C("id")))...).As("execution")
	s.LeftJoin(final).OnP(sql.And(sql.ColumnsEQ(s.C("id"), final.C("request_id")),
		sql.ColumnsEQ(s.C("project_id"), final.C("project_id")), sql.ColumnsEQ(s.C("channel_id"), final.C("channel_id"))))
	return u, sql.Table(requestexecution.Table).As("execution")
}

func joinLatestConsumptionExecution(s *sql.Selector) *sql.SelectTable {
	execution := sql.Table(requestexecution.Table).As("execution")
	latest := sql.Table(requestexecution.Table).As("latest")
	s.LeftJoin(execution).OnP(sql.EQ(execution.C("id"), sql.Dialect(s.Dialect()).Select(sql.Max(latest.C("id"))).From(latest).
		Where(sql.And(sql.ColumnsEQ(latest.C("request_id"), s.C("id")), sql.ColumnsEQ(latest.C("project_id"), s.C("project_id")), sql.ColumnsEQ(latest.C("channel_id"), s.C("channel_id"))))))
	return execution
}

func (s *ConsumptionService) queryGroups(ctx context.Context, projectID int, input ConsumptionFilter, start *time.Time, end time.Time) ([]consumptionGroup, error) {
	// Live and archived rows must be read from one snapshot; cleanup may move
	// a record between the two sources at any moment.
	options := &stdsql.TxOptions{ReadOnly: true, Isolation: stdsql.LevelRepeatableRead}
	if s.client.Driver().Dialect() == dialect.SQLite {
		options = &stdsql.TxOptions{}
	}
	tx, err := s.client.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	reader := *s
	reader.client = tx.Client()
	return reader.queryGroupsSnapshot(ctx, projectID, input, start, end)
}

func (s *ConsumptionService) queryGroupsSnapshot(ctx context.Context, projectID int, input ConsumptionFilter, start *time.Time, end time.Time) ([]consumptionGroup, error) {
	query := s.client.Request.Query().Where(request.CreatedAtLT(end), request.StatusIn(request.StatusCompleted, request.StatusFailed, request.StatusCanceled))
	if projectID > 0 {
		query.Where(request.ProjectIDEQ(projectID))
	}
	if start != nil {
		query.Where(request.CreatedAtGTE(*start))
	}
	var rows []consumptionGroup
	err := query.Modify(func(q *sql.Selector) {
		excludeArchivedRequests(q)
		u, execution := joinConsumptionGroups(q, projectID, input, start, end)
		model := "COALESCE(NULLIF(TRIM(" + execution.C("model_id") + "), ''), " + q.C("model_id") + ")"
		selectConsumptionEntity(q, input, model)
		cacheRead, cacheWrite := "COALESCE("+u.C("prompt_cached_tokens")+", 0)", "COALESCE("+u.C("prompt_write_cached_tokens")+", 0)"
		cacheCondition := "(" + cacheRead + " > 0 OR " + cacheWrite + " > 0)"
		if input.Metric == ConsumptionMetricCache {
			q.Where(sql.ExprP(cacheCondition))
		}
		cacheInput := "COALESCE(SUM(" + u.C("prompt_tokens") + "), 0)"
		if input.Metric == ConsumptionMetricCache {
			cacheInput = "COALESCE(SUM(CASE WHEN " + cacheCondition + " THEN " + u.C("prompt_tokens") + " ELSE 0 END), 0)"
		}
		outcome, speed := consumptionOutcomeAndSpeed(u, execution)
		appendConsumptionPricing(q, "("+consumptionUnpriced(u)+") = 0", u.C("total_cost"), "COALESCE("+u.C("prompt_tokens")+" + "+u.C("completion_tokens")+", 0)")
		firstToken := execution.C("metrics_first_token_latency_ms")
		q.AppendSelect(
			sql.As("SUM("+consumptionUnpriced(u)+")", "unpriced_requests"),
			sql.As(sql.Count("*"), "requests"),
			sql.As("COALESCE(SUM("+u.C("prompt_tokens")+" + "+u.C("completion_tokens")+"), 0)", "tokens"),
			sql.As("COALESCE(SUM("+u.C("total_cost")+"), 0)", "cost"),
			sql.As("SUM(CASE WHEN "+cacheCondition+" THEN 1 ELSE 0 END)", "cache_requests"),
			sql.As("SUM(CASE WHEN "+cacheCondition+" THEN "+cacheRead+" ELSE 0 END)", "cache_read"),
			sql.As(cacheInput, "cache_input"),
			sql.As("COALESCE(SUM(CASE WHEN "+cacheWrite+" > 0 THEN "+u.C("total_cost")+" ELSE 0 END), 0)", "cache_creation_cost"),
			sql.As("COALESCE(SUM("+outcome+"), 0)", "success_count"),
			sql.As("COUNT("+outcome+")", "outcome_count"),
			sql.As("COALESCE(SUM("+firstToken+"), 0)", "first_token_sum"),
			sql.As("COUNT("+firstToken+")", "first_token_count"),
			sql.As("COALESCE(SUM("+speed+"), 0)", "speed_sum"),
			sql.As("COUNT("+speed+")", "speed_count"),
		)
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("query consumption groups: %w", err)
	}
	archived, err := s.queryArchivedGroups(ctx, projectID, input, start, end)
	if err != nil {
		return nil, err
	}
	return mergeConsumptionGroups(rows, archived), nil
}

// Each average uses matching cost and quantity samples. Unknown costs never
// become zero, and requests without token evidence do not dilute token prices.
func appendConsumptionPricing(q *sql.Selector, complete, cost, tokens string) {
	tokenComplete := "(" + complete + ") AND (" + tokens + ") > 0"
	for _, item := range []struct{ condition, value, name string }{
		{complete, "1", "priced_requests"},
		{complete, cost, "priced_cost"},
		{tokenComplete, "1", "token_priced_requests"},
		{tokenComplete, cost, "token_priced_cost"},
		{tokenComplete, tokens, "priced_tokens"},
	} {
		q.AppendSelect(sql.As("COALESCE(SUM(CASE WHEN "+item.condition+" THEN "+item.value+" ELSE 0 END), 0)", item.name))
	}
}

func selectConsumptionEntity(q *sql.Selector, input ConsumptionFilter, model string) {
	if input.Dimension == ConsumptionDimensionModel {
		q.Select(sql.As(model, "model"), sql.As(model, "name"))
		q.GroupBy(model)
		if input.Search != nil && *input.Search != "" {
			q.Where(sql.ContainsFold(model, *input.Search))
		}
		return
	}

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
			q.Where(sql.NotNull(group.C("id")))
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
	q.Select(sql.As(entityID, "entity_id"), sql.As(model, "model"))
	if input.Dimension == ConsumptionDimensionUser {
		q.AppendSelect(sql.As(names[0], "first_name"), sql.As(names[1], "last_name"), sql.As(names[2], "email"), "'' AS name")
	} else {
		q.AppendSelect(sql.As(names[0], "name"), "'' AS first_name", "'' AS last_name", "'' AS email")
	}
	q.GroupBy(append([]string{entityID, "model"}, names...)...)
}
