package biz

import (
	"cmp"
	"context"
	"slices"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/shopspring/decimal"
)

const channelUsageBatchSize = 256

// Read only the requested channel/day, in bounded batches. Pricing JSON stays
// on the server; the browser receives model totals, never individual requests.
func queryChannelTodayUsageModels(ctx context.Context, client *ent.Client, id int, start, end time.Time) ([]*objects.ChannelTodayModelUsage, error) {
	query := client.Request.Query().Where(request.ChannelIDEQ(id), request.CreatedAtGTE(start.UTC()), request.CreatedAtLT(end.UTC()))
	lastID, err := query.Clone().Order(ent.Desc(request.FieldID)).FirstID(ctx)
	if ent.IsNotFound(err) {
		return []*objects.ChannelTodayModelUsage{}, nil
	}
	if err != nil {
		return nil, err
	}
	type modelTotals struct {
		objects.ChannelTodayModelUsage
		input, read   int64
		standard      decimal.Decimal
		standardKnown bool
		pricedCost    float64
		pricedTokens  int64
	}
	models := map[string]*modelTotals{}
	for after := 0; after < lastID; {
		var requests []struct {
			ID        int    `json:"id"`
			ProjectID int    `json:"project_id"`
			Model     string `json:"model"`
		}
		err := query.Clone().Where(request.IDGT(after), request.IDLTE(lastID)).Order(ent.Asc(request.FieldID)).Limit(channelUsageBatchSize).
			Modify(func(q *sql.Selector) {
				execution := joinLatestConsumptionExecution(q)
				q.Select(q.C("id"), q.C("project_id"), sql.As("COALESCE(NULLIF(TRIM("+execution.C("model_id")+"), ''), "+q.C("model_id")+")", "model"))
			}).Scan(ctx, &requests)
		if err != nil {
			return nil, err
		}
		if len(requests) == 0 {
			break
		}
		ids := make([]int, len(requests))
		for i, req := range requests {
			ids[i] = req.ID
		}
		logs, err := client.UsageLog.Query().Where(usagelog.RequestIDIn(ids...), usagelog.ChannelIDEQ(id)).Select(
			usagelog.FieldRequestID, usagelog.FieldProjectID, usagelog.FieldTotalCost, usagelog.FieldCostItems, usagelog.FieldCostPriceReferenceID,
			usagelog.FieldID, usagelog.FieldCreatedAt, usagelog.FieldPromptTokens, usagelog.FieldCompletionTokens, usagelog.FieldTotalTokens,
			usagelog.FieldPromptCachedTokens, usagelog.FieldPromptWriteCachedTokens, usagelog.FieldPromptWriteCachedTokens5m, usagelog.FieldPromptWriteCachedTokens1h,
		).All(ctx)
		if err != nil {
			return nil, err
		}
		versions, err := loadStandardCostVersions(ctx, client, logs)
		if err != nil {
			return nil, err
		}
		byRequest := make(map[int][]*ent.UsageLog, len(requests))
		for _, row := range logs {
			byRequest[row.RequestID] = append(byRequest[row.RequestID], row)
		}
		for _, req := range requests {
			total := models[req.Model]
			if total == nil {
				total = &modelTotals{ChannelTodayModelUsage: objects.ChannelTodayModelUsage{Model: req.Model}}
				models[req.Model] = total
			}
			total.RequestCount++
			seen, unpriced, standardMissing := false, false, false
			var requestCost float64
			var requestTokens int64
			for _, row := range byRequest[req.ID] {
				if row.ProjectID != req.ProjectID {
					continue
				}
				seen = true
				tokens := row.PromptTokens + row.CompletionTokens
				total.TotalTokens += tokens
				requestTokens += tokens
				total.input += row.PromptTokens
				total.read += row.PromptCachedTokens
				if row.TotalCost == nil {
					unpriced = true
				} else {
					total.TotalCost += *row.TotalCost
					requestCost += *row.TotalCost
				}
				if cost, ok := usageStandardCost(row, versions); ok {
					total.standard = total.standard.Add(cost)
					total.standardKnown = true
				} else {
					standardMissing = true
				}
			}
			if !seen || unpriced {
				total.UnpricedCount++
			} else if requestTokens > 0 {
				total.pricedCost += requestCost
				total.pricedTokens += requestTokens
				total.TokenPricedRequestCount++
			}
			if !seen || standardMissing {
				total.StandardUnpricedCount++
			}
		}
		after = requests[len(requests)-1].ID
	}
	result := make([]*objects.ChannelTodayModelUsage, 0, len(models))
	for _, total := range models {
		total.AvgCostPerMillionTokens = consumptionRatio(total.pricedCost*1_000_000, total.pricedTokens)
		if total.input > 0 {
			rate := min(1, max(0, float64(total.read)/float64(total.input)))
			total.CacheHitRate = &rate
		}
		if total.standardKnown {
			value := total.standard.InexactFloat64()
			total.StandardCost = &value
		}
		result = append(result, &total.ChannelTodayModelUsage)
	}
	slices.SortFunc(result, func(a, b *objects.ChannelTodayModelUsage) int {
		if order := cmp.Compare(b.TotalCost, a.TotalCost); order != 0 {
			return order
		}
		return cmp.Compare(a.Model, b.Model)
	})
	return result, nil
}
