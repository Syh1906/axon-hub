package biz

import (
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

type consumptionRangeEntity struct {
	entry  ConsumptionEntry
	total  consumptionGroup
	models map[string]*consumptionGroup
}

type consumptionRangeTotals struct {
	entries   map[objects.GUID]*consumptionRangeEntity
	models    map[string]*consumptionGroup
	attempts  map[objects.GUID]map[string]*consumptionGroup
	cache     map[objects.GUID]map[string]*consumptionCacheGroup
	generated time.Time
}

func newConsumptionRangeTotals() *consumptionRangeTotals {
	return &consumptionRangeTotals{
		entries: map[objects.GUID]*consumptionRangeEntity{}, models: map[string]*consumptionGroup{},
		attempts: map[objects.GUID]map[string]*consumptionGroup{}, cache: map[objects.GUID]map[string]*consumptionCacheGroup{},
	}
}

func consumptionMetricGroup(m ConsumptionMetrics) consumptionGroup {
	group := consumptionGroup{
		Requests: m.RequestCount, Tokens: m.TotalTokens, Cost: m.TotalCost,
		PricedRequests: m.PricedRequestCount, UnpricedRequests: m.UnpricedRequestCount, TokenPricedRequests: m.TokenPricedRequestCount,
		PricedCost: m.PricedCost, TokenPricedCost: m.TokenPricedCost, PricedTokens: m.PricedTokens,
		CacheRequests: m.CacheRequests, CacheRead: m.CacheReadTokens, CacheInput: m.CacheInputTokens, CacheCreationCost: m.CacheCreationCost,
		SuccessCount: m.SuccessCount, OutcomeCount: m.OutcomeCount, FirstTokenSum: m.FirstTokenSum, FirstTokenCount: m.FirstTokenCount,
		SpeedSum: m.SpeedSum, SpeedCount: m.SpeedSampleCount,
	}
	if m.ChannelCost != nil {
		group.ChannelCost, group.ChannelRecords = *m.ChannelCost, 1
	}
	if m.BilledCost != nil {
		group.BilledCost, group.BilledRecords = *m.BilledCost, 1
	}
	return group
}

func rangeGroup[K comparable, T any](rows map[K]*T, key K) *T {
	if rows[key] == nil {
		rows[key] = new(T)
	}
	return rows[key]
}

func (r *consumptionRangeTotals) add(day *ConsumptionResult) {
	if r.generated.IsZero() || day.GeneratedAt.Before(r.generated) {
		r.generated = day.GeneratedAt
	}
	for _, model := range day.ModelEntries {
		rangeGroup(r.models, model.Model).add(consumptionMetricGroup(model.Metrics))
	}
	for _, entry := range day.Entries {
		entity := r.entries[entry.ID]
		if entity == nil {
			entity = &consumptionRangeEntity{entry: *entry, models: map[string]*consumptionGroup{}}
			entity.entry.Models = nil
			r.entries[entry.ID] = entity
		}
		entity.total.add(consumptionMetricGroup(entry.Metrics))
		for _, model := range entry.Models {
			rangeGroup(entity.models, model.Model).add(consumptionMetricGroup(model.Metrics))
		}
	}
	for _, sample := range day.PerformanceSamples {
		if r.attempts[sample.ChannelID] == nil {
			r.attempts[sample.ChannelID] = map[string]*consumptionGroup{}
		}
		rangeGroup(r.attempts[sample.ChannelID], sample.Model).add(consumptionMetricGroup(sample.Metrics))
	}
	for _, sample := range day.CacheSamples {
		if r.cache[sample.ChannelID] == nil {
			r.cache[sample.ChannelID] = map[string]*consumptionCacheGroup{}
		}
		rangeGroup(r.cache[sample.ChannelID], sample.Model).add(consumptionCacheGroup{Samples: sample.Samples, Eligible: sample.Eligible, Theoretical: sample.Theoretical, Observed: sample.Observed})
	}
}

func replaceRangePerformance(g *consumptionGroup, perf consumptionGroup) {
	g.SuccessCount, g.OutcomeCount = perf.SuccessCount, perf.OutcomeCount
	g.FirstTokenSum, g.FirstTokenCount = perf.FirstTokenSum, perf.FirstTokenCount
	g.SpeedSum, g.SpeedCount = perf.SpeedSum, perf.SpeedCount
}

func (r *consumptionRangeTotals) finish(input ConsumptionFilter, page *ConsumptionSlicePage, revision string) *ConsumptionResult {
	result := &ConsumptionResult{Revision: revision, GeneratedAt: r.generated, ResolvedAt: page.ResolvedAt, RangeStart: page.RangeStart, RangeEnd: page.RangeEnd, Timezone: page.Timezone, CurrencyCode: page.CurrencyCode}
	if result.GeneratedAt.IsZero() {
		result.GeneratedAt = page.ResolvedAt
	}
	for name, g := range r.models {
		result.ModelEntries = append(result.ModelEntries, &ConsumptionModel{Model: name, Metrics: g.metrics()})
	}
	sortConsumptionModels(result.ModelEntries, input)
	for id, e := range r.entries {
		var parentPerformance consumptionGroup
		for name, g := range e.models {
			if input.Dimension == ConsumptionDimensionChannel {
				var perf consumptionGroup
				if sample := r.attempts[id][name]; sample != nil {
					perf = *sample
				}
				replaceRangePerformance(g, perf)
				parentPerformance.add(perf)
			}
			m := g.metrics()
			if sample := r.cache[id][name]; sample != nil {
				m.CacheCoefficient = sample.coefficient()
				sample.attach(&m)
			}
			e.entry.Models = append(e.entry.Models, &ConsumptionModel{Model: name, Metrics: m})
		}
		if input.Dimension == ConsumptionDimensionChannel {
			replaceRangePerformance(&e.total, parentPerformance)
		}
		e.entry.Metrics = e.total.metrics()
		if len(r.cache[id]) > 0 {
			var total consumptionCacheGroup
			for _, group := range r.cache[id] {
				total.add(*group)
			}
			e.entry.Metrics.CacheCoefficient = total.coefficient()
			total.attach(&e.entry.Metrics)
		}
		sortConsumptionModels(e.entry.Models, input)
		result.Entries = append(result.Entries, &e.entry)
	}
	sortConsumptionEntries(result.Entries, input)
	return result
}
