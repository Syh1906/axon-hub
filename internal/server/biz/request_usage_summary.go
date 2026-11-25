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
	"github.com/looplj/axonhub/internal/scopes"
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
	CostMultiplierMixed                bool
	ChannelCost                        *float64
	ChannelCostMultiplier              *decimal.Decimal
	ChannelCostMultiplierMixed         bool
}

// An absent archive rate is unknown, while differing recorded rates are mixed.
type requestCostRate struct {
	value          *decimal.Decimal
	mixed, unknown bool
}

func (r *requestCostRate) add(rate *decimal.Decimal, mixed bool) {
	switch {
	case mixed:
		r.mixed = true
	case rate == nil:
		r.unknown = true
	case r.value == nil:
		r.value = rate
	case !r.value.Equal(*rate):
		r.mixed = true
	}
}

func (r requestCostRate) result() (*decimal.Decimal, bool) {
	if r.mixed || r.unknown {
		return nil, r.mixed
	}
	return r.value, false
}

func addRequestCost(total **float64, amount *float64) {
	if amount != nil {
		if *total == nil {
			*total = new(float64)
		}
		**total += *amount
	}
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
		usagelog.FieldBilledCost, usagelog.FieldBillingGroupID, usagelog.FieldBilledItems,
		usagelog.FieldTotalCost, usagelog.FieldCostItems, usagelog.FieldCostSnapshot, usagelog.FieldCostPriceReferenceID,
	).Modify(matchProject).All(ctx)
	if err != nil {
		return nil, err
	}
	archives, err := client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage), usagearchive.RequestIDIn(visible...)).
		Select(usagearchive.FieldRequestID, usagearchive.FieldPromptTokens, usagearchive.FieldCompletionTokens, usagearchive.FieldTotalTokens,
			usagearchive.FieldPromptCachedTokens, usagearchive.FieldPromptWriteCachedTokens, usagearchive.FieldCompletionReasoningTokens, usagearchive.FieldTotalCost,
			usagearchive.FieldBillingGroupID, usagearchive.FieldBilledCost, usagearchive.FieldBillingEvidence).
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
	type rates struct{ billed, channel requestCostRate }
	byRequest := make(map[int]*rates, len(visible))
	for _, id := range visible {
		byRequest[id] = &rates{}
	}
	showChannel := requireRateAdmin(ctx, scopes.ScopeReadChannels) == nil
	addCost := func(summary *RequestUsageSummary, amount *float64) {
		summary.UsageRecords++
		if amount == nil {
			summary.UnpricedRecords++
			return
		}
		addRequestCost(&summary.TotalCost, amount)
	}
	for _, row := range logs {
		summary := result[row.RequestID]
		amount, items := row.TotalCost, row.CostItems
		price := row.CostSnapshot
		if row.BillingGroupID != nil {
			amount = row.BilledCost
			if price != nil && price.Billing != nil && price.Billing.Rate != nil {
				items, price = row.BilledItems, price.BilledPrice()
			}
		}
		addCost(summary, amount)
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
		summary.CostItems = append(summary.CostItems, items...)
		fallback, ok := multipliers[row.CostPriceReferenceID]
		if !ok {
			fallback = decimal.NewFromInt(1)
		}
		if amount != nil {
			rate := &fallback
			if price != nil {
				rate = price.DisplayMultiplier(items)
			}
			byRequest[row.RequestID].billed.add(rate, rate == nil)
		}
		if showChannel && row.TotalCost != nil {
			addRequestCost(&summary.ChannelCost, row.TotalCost)
			rate := &fallback
			if row.CostSnapshot != nil {
				rate = row.CostSnapshot.DisplayMultiplier(row.CostItems)
			}
			byRequest[row.RequestID].channel.add(rate, rate == nil)
		}
	}
	for _, row := range archives {
		summary := result[row.RequestID]
		amount := row.TotalCost
		if row.BillingGroupID != nil {
			amount = row.BilledCost
		}
		addCost(summary, amount)
		r := byRequest[row.RequestID]
		e := row.BillingEvidence
		if amount != nil {
			if e == nil {
				r.billed.add(nil, false)
			} else {
				r.billed.add(e.Multiplier, e.Mixed)
			}
		}
		if showChannel && row.TotalCost != nil {
			addRequestCost(&summary.ChannelCost, row.TotalCost)
			if e == nil {
				r.channel.add(nil, false)
			} else if e.Source == "channel" {
				r.channel.add(e.Multiplier, e.Mixed)
			} else if len(e.Items) == 0 {
				r.channel.add(nil, false)
			} else {
				for _, item := range e.Items {
					if item.ChannelAmount != nil {
						r.channel.add(item.ChannelRate, false)
					}
				}
			}
		}
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
	}
	for id, r := range byRequest {
		result[id].CostPriceMultiplier, result[id].CostMultiplierMixed = r.billed.result()
		result[id].ChannelCostMultiplier, result[id].ChannelCostMultiplierMixed = r.channel.result()
	}
	return result, nil
}
