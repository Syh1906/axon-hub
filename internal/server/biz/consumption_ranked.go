package biz

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

type consumptionRanking struct {
	result *ConsumptionResult
	until  time.Time
}

func (s *ConsumptionService) cachedRanking(key string) *ConsumptionResult {
	value, ok := s.rankings.Get(key)
	if ok && value.until.After(time.Now()) {
		return value.result
	}
	return nil
}

// 长日期范围只向浏览器发送期间排名。翻页固定到首次读取的修订，避免新账目移动已显示的行。
func (s *ConsumptionService) RankedPage(ctx context.Context, input ConsumptionFilter, projectUsage bool, offset int, revision, order *string, descending bool, providers []string, asOf *time.Time) (*ConsumptionRankedPage, error) {
	if offset < 0 || (offset > 0 && revision == nil) {
		return nil, fmt.Errorf("invalid consumption page offset")
	}
	var project int
	var err error
	view := "leaderboard"
	if projectUsage {
		view = "usage"
		project, err = usageStatisticsProjectID(ctx, input)
	} else {
		project, err = leaderboardProjectID(ctx, input)
	}
	if err != nil {
		return nil, err
	}
	filter := input
	filter.AllowRankedPage = false
	if input.Dimension == ConsumptionDimensionChannel || input.Dimension == ConsumptionDimensionAPIKeyGroup || input.Dimension == ConsumptionDimensionModel {
		filter.Search = nil
	}
	if input.Dimension == ConsumptionDimensionAPIKeyGroup {
		filter.GroupRef = nil
	}
	scope, err := json.Marshal([]any{view, project, filter})
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("consumption:ranked:v1:%x:", sha256.Sum256(scope))
	if asOf != nil && asOf.After(time.Now().Add(time.Second)) {
		return nil, fmt.Errorf("invalid consumption snapshot time")
	}
	var result *ConsumptionResult
	if revision != nil && len(*revision) == 64 {
		result = s.cachedRanking(prefix + *revision)
	}
	if result == nil {
		manifest := filter
		manifest.revisionsOnly = true
		var first *ConsumptionSlicePage
		var states []*ConsumptionSliceRevision
		var cursor *time.Time
		for {
			page, err := s.ReadSlices(ctx, manifest, cursor, nil, asOf, false, view)
			if err != nil {
				return nil, err
			}
			if first == nil {
				first = page
				asOf = &page.ResolvedAt
			}
			if page.Pending {
				return &ConsumptionRankedPage{Pending: true, ResolvedAt: *asOf}, nil
			}
			states = append(states, page.Days...)
			cursor = page.Next
			if cursor == nil {
				break
			}
		}
		if first.Next != nil {
			check, err := s.ReadSlices(ctx, filter, nil, states, asOf, true, view)
			if err != nil {
				return nil, err
			}
			if check.Pending {
				return &ConsumptionRankedPage{Pending: true, ResolvedAt: *asOf}, nil
			}
		}
		// ALL 的截止时刻随每次刷新变化。同一批日期及修订复用数值，响应仍采用本次读取的截止时刻。
		var revisionEnd *time.Time
		if filter.Period != ConsumptionPeriodAll {
			revisionEnd = &first.RangeEnd
		}
		bytes, err := json.Marshal([]any{first.Timezone, first.CurrencyCode, first.RangeStart, revisionEnd, states})
		if err != nil {
			return nil, err
		}
		version := fmt.Sprintf("%x", sha256.Sum256(bytes))
		if revision != nil && *revision != version {
			return nil, fmt.Errorf("consumption ranking changed; refresh the table")
		}
		key := prefix + version
		result, err = s.sharedQuery(ctx, key, func(ctx context.Context) (*ConsumptionResult, error) {
			if cached := s.cachedRanking(key); cached != nil {
				return cached, nil
			}
			totals := newConsumptionRangeTotals()
			var after *time.Time
			pos := 0
			for {
				page, err := s.ReadSlices(ctx, filter, after, nil, asOf, false, view)
				if err != nil {
					return nil, err
				}
				if page.Pending || page.Timezone != first.Timezone || page.CurrencyCode != first.CurrencyCode || !page.RangeEnd.Equal(first.RangeEnd) {
					return nil, nil
				}
				for _, day := range page.Days {
					if pos >= len(states) || !day.Start.Equal(states[pos].Start) || day.Revision != states[pos].Revision {
						return nil, nil
					}
					pos++
				}
				for _, d := range page.Slices {
					totals.add(d)
				}
				after = page.Next
				if after == nil {
					break
				}
			}
			if pos != len(states) {
				return nil, nil
			}
			value := totals.finish(filter, first, version)
			s.rankings.Add(key, consumptionRanking{value, time.Now().Add(5 * time.Minute)})
			return value, nil
		})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return &ConsumptionRankedPage{Pending: true, ResolvedAt: *asOf}, nil
		}
	}
	var ordered *ConsumptionResult
	var orderKey string
	if order != nil || input.Search != nil || input.GroupRef != nil {
		params, err := json.Marshal([]any{input.Search, input.GroupRef, providers, order, descending})
		if err != nil {
			return nil, err
		}
		orderKey = fmt.Sprintf("%s%s:order:%x", prefix, result.Revision, sha256.Sum256(params))
		ordered = s.cachedRanking(orderKey)
	}
	if ordered == nil {
		ordered, err = orderConsumptionRanking(result, input, providers, order, descending)
		if err != nil {
			return nil, err
		}
		if orderKey != "" && max(len(ordered.Entries), len(ordered.ModelEntries)) > consumptionRankPageSize {
			s.rankings.Add(orderKey, consumptionRanking{ordered, time.Now().Add(5 * time.Minute)})
		}
	}
	page, err := consumptionRankWindow(ordered, input.Dimension, offset)
	if err != nil {
		return nil, err
	}
	if asOf != nil {
		page.ResolvedAt = *asOf
		page.Result.ResolvedAt = *asOf
		if filter.Period == ConsumptionPeriodAll {
			page.Result.RangeEnd = *asOf
		}
	}
	return page, nil
}

