package biz

import (
	"context"
	"strconv"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
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
		speedVersion := strconv.Itoa(usageanalytics.OutputSpeedVersion)
		u := usageForConsumption(projectID, q.Dialect(), start, end, input.Dimension == ConsumptionDimensionChannel)
		match := sql.And(sql.ColumnsEQ(q.C("request_id"), u.C("request_id")), sql.ColumnsEQ(q.C("project_id"), u.C("project_id")))
		if input.Dimension == ConsumptionDimensionChannel {
			match = sql.And(match, sql.ColumnsEQ(q.C("channel_id"), u.C("channel_id")))
		}
		q.LeftJoin(u).OnP(match)
		// Older snapshots contain only final-channel billing. Prefer preserved
		// bills; if they are gone, retain the known amount without claiming that
		// it proves complete all-attempt pricing.
		value := func(field string) string {
			fallback := "COALESCE(" + q.C(field) + ", 0)"
			if input.Dimension == ConsumptionDimensionChannel {
				// New request snapshots hold all-channel totals. Without the
				// underlying bills, that total cannot be attributed to one channel.
				fallback = "CASE WHEN " + q.C("billing_version") + " = 0 THEN " + fallback + " ELSE 0 END"
			}
			return "CASE WHEN " + u.C("usage_count") + " IS NOT NULL THEN COALESCE(" + u.C(field) + ", 0) ELSE " + fallback + " END"
		}
		complete := "CASE WHEN " + u.C("usage_count") + " IS NOT NULL THEN " + u.C("priced_count") + " = " + u.C("usage_count") + " ELSE " + q.C("billing_version") + " = " + strconv.Itoa(requestBillingVersion) + " AND " + q.C("pricing_complete") + " END"
		if input.Dimension == ConsumptionDimensionChannel {
			complete = "(" + u.C("usage_count") + " IS NOT NULL AND " + u.C("priced_count") + " = " + u.C("usage_count") + ")"
		}
		cost, tokens := value("total_cost"), "("+value("prompt_tokens")+" + "+value("completion_tokens")+")"
		cacheRead, cacheWrite := value("prompt_cached_tokens"), value("prompt_write_cached_tokens")
		cache := "(" + cacheRead + " > 0 OR " + cacheWrite + " > 0)"
		selectConsumptionEntity(q, input, q.C("model_id"))
		appendConsumptionPricing(q, complete, cost, tokens)
		if input.Metric == ConsumptionMetricCache {
			q.Where(sql.ExprP(cache))
		}
		q.AppendSelect(sql.As(sql.Count("*"), "requests"),
			sql.As("SUM(CASE WHEN "+complete+" THEN 0 ELSE 1 END)", "unpriced_requests"),
			sql.As("SUM(CASE WHEN "+q.C("speed_version")+" = "+speedVersion+" THEN "+q.C("speed_sum")+" ELSE 0 END)", "speed_sum"),
			sql.As("SUM(CASE WHEN "+q.C("speed_version")+" = "+speedVersion+" THEN "+q.C("speed_count")+" ELSE 0 END)", "speed_count"),
			sql.As("COALESCE(SUM("+tokens+"),0)", "tokens"),
			sql.As("COALESCE(SUM("+cost+"),0)", "cost"),
			sql.As("SUM(CASE WHEN "+cache+" THEN 1 ELSE 0 END)", "cache_requests"),
			sql.As("COALESCE(SUM("+cacheRead+"),0)", "cache_read"),
			sql.As("COALESCE(SUM("+value("prompt_tokens")+"),0)", "cache_input"),
			sql.As("SUM(CASE WHEN "+cacheWrite+" > 0 THEN "+cost+" ELSE 0 END)", "cache_creation_cost"))
		for _, field := range [][2]string{
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
		t.PricedRequests += row.PricedRequests
		t.PricedCost += row.PricedCost
		t.TokenPricedRequests += row.TokenPricedRequests
		t.TokenPricedCost += row.TokenPricedCost
		t.PricedTokens += row.PricedTokens
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
