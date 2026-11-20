package gql

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/99designs/gqlgen/graphql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
)

type requestSpeedCacheKey struct{}

type requestSpeedCache struct {
	sync.Mutex

	values map[int]*biz.RequestOutputRate
}

func withRequestSpeedCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestSpeedCacheKey{}, &requestSpeedCache{values: make(map[int]*biz.RequestOutputRate)})
}

func cacheRequestSpeeds(ctx context.Context, client *ent.Client, page *ent.RequestConnection, err error) (*ent.RequestConnection, error) {
	if err == nil {
		err = cacheRequestUsageSummaries(ctx, client, page)
	}
	cache, ok := ctx.Value(requestSpeedCacheKey{}).(*requestSpeedCache)
	if err != nil || !ok || page == nil || len(page.Edges) == 0 || !slices.Contains(graphql.CollectAllFields(ctx), "edges") {
		return page, err
	}
	ids := make([]int, 0, len(page.Edges))
	for _, edge := range page.Edges {
		ids = append(ids, edge.Node.ID)
	}
	values, err := biz.RequestOutputRates(ctx, client, ids)
	if err != nil {
		return nil, err
	}
	cache.Lock()
	defer cache.Unlock()
	maps.Copy(cache.values, values)
	return page, nil
}

func requestOutputSpeed(ctx context.Context, client *ent.Client, id int) (*float64, error) {
	rate, err := requestOutputRate(ctx, client, id)
	if rate == nil {
		return nil, err
	}
	return &rate.TokensPerSecond, err
}

func requestOutputRate(ctx context.Context, client *ent.Client, id int) (*biz.RequestOutputRate, error) {
	if cache, ok := ctx.Value(requestSpeedCacheKey{}).(*requestSpeedCache); ok {
		cache.Lock()
		value, found := cache.values[id]
		cache.Unlock()
		if found {
			return value, nil
		}
	}
	values, err := biz.RequestOutputRates(ctx, client, []int{id})
	return values[id], err
}
