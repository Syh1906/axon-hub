package biz

import (
	"context"
	stdsql "database/sql"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
)

// RequestUsageSummary is a read model, not a synthetic UsageLog. Individual
// ledger rows keep their IDs, channels, prices and audit history unchanged.
type RequestUsageSummary struct {
	UsageRecords                       int
	ArchivedRecords                    int
	UnpricedRecords                    int
	PromptTokens                       int64
	CompletionTokens                   int64
	TotalTokens                        int64
	PromptCachedTokens                 int64
	PromptWriteCachedTokens            int64
	CompletionReasoningTokens          int64
	PromptAudioTokens                  *int64
	PromptWriteCachedTokens5m          *int64
	PromptWriteCachedTokens1h          *int64
	CompletionAudioTokens              *int64
	CompletionAcceptedPredictionTokens *int64
	CompletionRejectedPredictionTokens *int64
	TotalCost                          *float64
	CostItems                          []objects.CostItem
	CostPriceMultiplier                *decimal.Decimal
}

// RequestUsageSummaries authorizes the requested page first, then reads its
// retained and archived billing in batches. No request/response bodies, channel
// credentials or current prices are loaded. Unknown prices stay distinct from
// measured zero, and archived rows do not invent deleted cost breakdowns.
func RequestUsageSummaries(ctx context.Context, client *ent.Client, ids []int) (map[int]*RequestUsageSummary, error) {
	if _, transactional := client.Driver().(dialect.Tx); transactional {
		return requestUsageSummariesSnapshot(ctx, client, ids)
	}
	options := &stdsql.TxOptions{ReadOnly: true, Isolation: stdsql.LevelRepeatableRead}
	if client.Driver().Dialect() == dialect.SQLite {
		options = &stdsql.TxOptions{}
	}
	tx, err := client.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return requestUsageSummariesSnapshot(ctx, tx.Client(), ids)
}

