package biz

import (
	"context"
	"slices"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

type channelUsageFacts struct {
	models   []channelModelTotals
	attempts []consumptionGroup
}

func readChannelUsageFacts(ctx context.Context, client *ent.Client, ids []int, start, end time.Time) (channelUsageFacts, error) {
	var facts channelUsageFacts
	if err := prepareChannelStandardCosts(ctx, client, ids, start, end); err != nil {
		return facts, err
	}
	var err error
	facts.models, err = readChannelModelTotals(ctx, client, ids, start, end)
	if err != nil {
		return facts, err
	}
	facts.attempts, err = readChannelAttemptGroups(ctx, client, 0, ConsumptionFilter{DailySnapshot: true}, ids, &start, end, true)
	return facts, err
}

type channelHourKey struct {
	id         int
	start, end int64
}

type channelHourValue struct {
	version string
	channelUsageFacts
}

type channelUsageHourCache struct {
	once    sync.Once
	entries *lru.Cache[channelHourKey, channelHourValue]
}

func (svc *ChannelService) channelUsageWithModels(ctx context.Context, client *ent.Client, ids []int, start, end time.Time) ([]*objects.ChannelTodayUsage, error) {
	// A caller-owned transaction may still roll back. Keep its reads and
	// derived prices local to that transaction.
	_, callerTransaction := client.Driver().(dialect.Tx)
	if client != svc.db || callerTransaction {
		return readStatisticsSnapshot(ctx, client, false, func(tx *ent.Client) ([]*objects.ChannelTodayUsage, error) {
			facts, err := readChannelUsageFacts(ctx, tx, ids, start, end)
			if err != nil {
				return nil, err
			}
			return renderChannelUsage(ids, start.Location().String(), facts), nil
		})
	}
	cache := &svc.usageHours
	cache.once.Do(func() {
		// Covers a full day for a 100-channel page, including DST boundary hours.
		cache.entries, _ = lru.New[channelHourKey, channelHourValue](4096)
	})
	var updated map[channelHourKey]*channelHourValue
	result, err := readStatisticsSnapshot(ctx, client, false, func(tx *ent.Client) ([]*objects.ChannelTodayUsage, error) {
		revisions, err := readStatisticsRevisions(ctx, tx, 0, start, end)
		if err != nil {
			return nil, err
		}
		updated = make(map[channelHourKey]*channelHourValue)
		var all channelUsageFacts
		type missingRange struct {
			start, end time.Time
			ids        []int
		}
		var ranges []missingRange
		for from := start.UTC(); from.Before(end); {
			until := from.Truncate(time.Hour).Add(time.Hour)
			if until.After(end) {
				until = end.UTC()
			}
			version := statisticsFingerprint(revisions, from, until, consumptionSnapshotVersion)
			var missing []int
			for _, id := range ids {
				key := channelHourKey{id, from.Unix(), until.Unix()}
				cached, ok := cache.entries.Get(key)
				if ok && cached.version == version {
					all.models = append(all.models, cached.models...)
					all.attempts = append(all.attempts, cached.attempts...)
				} else {
					missing = append(missing, id)
					updated[key] = &channelHourValue{version: version}
				}
			}
			if len(missing) > 0 {
				if n := len(ranges); n > 0 && ranges[n-1].end.Equal(from) && slices.Equal(ranges[n-1].ids, missing) {
					ranges[n-1].end = until
				} else {
					ranges = append(ranges, missingRange{from, until, missing})
				}
			}
			from = until
		}
		// On a cold day read adjacent missing hours in one set of queries.
		keyFor := func(id int, hour int64) channelHourKey {
			return channelHourKey{id, max(start.Unix(), hour*3600), min(end.Unix(), (hour+1)*3600)}
		}
		// The bounded aggregates run faster than PostgreSQL can JIT-compile
		// them on a busy day. This is our own transaction; SET LOCAL expires
		// at commit/rollback and is never issued on an unchanged refresh.
		if len(ranges) > 0 && tx.Driver().Dialect() == dialect.Postgres {
			if err := tx.Driver().Exec(ctx, "SET LOCAL jit = off", []any{}, nil); err != nil {
				return nil, err
			}
		}
		for _, r := range ranges {
			facts, err := readChannelUsageFacts(ctx, tx, r.ids, r.start, r.end)
			if err != nil {
				return nil, err
			}
			for _, v := range facts.models {
				h := updated[keyFor(v.ChannelID, v.HourBucket)]
				h.models = append(h.models, v)
			}
			for _, v := range facts.attempts {
				h := updated[keyFor(v.EntityID, v.HourBucket)]
				h.attempts = append(h.attempts, v)
			}
			all.models = append(all.models, facts.models...)
			all.attempts = append(all.attempts, facts.attempts...)
		}
		return renderChannelUsage(ids, start.Location().String(), all), nil
	})
	if err != nil {
		return nil, err
	}
	for key, v := range updated {
		cache.entries.Add(key, *v)
	}
	return result, nil
}

func renderChannelUsage(ids []int, timezone string, facts channelUsageFacts) []*objects.ChannelTodayUsage {
	type key struct {
		id    int
		model string
	}
	byModel := make(map[key]*channelModelTotals)
	totals := make(map[int]*objects.ChannelTodayUsage, len(ids))
	result := make([]*objects.ChannelTodayUsage, 0, len(ids))
	for _, id := range ids {
		value := &objects.ChannelTodayUsage{ChannelID: objects.GUID{Type: ent.TypeChannel, ID: id}, Timezone: timezone, Models: []*objects.ChannelTodayModelUsage{}}
		totals[id] = value
		result = append(result, value)
	}
	for _, row := range facts.models {
		total := totals[row.ChannelID]
		total.RequestCount += row.Requests
		total.TotalCost += row.TotalCost
		total.PricedCount += row.ParentPriced
		total.UnpricedCount += row.ParentUnpriced
		k := key{row.ChannelID, row.Model}
		m := byModel[k]
		if m == nil {
			m = &channelModelTotals{ChannelID: row.ChannelID, Model: row.Model}
			byModel[k] = m
		}
		m.Requests += row.Requests
		m.Priced += row.Priced
		m.Unpriced += row.Unpriced
		m.Tokens += row.Tokens
		m.TokenPricedRequests += row.TokenPricedRequests
		m.PricedTokens += row.PricedTokens
		m.PricedCost += row.PricedCost
		m.TotalCost += row.TotalCost
		m.PromptTokens += row.PromptTokens
		m.CacheRead += row.CacheRead
		m.StandardUnpriced += row.StandardUnpriced
		m.StandardKnown += row.StandardKnown
		m.StandardCost += row.StandardCost
	}
	rows := make([]channelModelTotals, 0, len(byModel))
	for _, m := range byModel {
		rows = append(rows, *m)
	}
	for i := range facts.attempts {
		facts.attempts[i].HourBucket = 0
	}
	models := channelUsageModels(rows, mergeConsumptionGroups(nil, facts.attempts))
	for id, values := range models {
		totals[id].Models = values
	}
	return result
}
