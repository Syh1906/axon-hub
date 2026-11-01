package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
)

func (s *ConsumptionService) queryArchivedGroups(ctx context.Context, projectID int, input ConsumptionFilter, start *time.Time, end time.Time) ([]consumptionGroup, error) {
	query := s.client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindRequest), usagearchive.OccurredAtLT(end))
	if projectID > 0 {
		query.Where(usagearchive.ProjectIDEQ(projectID))
	}
	if start != nil {
		query.Where(usagearchive.OccurredAtGTE(*start))
	}
	var rows []consumptionGroup
	err := query.Modify(func(q *sql.Selector) {
		selectConsumptionEntity(q, input, q.C("model_id"))
		if input.Metric == ConsumptionMetricCache {
			q.Where(sql.GT(q.C("cache_requests"), 0))
		}
		q.AppendSelect(sql.As(sql.Count("*"), "requests"),
			sql.As("SUM(CASE WHEN "+q.C("pricing_complete")+" THEN 0 ELSE 1 END)", "unpriced_requests"),
			sql.As("SUM(CASE WHEN "+q.C("speed_version")+" = 1 THEN "+q.C("speed_sum")+" ELSE 0 END)", "speed_sum"),
			sql.As("SUM(CASE WHEN "+q.C("speed_version")+" = 1 THEN "+q.C("speed_count")+" ELSE 0 END)", "speed_count"),
			sql.As("COALESCE(SUM("+q.C("prompt_tokens")+" + "+q.C("completion_tokens")+"),0)", "tokens"))
		for _, field := range [][2]string{
			{"total_cost", "cost"}, {"cache_requests", "cache_requests"}, {"prompt_cached_tokens", "cache_read"},
			{"cache_input", "cache_input"}, {"cache_creation_cost", "cache_creation_cost"},
			{"success_count", "success_count"}, {"outcome_count", "outcome_count"},
			{"first_token_sum", "first_token_sum"}, {"first_token_count", "first_token_count"},
		} {
			q.AppendSelect(sql.As("COALESCE(SUM("+q.C(field[0])+"),0)", field[1]))
		}
	}).Scan(ctx, &rows)
	return rows, err
}

func mergeConsumptionGroups(live, archived []consumptionGroup) []consumptionGroup {
	type key struct {
		id    int
		model string
	}
	index := make(map[key]int, len(live))
	for i, row := range live {
		index[key{row.EntityID, row.Model}] = i
	}
	for _, row := range archived {
		k := key{row.EntityID, row.Model}
		i, found := index[k]
		if !found {
			index[k] = len(live)
			live = append(live, row)
			continue
		}
		t := &live[i]
		t.Requests += row.Requests
		t.Tokens += row.Tokens
		t.Cost += row.Cost
		t.UnpricedRequests += row.UnpricedRequests
		t.CacheRequests += row.CacheRequests
		t.CacheRead += row.CacheRead
		t.CacheInput += row.CacheInput
		t.CacheCreationCost += row.CacheCreationCost
		t.SuccessCount += row.SuccessCount
		t.OutcomeCount += row.OutcomeCount
		t.FirstTokenSum += row.FirstTokenSum
		t.FirstTokenCount += row.FirstTokenCount
		t.SpeedSum += row.SpeedSum
		t.SpeedCount += row.SpeedCount
	}
	return live
}
