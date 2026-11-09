package biz

import (
	"cmp"
	"context"
	stdsql "database/sql"
	"slices"
	"time"

	"entgo.io/ent/dialect"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/shopspring/decimal"
)

const channelUsageBatchSize = 512

// Read only the requested channel/day, in bounded batches. Pricing JSON stays
// on the server; the browser receives model totals, never individual requests.
func queryChannelTodayUsageModels(ctx context.Context, client *ent.Client, id int, start, end time.Time) ([]*objects.ChannelTodayModelUsage, error) {
	if _, transactional := client.Driver().(dialect.Tx); transactional {
		return queryChannelTodayUsageModelsSnapshot(ctx, client, id, start, end)
	}
	// Keep requests, attempts, usage and historical pricing in one read snapshot.
	options := &stdsql.TxOptions{ReadOnly: true, Isolation: stdsql.LevelRepeatableRead}
	if client.Driver().Dialect() == dialect.SQLite {
		options = &stdsql.TxOptions{}
	}
	tx, err := client.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryChannelTodayUsageModelsSnapshot(ctx, tx.Client(), id, start, end)
}

func queryChannelTodayUsageModelsSnapshot(ctx context.Context, client *ent.Client, id int, start, end time.Time) ([]*objects.ChannelTodayModelUsage, error) {
	type modelTotals struct {
		objects.ChannelTodayModelUsage
		input, read   int64
		standard      decimal.Decimal
		standardKnown bool
		pricedCost    float64
		pricedTokens  int64
		performance   consumptionGroup
	}
	models := map[string]*modelTotals{}
	versions := map[string]objects.ModelPrice{}
	loadedVersions := map[string]bool{}
	var afterTime time.Time
	var afterID int
	for {
		selected, err := queryChannelUsageBatch(ctx, client, id, start, end, afterTime, afterID)
		if err != nil {
			return nil, err
		}
		if len(selected) == 0 {
			break
		}
		logs := make([]*ent.UsageLog, 0, len(selected))
		for i := range selected {
			if selected[i].ID == 0 || selected[i].Archived {
				continue
			}
			selected[i].UsageLog.CostItems = selected[i].CostItems
			logs = append(logs, &selected[i].UsageLog)
		}
		if err := loadStandardCostVersionsInto(ctx, client, logs, versions, loadedVersions); err != nil {
			return nil, err
		}
		requestCount, lastRequest := 0, 0
		for begin := 0; begin < len(selected); {
			req := selected[begin]
			endRow := begin + 1
			for endRow < len(selected) && selected[endRow].UsageRequestID == req.UsageRequestID && selected[endRow].Model == req.Model {
				endRow++
			}
			requestRows := selected[begin:endRow]
			if req.UsageRequestID != lastRequest {
				requestCount++
				lastRequest = req.UsageRequestID
			}
			begin = endRow
			total := models[req.Model]
			if total == nil {
				total = &modelTotals{ChannelTodayModelUsage: objects.ChannelTodayModelUsage{Model: req.Model}}
				models[req.Model] = total
			}
			if req.LogicalRequest {
				total.RequestCount++
			}
			seen, unpriced, standardMissing := false, false, false
			var requestCost float64
			var requestTokens int64
			for i := range requestRows {
				row := &requestRows[i].UsageLog
				if row.ID == 0 {
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
			} else {
				total.PricedCount++
				if requestTokens > 0 {
					total.pricedCost += requestCost
					total.pricedTokens += requestTokens
					total.TokenPricedRequestCount++
				}
			}
			if !seen || standardMissing {
				total.StandardUnpricedCount++
			}
		}
		last := selected[len(selected)-1]
		afterID, afterTime = last.UsageRequestID, last.RequestCreatedAt
		if requestCount < channelUsageBatchSize {
			break
		}
	}
	attempts, err := queryChannelAttemptGroups(ctx, client, 0, ConsumptionFilter{}, []int{id}, &start, end)
	if err != nil {
		return nil, err
	}
	for _, attempt := range attempts {
		total := models[attempt.Model]
		if total == nil {
			total = &modelTotals{ChannelTodayModelUsage: objects.ChannelTodayModelUsage{Model: attempt.Model}}
			models[attempt.Model] = total
		}
		total.performance = attempt
	}
	result := make([]*objects.ChannelTodayModelUsage, 0, len(models))
	for _, total := range models {
		metrics := total.performance.metrics()
		total.SuccessRate = metrics.SuccessRate
		total.AvgFirstTokenMs = metrics.AvgFirstTokenMs
		total.AvgTokensPerSecond = metrics.AvgTokensPerSecond
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
