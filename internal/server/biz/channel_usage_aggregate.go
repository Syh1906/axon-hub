package biz

import (
	"cmp"
	"context"
	"slices"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

// Select request/channel billing units once for the whole visible batch. The
// two indexed branches avoid rechecking every request for every 512-row page.
func channelUsageUnits(db string, ids []int, start, end time.Time) *sql.Selector {
	d := sql.Dialect(db)
	r := requestHistory(db)
	fields := []string{"id", "project_id", "channel_id", "created_at", "result_execution_id", "attribution_model_id"}
	logical := d.Select(append(r.Columns(fields...), sql.As(r.C("channel_id"), "target_channel_id"))...).From(r).
		Where(sql.And(sql.InInts(r.C("channel_id"), ids...), sql.GTE(r.C("created_at"), start.UTC()), sql.LT(r.C("created_at"), end.UTC())))
	bill := usageHistory(db).As("other_channel_bill")
	var billTable sql.TableView = bill
	if db == dialect.Postgres {
		billTable = postgresConsumptionUsage(r.C("id"), r.C("project_id"), "other_channel_bill")
	}
	other := d.Select(append(r.Columns(fields...), sql.As(bill.C("channel_id"), "target_channel_id"))...).Distinct().From(r).
		Join(billTable).OnP(sql.And(sql.ColumnsEQ(bill.C("request_id"), r.C("id")), sql.ColumnsEQ(bill.C("project_id"), r.C("project_id")))).
		Where(sql.And(sql.InInts(bill.C("channel_id"), ids...), sql.Or(sql.IsNull(r.C("channel_id")), sql.ColumnsNEQ(r.C("channel_id"), bill.C("channel_id"))),
			sql.GTE(r.C("created_at"), start.UTC()), sql.LT(r.C("created_at"), end.UTC())))
	if len(ids) == 1 {
		other.Where(sql.Or(sql.IsNull(r.C("channel_id")), sql.NEQ(r.C("channel_id"), ids[0])))
	}
	return logical.UnionAll(other).As("channel_request")
}

func channelUsageModelAggregate(db string, ids []int, start, end time.Time) *sql.Selector {
	d := sql.Dialect(db)
	r := channelUsageUnits(db, ids, start, end)
	q := d.Select().From(r).As("channel_request")
	u := usageHistory(db).As("channel_bill")
	var bills sql.TableView = u
	if db == dialect.Postgres {
		bills = postgresConsumptionUsage(q.C("id"), q.C("project_id"), "channel_bill")
	}
	q.LeftJoin(bills).OnP(sql.And(sql.ColumnsEQ(q.C("id"), u.C("request_id")), sql.ColumnsEQ(q.C("project_id"), u.C("project_id")), sql.ColumnsEQ(q.C("target_channel_id"), u.C("channel_id"))))
	logical := q.C("channel_id") + " = " + q.C("target_channel_id")
	// Only the adopted model is needed. With no explicit winner, the
	// request/channel/id index can return the last attempt in one lookup.
	e := sql.Table("request_executions").As("chosen")
	e.SetDialect(db)
	chosen := d.Select(e.C("model_id")).From(e).Where(sql.And(
		sql.ColumnsEQ(e.C("request_id"), q.C("id")), sql.ColumnsEQ(e.C("project_id"), q.C("project_id")),
		sql.ColumnsEQ(e.C("channel_id"), q.C("channel_id")),
		sql.Or(sql.IsNull(q.C("result_execution_id")), sql.ColumnsEQ(e.C("id"), q.C("result_execution_id"))),
	)).OrderBy(sql.Desc(e.C("id"))).Limit(1)
	chosenSQL, _ := chosen.Query()
	finalModel := "COALESCE(NULLIF(TRIM((" + chosenSQL + ")), ''), " + q.C("attribution_model_id") + ")"
	model := "CASE WHEN " + logical + " THEN " + finalModel + " ELSE COALESCE(NULLIF(TRIM(" + u.C("model_id") + "), ''), " + finalModel + ") END"
	complete := "COUNT(" + u.C("id") + ") > 0 AND COUNT(" + u.C("total_cost") + ") = COUNT(" + u.C("id") + ")"
	standard := "COUNT(" + u.C("id") + ") > 0 AND COUNT(" + u.C("statistics_standard_cost") + ") = COUNT(" + u.C("id") + ")"
	q.Select(sql.As(q.C("target_channel_id"), "channel_id"), sql.As(model, "model"),
		sql.As("CASE WHEN "+logical+" THEN 1 ELSE 0 END", "requests"),
		sql.As("CASE WHEN "+complete+" THEN 1 ELSE 0 END", "priced"),
		sql.As("CASE WHEN "+standard+" THEN 0 ELSE 1 END", "standard_unpriced"),
		sql.As("COUNT("+u.C("statistics_standard_cost")+")", "standard_known"),
		sql.As("COALESCE(SUM("+u.C("statistics_standard_cost")+"),0)", "standard_cost"))
	for _, field := range []string{"total_cost", "prompt_tokens", "completion_tokens", "prompt_cached_tokens"} {
		q.AppendSelect(sql.As("COALESCE(SUM("+u.C(field)+"),0)", field))
	}
	// All models billed to the same request/channel contribute a single parent
	// pricing sample, even when only some of those models have known prices.
	unit := q.C("id") + ", " + q.C("project_id") + ", " + q.C("target_channel_id")
	q.AppendSelect(sql.As(statisticsTimeBucket(db, q.C("created_at"), 3600), "hour_bucket"),
		sql.As("ROW_NUMBER() OVER (PARTITION BY "+unit+")", "unit_row"),
		sql.As("MIN(CASE WHEN "+complete+" THEN 1 ELSE 0 END) OVER (PARTITION BY "+unit+")", "unit_priced"))
	q.GroupBy(q.C("created_at"), q.C("id"), q.C("project_id"), q.C("channel_id"), q.C("target_channel_id"), "model").As("model_unit")
	tokens := "(" + q.C("prompt_tokens") + " + " + q.C("completion_tokens") + ")"
	total := d.Select(q.C("hour_bucket"), q.C("channel_id"), q.C("model"),
		sql.As("SUM(CASE WHEN "+q.C("unit_row")+" = 1 THEN "+q.C("unit_priced")+" ELSE 0 END)", "parent_priced"),
		sql.As("SUM(CASE WHEN "+q.C("unit_row")+" = 1 THEN 1 - "+q.C("unit_priced")+" ELSE 0 END)", "parent_unpriced"),
		sql.As("SUM("+q.C("requests")+")", "requests"), sql.As("SUM("+q.C("priced")+")", "priced"),
		sql.As("SUM(1 - "+q.C("priced")+")", "unpriced"), sql.As("SUM("+tokens+")", "tokens"),
		sql.As("SUM(CASE WHEN "+q.C("priced")+" = 1 AND "+tokens+" > 0 THEN 1 ELSE 0 END)", "token_priced_requests"),
		sql.As("SUM(CASE WHEN "+q.C("priced")+" = 1 AND "+tokens+" > 0 THEN "+tokens+" ELSE 0 END)", "priced_tokens"),
		sql.As("SUM(CASE WHEN "+q.C("priced")+" = 1 AND "+tokens+" > 0 THEN "+q.C("total_cost")+" ELSE 0 END)", "priced_cost")).From(q)
	for _, field := range []string{"total_cost", "prompt_tokens", "prompt_cached_tokens", "standard_unpriced", "standard_known", "standard_cost"} {
		total.AppendSelect(sql.As("SUM("+q.C(field)+")", field))
	}
	return total.GroupBy(q.C("hour_bucket"), q.C("channel_id"), q.C("model"))
}

type channelModelTotals struct {
	HourBucket          int64   `json:"hour_bucket"`
	ParentPriced        int     `json:"parent_priced"`
	ParentUnpriced      int     `json:"parent_unpriced"`
	ChannelID           int     `json:"channel_id"`
	Model               string  `json:"model"`
	Requests            int     `json:"requests"`
	Priced              int     `json:"priced"`
	Unpriced            int     `json:"unpriced"`
	Tokens              int64   `json:"tokens"`
	TokenPricedRequests int     `json:"token_priced_requests"`
	PricedTokens        int64   `json:"priced_tokens"`
	PricedCost          float64 `json:"priced_cost"`
	TotalCost           float64 `json:"total_cost"`
	PromptTokens        int64   `json:"prompt_tokens"`
	CacheRead           int64   `json:"prompt_cached_tokens"`
	StandardUnpriced    int     `json:"standard_unpriced"`
	StandardKnown       int     `json:"standard_known"`
	StandardCost        float64 `json:"standard_cost"`
}

func readChannelModelTotals(ctx context.Context, client *ent.Client, ids []int, start, end time.Time) ([]channelModelTotals, error) {
	var rows []channelModelTotals
	query, args := channelUsageModelAggregate(client.Driver().Dialect(), ids, start, end).Query()
	var cursor sql.Rows
	if err := client.Driver().Query(ctx, query, args, &cursor); err != nil {
		return nil, err
	}
	defer cursor.Close()
	if err := sql.ScanSlice(&cursor, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func channelUsageModels(rows []channelModelTotals, attempts []consumptionGroup) map[int][]*objects.ChannelTodayModelUsage {
	byChannel := make(map[int][]*objects.ChannelTodayModelUsage, len(rows))
	type key struct {
		channel int
		model   string
	}
	byModel := map[key]*objects.ChannelTodayModelUsage{}
	for _, row := range rows {
		value := &objects.ChannelTodayModelUsage{Model: row.Model, RequestCount: row.Requests, PricedCount: row.Priced, UnpricedCount: row.Unpriced,
			TotalTokens: row.Tokens, TotalCost: row.TotalCost, StandardUnpricedCount: row.StandardUnpriced, TokenPricedRequestCount: row.TokenPricedRequests,
			AvgCostPerMillionTokens: consumptionRatio(row.PricedCost*1_000_000, row.PricedTokens), CacheHitRate: consumptionRatio(float64(row.CacheRead), row.PromptTokens)}
		if value.CacheHitRate != nil {
			*value.CacheHitRate = min(1, max(0, *value.CacheHitRate))
		}
		if row.StandardKnown > 0 {
			value.StandardCost = &row.StandardCost
		}
		byModel[key{row.ChannelID, row.Model}] = value
		byChannel[row.ChannelID] = append(byChannel[row.ChannelID], value)
	}
	for _, attempt := range attempts {
		value := byModel[key{attempt.EntityID, attempt.Model}]
		if value == nil {
			value = &objects.ChannelTodayModelUsage{Model: attempt.Model}
			byModel[key{attempt.EntityID, attempt.Model}] = value
			byChannel[attempt.EntityID] = append(byChannel[attempt.EntityID], value)
		}
		metrics := attempt.metrics()
		value.SuccessRate, value.AvgFirstTokenMs, value.AvgTokensPerSecond = metrics.SuccessRate, metrics.AvgFirstTokenMs, metrics.AvgTokensPerSecond
	}
	for _, models := range byChannel {
		slices.SortFunc(models, func(a, b *objects.ChannelTodayModelUsage) int {
			if order := cmp.Compare(b.TotalCost, a.TotalCost); order != 0 {
				return order
			}
			return cmp.Compare(a.Model, b.Model)
		})
	}
	return byChannel
}
