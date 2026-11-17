package biz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
)

// Hour facts share the durable summary store, but never enter the date-job
// queue: query is NULL and pending is false. Their keys have a separate format
// version. The public date payload and its revision contract remain unchanged.
type consumptionHour struct {
	key        string
	start, end time.Time
	version    string
	stored     *ent.ConsumptionSnapshot
}

type consumptionHourPayload struct {
	Rows     []consumptionGroup      `json:"rows"`
	Attempts []consumptionGroup      `json:"attempts"`
	Cache    []consumptionCacheGroup `json:"cache"`
}

func (s *ConsumptionService) consumptionHours(ctx context.Context, query consumptionDayQuery) ([]consumptionHour, error) {
	revisions, err := readStatisticsRevisions(ctx, s.client, query.Project, query.Start, query.End)
	if err != nil {
		return nil, err
	}
	var hours []consumptionHour
	var keys []string
	for start := query.Start; start.Before(query.End); {
		end := start.UTC().Truncate(time.Hour).Add(time.Hour)
		if end.After(query.End) {
			end = query.End
		}
		part := query
		part.Start, part.End = start, end
		// Dates, search and selected groups belong to the presentation layer.
		part.Filter.StartDate, part.Filter.EndDate = nil, nil
		bytes, err := json.Marshal(part)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%x", sha256.Sum256(append([]byte("consumption-hour:v1:"), bytes...)))
		hours = append(hours, consumptionHour{key: key, start: start, end: end, version: statisticsFingerprint(revisions, start, end)})
		keys = append(keys, key)
		start = end
	}
	stored, err := s.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.CacheKeyIn(keys...)).
		Select(consumptionsnapshot.FieldID, consumptionsnapshot.FieldCacheKey, consumptionsnapshot.FieldFingerprint,
			consumptionsnapshot.FieldPayload, consumptionsnapshot.FieldRequestedAt).All(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*ent.ConsumptionSnapshot, len(stored))
	for _, row := range stored {
		byKey[row.CacheKey] = row
	}
	for i := range hours {
		hours[i].stored = byKey[hours[i].key]
	}
	return hours, nil
}

func (s *ConsumptionService) prepareConsumptionHours(ctx context.Context, query consumptionDayQuery, job *ent.ConsumptionSnapshot) error {
	version, err := s.consumptionFingerprint(ctx, query)
	if err != nil || len(job.Payload) > 0 && version == job.Fingerprint {
		return err
	}
	hours, err := s.consumptionHours(ctx, query)
	if err != nil {
		return err
	}
	for _, hour := range hours {
		if hour.stored != nil && hour.stored.Fingerprint == hour.version {
			continue
		}
		_, err := readStatisticsSnapshot(ctx, s.client, false, func(client *ent.Client) (*consumptionFacts, error) {
			reader := *s
			reader.client = client
			part := query
			part.Start, part.End = hour.start, hour.end
			current, err := reader.consumptionHours(ctx, part)
			if err != nil {
				return nil, err
			}
			return reader.readConsumptionHour(ctx, query, current[0])
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *ConsumptionService) readConsumptionHour(ctx context.Context, query consumptionDayQuery, hour consumptionHour) (*consumptionFacts, error) {
	if hour.stored != nil && hour.stored.Fingerprint == hour.version {
		var payload consumptionHourPayload
		if err := json.Unmarshal(hour.stored.Payload, &payload); err != nil {
			return nil, fmt.Errorf("decode consumption hour: %w", err)
		}
		return &consumptionFacts{rows: payload.Rows, attempts: payload.Attempts, cache: payload.Cache}, nil
	}
	facts, err := s.queryGroupsFactsSnapshot(ctx, query.Project, query.Filter, &hour.start, hour.end)
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(consumptionHourPayload{Rows: facts.rows, Attempts: facts.attempts, Cache: facts.cache})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	err = s.client.ConsumptionSnapshot.Create().SetCacheKey(hour.key).SetProjectID(query.Project).
		SetRangeStart(hour.start).SetRangeEnd(hour.end).SetGeneratedAt(now).SetRequestedAt(now).SetCheckedAt(now).SetLeaseUntil(now).
		SetPayload(bytes).SetFingerprint(hour.version).SetSourceVersion(hour.version).SetSnapshotVersion(hour.version).
		SetPending(false).OnConflictColumns(consumptionsnapshot.FieldCacheKey).UpdateNewValues().Exec(ctx)
	return facts, err
}

// Called within the final repeatable-read transaction. Cached hours are only
// reused when their source revision matches that same snapshot. Corrections
// since preparation recompute their affected hour, not the entire day.
func (s *ConsumptionService) consumptionHourlyFacts(ctx context.Context, query consumptionDayQuery) (*consumptionFacts, error) {
	hours, err := s.consumptionHours(ctx, query)
	if err != nil {
		return nil, err
	}
	result := &consumptionFacts{}
	parents := map[int]*consumptionBillingTotals{}
	type cacheKey struct {
		channel    int
		model, ttl string
	}
	cache := map[cacheKey]consumptionCacheGroup{}
	var touch []int
	for _, hour := range hours {
		facts, err := s.readConsumptionHour(ctx, query, hour)
		if err != nil {
			return nil, err
		}
		seen := map[int]bool{}
		for _, row := range facts.rows {
			if row.ChannelTotal == nil || seen[row.EntityID] {
				continue
			}
			seen[row.EntityID] = true
			if parents[row.EntityID] == nil {
				parents[row.EntityID] = &consumptionBillingTotals{}
			}
			p := row.ChannelTotal
			parents[row.EntityID].add(consumptionGroup{PricedRequests: p.PricedRequests, PricedCost: p.PricedCost,
				TokenPricedRequests: p.TokenPricedRequests, TokenPricedCost: p.TokenPricedCost, PricedTokens: p.PricedTokens,
				UnpricedRequests: p.UnpricedRequests, CacheRequests: p.CacheRequests})
		}
		result.rows = mergeConsumptionGroups(result.rows, facts.rows)
		result.attempts = mergeConsumptionGroups(result.attempts, facts.attempts)
		for _, group := range facts.cache {
			key := cacheKey{group.ChannelID, group.Model, group.TTL}
			value := cache[key]
			value.ChannelID, value.Model, value.TTL = group.ChannelID, group.Model, group.TTL
			value.add(group)
			cache[key] = value
		}
		if hour.stored != nil && time.Since(hour.stored.RequestedAt) > 24*time.Hour {
			touch = append(touch, hour.stored.ID)
		}
	}
	for i := range result.rows {
		result.rows[i].ChannelTotal = parents[result.rows[i].EntityID]
	}
	for _, group := range cache {
		result.cache = append(result.cache, group)
	}
	if len(touch) > 0 {
		if err := s.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDIn(touch...)).SetRequestedAt(time.Now().UTC()).Exec(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}
