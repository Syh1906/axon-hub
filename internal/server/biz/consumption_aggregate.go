package biz

import (
	"cmp"
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func consumptionEntries(rows []consumptionGroup, input ConsumptionFilter) []*ConsumptionEntry {
	entries := make([]*ConsumptionEntry, 0)
	byID := make(map[int]*ConsumptionEntry)
	totals := make(map[int]*consumptionGroup)
	entityType := ent.TypeUser
	if input.Dimension == ConsumptionDimensionAPIKey {
		entityType = ent.TypeAPIKey
	}
	if input.Dimension == ConsumptionDimensionChannel {
		entityType = ent.TypeChannel
	}
	for _, row := range rows {
		entry := byID[row.EntityID]
		if entry == nil {
			name := row.Name
			if input.Dimension == ConsumptionDimensionUser {
				name = strings.TrimSpace(row.FirstName + " " + row.LastName)
				if name == "" {
					name = row.Email
				}
			}
			entry = &ConsumptionEntry{ID: objects.GUID{Type: entityType, ID: row.EntityID}, Name: name, Models: make([]*ConsumptionModel, 0)}
			byID[row.EntityID], totals[row.EntityID] = entry, &consumptionGroup{}
			entries = append(entries, entry)
		}
		if row.Model != "" {
			entry.Models = append(entry.Models, &ConsumptionModel{Model: row.Model, Metrics: row.metrics()})
		}
		total := totals[row.EntityID]
		total.Requests += row.Requests
		total.Tokens += row.Tokens
		total.Cost += row.Cost
		total.CacheRequests += row.CacheRequests
		total.CacheRead += row.CacheRead
		total.CacheInput += row.CacheInput
		total.CacheCreationCost += row.CacheCreationCost
		total.SuccessCount += row.SuccessCount
		total.OutcomeCount += row.OutcomeCount
		total.FirstTokenSum += row.FirstTokenSum
		total.FirstTokenCount += row.FirstTokenCount
		total.SpeedSum += row.SpeedSum
		total.SpeedCount += row.SpeedCount
	}
	for _, entry := range entries {
		entry.Metrics = totals[entry.ID.ID].metrics()
		slices.SortStableFunc(entry.Models, func(a, b *ConsumptionModel) int {
			if input.Metric == ConsumptionMetricCache {
				if order := compareOptionalDescending(a.Metrics.CacheHitRate, b.Metrics.CacheHitRate); order != 0 {
					return order
				}
				if order := cmp.Compare(b.Metrics.RequestCount, a.Metrics.RequestCount); order != 0 {
					return order
				}
			} else if order := cmp.Compare(b.Metrics.TotalCost, a.Metrics.TotalCost); order != 0 {
				return order
			}
			return strings.Compare(a.Model, b.Model)
		})
	}
	return entries
}

func (r consumptionGroup) metrics() ConsumptionMetrics {
	metrics := ConsumptionMetrics{RequestCount: r.Requests, TotalTokens: r.Tokens, TotalCost: r.Cost, CacheRequests: r.CacheRequests, CacheReadTokens: r.CacheRead, CacheInputTokens: r.CacheInput, CacheCreationCost: r.CacheCreationCost}
	metrics.AvgCostPerRequest = consumptionRatio(r.Cost, int64(r.Requests))
	metrics.AvgCostPerMillionTokens = consumptionRatio(r.Cost*1_000_000, r.Tokens)
	metrics.SuccessRate = consumptionRatio(float64(r.SuccessCount), r.OutcomeCount)
	metrics.AvgFirstTokenMs = consumptionRatio(r.FirstTokenSum, r.FirstTokenCount)
	metrics.AvgTokensPerSecond = consumptionRatio(r.SpeedSum, r.SpeedCount)
	metrics.CacheHitRate = consumptionRatio(float64(r.CacheRead), r.CacheInput)
	if metrics.CacheHitRate == nil && r.CacheRequests > 0 {
		value := 0.0
		metrics.CacheHitRate = &value
	}
	if metrics.CacheHitRate != nil {
		*metrics.CacheHitRate = min(1, max(0, *metrics.CacheHitRate))
	}
	return metrics
}

func consumptionRatio(numerator float64, denominator int64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := numerator / float64(denominator)
	return &value
}

func compareOptionalDescending[T cmp.Ordered](a, b *T) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	return cmp.Compare(*b, *a)
}

func sortConsumptionEntries(entries []*ConsumptionEntry, input ConsumptionFilter) {
	slices.SortStableFunc(entries, func(a, b *ConsumptionEntry) int {
		if input.Metric == ConsumptionMetricCache {
			if input.Dimension == ConsumptionDimensionChannel {
				if order := compareOptionalDescending(a.Metrics.CacheCoefficient, b.Metrics.CacheCoefficient); order != 0 {
					return order
				}
			}
			if order := compareOptionalDescending(a.Metrics.CacheHitRate, b.Metrics.CacheHitRate); order != 0 {
				return order
			}
			if order := cmp.Compare(b.Metrics.RequestCount, a.Metrics.RequestCount); order != 0 {
				return order
			}
		} else if order := cmp.Compare(b.Metrics.TotalCost, a.Metrics.TotalCost); order != 0 {
			return order
		}
		return cmp.Compare(a.ID.ID, b.ID.ID)
	})
}