func requestUsageSummariesSnapshot(ctx context.Context, client *ent.Client, ids []int) (map[int]*RequestUsageSummary, error) {
	result := make(map[int]*RequestUsageSummary, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	visible, err := client.Request.Query().Where(request.IDIn(ids...)).IDs(ctx)
	if err != nil || len(visible) == 0 {
		return result, err
	}
	for _, id := range visible {
		result[id] = &RequestUsageSummary{CostItems: []objects.CostItem{}}
	}
	matchProject := func(q *sql.Selector) {
		r := sql.Table(request.Table).As("summary_request")
		q.Join(r).On(q.C("request_id"), r.C("id")).Where(sql.ColumnsEQ(q.C("project_id"), r.C("project_id")))
	}
	logs, err := client.UsageLog.Query().Where(usagelog.RequestIDIn(visible...)).Select(
		usagelog.FieldRequestID, usagelog.FieldPromptTokens, usagelog.FieldCompletionTokens, usagelog.FieldTotalTokens,
		usagelog.FieldPromptCachedTokens, usagelog.FieldPromptWriteCachedTokens, usagelog.FieldCompletionReasoningTokens,
		usagelog.FieldPromptAudioTokens, usagelog.FieldPromptWriteCachedTokens5m, usagelog.FieldPromptWriteCachedTokens1h,
		usagelog.FieldCompletionAudioTokens, usagelog.FieldCompletionAcceptedPredictionTokens, usagelog.FieldCompletionRejectedPredictionTokens,
		usagelog.FieldTotalCost, usagelog.FieldCostItems, usagelog.FieldCostSnapshot, usagelog.FieldCostPriceReferenceID,
	).Modify(matchProject).All(ctx)
	if err != nil {
		return nil, err
	}
	archives, err := client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage), usagearchive.RequestIDIn(visible...)).
		Select(usagearchive.FieldRequestID, usagearchive.FieldPromptTokens, usagearchive.FieldCompletionTokens, usagearchive.FieldTotalTokens,
			usagearchive.FieldPromptCachedTokens, usagearchive.FieldPromptWriteCachedTokens, usagearchive.FieldCompletionReasoningTokens, usagearchive.FieldTotalCost).
		Modify(matchProject).All(ctx)
	if err != nil {
		return nil, err
	}
	refs := lo.Uniq(lo.FilterMap(logs, func(row *ent.UsageLog, _ int) (string, bool) {
		return row.CostPriceReferenceID, row.TotalCost != nil && row.CostSnapshot == nil && row.CostPriceReferenceID != ""
	}))
	multipliers, err := LoadUsagePriceMultipliers(ctx, client, refs)
	if err != nil {
		return nil, err
	}
	mixed := make(map[int]bool)
	addCost := func(summary *RequestUsageSummary, amount *float64) {
		summary.UsageRecords++
		if amount == nil {
			summary.UnpricedRecords++
			return
		}
		if summary.TotalCost == nil {
			summary.TotalCost = new(float64)
		}
		*summary.TotalCost += *amount
	}
	for _, row := range logs {
		summary := result[row.RequestID]
		addCost(summary, row.TotalCost)
		summary.PromptTokens += row.PromptTokens
		summary.CompletionTokens += row.CompletionTokens
		summary.TotalTokens += row.TotalTokens
		summary.PromptCachedTokens += row.PromptCachedTokens
		summary.PromptWriteCachedTokens += row.PromptWriteCachedTokens
		summary.CompletionReasoningTokens += row.CompletionReasoningTokens
		for _, value := range []struct {
			sum   **int64
			value int64
		}{
			{&summary.PromptAudioTokens, row.PromptAudioTokens}, {&summary.PromptWriteCachedTokens5m, row.PromptWriteCachedTokens5m},
			{&summary.PromptWriteCachedTokens1h, row.PromptWriteCachedTokens1h}, {&summary.CompletionAudioTokens, row.CompletionAudioTokens},
			{&summary.CompletionAcceptedPredictionTokens, row.CompletionAcceptedPredictionTokens}, {&summary.CompletionRejectedPredictionTokens, row.CompletionRejectedPredictionTokens},
		} {
			if *value.sum == nil {
				*value.sum = new(int64)
			}
			**value.sum += value.value
		}
		summary.CostItems = append(summary.CostItems, row.CostItems...)
		if row.TotalCost == nil {
			continue
		}
		multiplier := decimal.NewFromInt(1)
		if saved, ok := multipliers[row.CostPriceReferenceID]; ok {
			multiplier = saved
		}
		if row.CostSnapshot != nil {
			saved := row.CostSnapshot.DisplayMultiplier(row.CostItems)
			if saved == nil {
				mixed[row.RequestID] = true
				continue
			}
			multiplier = *saved
		}
		if summary.CostPriceMultiplier == nil {
			summary.CostPriceMultiplier = &multiplier
		} else if !summary.CostPriceMultiplier.Equal(multiplier) {
			mixed[row.RequestID] = true
		}
	}
	for _, row := range archives {
		summary := result[row.RequestID]
		addCost(summary, row.TotalCost)
		summary.ArchivedRecords++
		summary.PromptTokens += row.PromptTokens
		summary.CompletionTokens += row.CompletionTokens
		summary.TotalTokens += row.TotalTokens
		summary.PromptCachedTokens += row.PromptCachedTokens
		summary.PromptWriteCachedTokens += row.PromptWriteCachedTokens
		summary.CompletionReasoningTokens += row.CompletionReasoningTokens
		// Numeric archives retain totals, not modality detail or price JSON.
		summary.PromptAudioTokens, summary.CompletionAudioTokens = nil, nil
		summary.PromptWriteCachedTokens5m, summary.PromptWriteCachedTokens1h = nil, nil
		summary.CompletionAcceptedPredictionTokens, summary.CompletionRejectedPredictionTokens = nil, nil
		summary.CostItems = nil
		mixed[row.RequestID] = true
	}
	for id := range mixed {
		result[id].CostPriceMultiplier = nil
	}
	return result, nil
}
