package biz

import (
	"context"
	"fmt"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/looplj/axonhub/internal/objects"
	"golang.org/x/sync/singleflight"
)

const channelTodayUsageCacheTTL = 10 * time.Second

type channelTodayUsageCacheEntry struct {
	models  []*objects.ChannelTodayModelUsage
	expires time.Time
}

// Process-local, bounded and demand-driven; no Redis format or cleanup worker.
// Authorization and settings are resolved before entering this cache.
type channelTodayUsageCache struct {
	once    sync.Once
	entries *lru.Cache[string, channelTodayUsageCacheEntry]
	flight  singleflight.Group
}

func (svc *ChannelService) cachedTodayUsageModels(ctx context.Context, id int, start, end time.Time) ([]*objects.ChannelTodayModelUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client := svc.entFromContext(ctx)
	// Transaction-local reads must never consume or publish shared cached data.
	if client != svc.db {
		return queryChannelTodayUsageModels(ctx, client, id, start, end)
	}
	cache := &svc.todayUsageCache
	cache.once.Do(func() { cache.entries, _ = lru.New[string, channelTodayUsageCacheEntry](64) })
	key := fmt.Sprintf("%d:%d:%d", id, start.Unix(), end.Unix())
	fresh := func() ([]*objects.ChannelTodayModelUsage, bool) {
		entry, ok := cache.entries.Get(key)
		return entry.models, ok && time.Now().Before(entry.expires)
	}
	if models, ok := fresh(); ok {
		return models, nil
	}
	result := cache.flight.DoChan(key, func() (any, error) {
		if models, ok := fresh(); ok {
			return models, nil
		}
		// A closing/StrictMode-remounting hover cancels its own wait, not the
		// shared refresh serving other callers. Keep the query bounded.
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		models, err := queryChannelTodayUsageModels(refreshCtx, client, id, start, end)
		if err != nil {
			return nil, err
		}
		cache.entries.Add(key, channelTodayUsageCacheEntry{models: models, expires: time.Now().Add(channelTodayUsageCacheTTL)})
		return models, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-result:
		if value.Err != nil {
			return nil, value.Err
		}
		return value.Val.([]*objects.ChannelTodayModelUsage), nil
	}
}
