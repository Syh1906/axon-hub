package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

const requestBillingVersion = 1

// Parent billing samples are independent of model-level execution performance.
type consumptionBillingTotals struct {
	PricedRequests      int
	PricedCost          float64
	TokenPricedRequests int
	TokenPricedCost     float64
	PricedTokens        int64
	UnpricedRequests    int
	CacheRequests       int
}

func (total *consumptionBillingTotals) add(row consumptionGroup) {
	total.PricedRequests += row.PricedRequests
	total.PricedCost += row.PricedCost
	total.TokenPricedRequests += row.TokenPricedRequests
	total.TokenPricedCost += row.TokenPricedCost
	total.PricedTokens += row.PricedTokens
	total.UnpricedRequests += row.UnpricedRequests
	total.CacheRequests += row.CacheRequests
}

// Final-channel rows already carry their own usage. Add only bills charged to
// other channels, leaving the logical request count and adopted performance
// untouched. Both retained and archived bills follow their request's date.
func queryAdditionalChannelBilling(ctx context.Context, client *ent.Client, projectID int, input ConsumptionFilter, channelIDs []int, start *time.Time, end time.Time, finishedOnly, groupModels bool) ([]consumptionGroup, error) {
	var rows []consumptionGroup
	err := client.UsageLog.Query().Modify(func(q *sql.Selector) {
		u := usageHistory(q.Dialect()).As("billing_usage")
		r := requestHistory(q.Dialect()).As("billing_request")
		model := "COALESCE(NULL, '')"
		keys := []string{u.C("request_id"), u.C("project_id"), u.C("channel_id")}
		if groupModels {
			model = "COALESCE(NULLIF(TRIM(" + u.C("model_id") + "), ''), " + r.C("model_id") + ")"
			keys = append(keys, model)
		}
		units := sql.Dialect(q.Dialect()).Select(u.C("request_id"), u.C("project_id"), u.C("channel_id"), sql.As(model, "model_id")).
			From(u).Join(r).OnP(sql.And(sql.ColumnsEQ(u.C("request_id"), r.C("id")), sql.ColumnsEQ(u.C("project_id"), r.C("project_id")))).
			Where(sql.And(sql.GT(u.C("channel_id"), 0), sql.Or(sql.IsNull(r.C("channel_id")), sql.ColumnsNEQ(u.C("channel_id"), r.C("channel_id"))), sql.LT(r.C("created_at"), end))).
			GroupBy(keys...)
		if start != nil {
			units.Where(sql.GTE(r.C("created_at"), *start))
		}
		if projectID > 0 {
			units.Where(sql.EQ(r.C("project_id"), projectID))
		}
		if len(channelIDs) > 0 {
			units.Where(sql.InInts(u.C("channel_id"), channelIDs...))
		}
		if finishedOnly {
			units.Where(sql.In(r.C("status"), "completed", "failed", "canceled"))
		}
		if input.Metric == ConsumptionMetricCache {
			// Select the whole request/channel billing unit before splitting it
			// into models, so a cached retry keeps its other model charges too.
			cached := usageHistory(q.Dialect()).As("cached_billing")
			units.Where(sql.Exists(sql.Dialect(q.Dialect()).Select(cached.C("id")).From(cached).Where(sql.And(
				sql.ColumnsEQ(cached.C("request_id"), u.C("request_id")), sql.ColumnsEQ(cached.C("project_id"), u.C("project_id")),
				sql.ColumnsEQ(cached.C("channel_id"), u.C("channel_id")), sql.Or(sql.GT(cached.C("prompt_cached_tokens"), 0), sql.GT(cached.C("prompt_write_cached_tokens"), 0))))))
		}
		for _, field := range []string{"prompt_tokens", "completion_tokens", "prompt_cached_tokens", "prompt_write_cached_tokens", "total_cost"} {
			units.AppendSelect(sql.As("COALESCE(SUM("+u.C(field)+"), 0)", field))
		}
		units.AppendSelect(sql.As(sql.Count("*"), "usage_count"), sql.As(sql.Count(u.C("total_cost")), "priced_count"))
		units.As(usagelog.Table)
		q.From(units)
		selectConsumptionEntity(q, input, q.C("model_id"))
		cacheRead, cacheWrite := q.C("prompt_cached_tokens"), q.C("prompt_write_cached_tokens")
		cache := "(" + cacheRead + " > 0 OR " + cacheWrite + " > 0)"
		unpriced := consumptionUnpriced(units)
		tokens := "(" + q.C("prompt_tokens") + " + " + q.C("completion_tokens") + ")"
		appendConsumptionPricing(q, "("+unpriced+") = 0", q.C("total_cost"), tokens)
		q.AppendSelect("0 AS requests", sql.As("SUM("+unpriced+")", "unpriced_requests"),
			sql.As("SUM("+tokens+")", "tokens"), sql.As(sql.Sum(q.C("total_cost")), "cost"),
			sql.As("SUM(CASE WHEN "+cache+" THEN 1 ELSE 0 END)", "cache_requests"),
			sql.As(sql.Sum(cacheRead), "cache_read"), sql.As(sql.Sum(q.C("prompt_tokens")), "cache_input"),
			sql.As("SUM(CASE WHEN "+cacheWrite+" > 0 THEN "+q.C("total_cost")+" ELSE 0 END)", "cache_creation_cost"))
	}).Scan(ctx, &rows)
	return rows, err
}