const consumptionRankPageSize = 64

func orderConsumptionRanking(full *ConsumptionResult, input ConsumptionFilter, providers []string, order *string, descending bool) (*ConsumptionResult, error) {
	out := *full
	search := ""
	if input.Search != nil {
		search = strings.ToLower(strings.TrimSpace(*input.Search))
	}
	match := func(value string) bool { return strings.Contains(strings.ToLower(value), search) }
	if (input.Dimension == ConsumptionDimensionAPIKeyGroup && (search != "" || input.GroupRef != nil)) || (input.Dimension == ConsumptionDimensionChannel && search != "") {
		out.Entries = nil
		for _, entry := range full.Entries {
			if input.Dimension == ConsumptionDimensionAPIKeyGroup && ((input.GroupRef != nil && entry.ID.ID != *input.GroupRef) || !match(entry.Name)) {
				continue
			}
			if input.Dimension == ConsumptionDimensionChannel && !match(entry.Name) && !match(entry.ChannelProvider) && !slices.Contains(providers, entry.ChannelProvider) && !slices.ContainsFunc(entry.ChannelTags, match) {
				continue
			}
			out.Entries = append(out.Entries, entry)
		}
	}
	if input.Dimension == ConsumptionDimensionModel && search != "" {
		out.ModelEntries = nil
		for _, model := range full.ModelEntries {
			if match(model.Model) {
				out.ModelEntries = append(out.ModelEntries, model)
			}
		}
	}
	if order != nil {
		out.Entries = slices.Clone(out.Entries)
		out.ModelEntries = slices.Clone(out.ModelEntries)
		if _, ok := rankMetric(ConsumptionMetrics{}, *order); !ok {
			return nil, fmt.Errorf("unknown consumption sort field")
		}
		compare := func(a, b ConsumptionMetrics) int {
			left, _ := rankMetric(a, *order)
			right, _ := rankMetric(b, *order)
			if left == nil || right == nil {
				if left == right {
					return 0
				}
				if left == nil {
					return 1
				}
				return -1
			}
			result := cmp.Compare(*left, *right)
			if descending {
				return -result
			}
			return result
		}
		slices.SortStableFunc(out.Entries, func(a, b *ConsumptionEntry) int { return compare(a.Metrics, b.Metrics) })
		slices.SortStableFunc(out.ModelEntries, func(a, b *ConsumptionModel) int { return compare(a.Metrics, b.Metrics) })
	}
	return &out, nil
}

func consumptionRankWindow(full *ConsumptionResult, dimension ConsumptionDimension, offset int) (*ConsumptionRankedPage, error) {
	out := *full
	total := max(len(out.Entries), len(out.ModelEntries))
	if offset > total {
		return nil, fmt.Errorf("invalid consumption rank offset")
	}
	end := offset + min(consumptionRankPageSize, total-offset)
	if dimension == ConsumptionDimensionModel {
		out.ModelEntries = out.ModelEntries[offset:end]
	} else {
		out.Entries = out.Entries[offset:end]
	}
	page := &ConsumptionRankedPage{Result: &out, ResolvedAt: out.ResolvedAt}
	if end < total {
		page.NextOffset = &end
	}
	return page, nil
}

func rankMetric(m ConsumptionMetrics, key string) (*float64, bool) {
	switch key {
	case "requestCount":
		return new(float64(m.RequestCount)), true
	case "totalTokens":
		return new(float64(m.TotalTokens)), true
	case "totalCost":
		return &m.TotalCost, true
	case "channelCost":
		return m.ChannelCost, true
	case "billedCost":
		return m.BilledCost, true
	case "cacheRequests":
		return new(float64(m.CacheRequests)), true
	case "cacheReadTokens":
		return new(float64(m.CacheReadTokens)), true
	case "cacheInputTokens":
		return new(float64(m.CacheInputTokens)), true
	case "cacheHitRate":
		return m.CacheHitRate, true
	case "cacheCoefficient":
		if m.CacheCoefficient == nil {
			return nil, true
		}
		return new(float64(*m.CacheCoefficient)), true
	case "avgCostPerRequest":
		return m.AvgCostPerRequest, true
	case "avgCostPerMillionTokens":
		return m.AvgCostPerMillionTokens, true
	case "avgFirstTokenMs":
		return m.AvgFirstTokenMs, true
	case "avgTokensPerSecond":
		return m.AvgTokensPerSecond, true
	case "successRate":
		return m.SuccessRate, true
	default:
		return nil, false
	}
}
