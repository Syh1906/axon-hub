package gql

import (
	"context"
	"github.com/99designs/gqlgen/graphql"
	"slices"
	"sync"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
)

type requestSpeedCacheKey struct{}
type requestSpeedCache struct {
	sync.Mutex
	values map[int]*float64
}

func withRequestSpeedCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestSpeedCacheKey{}, &requestSpeedCache{values: make(map[int]*float64)})
}

func cacheRequestSpeeds(ctx context.Context, client *ent.Client, page *ent.RequestConnection, err error) (*ent.RequestConnection, error) {
	cache, ok := ctx.Value(requestSpeedCacheKey{}).(*requestSpeedCache)
	if err != nil || !ok || page == nil || len(page.Edges) == 0 || !slices.Contains(graphql.CollectAllFields(ctx), "edges") {
		return page, err
	}
	ids := make([]int, 0, len(page.Edges))
	for _, edge := range page.Edges {
		ids = append(ids, edge.Node.ID)
	}
	values, err := biz.RequestOutputSpeeds(ctx, client, ids)
	if err != nil {
		return nil, err
	}
	cache.Lock()
	defer cache.Unlock()
	for id, value := range values {
		cache.values[id] = value
	}
	return page, nil
}

func requestOutputSpeed(ctx context.Context, client *ent.Client, id int) (*float64, error) {
	if cache, ok := ctx.Value(requestSpeedCacheKey{}).(*requestSpeedCache); ok {
		cache.Lock()
		value, found := cache.values[id]
		cache.Unlock()
		if found {
			return value, nil
		}
	}
	values, err := biz.RequestOutputSpeeds(ctx, client, []int{id})
	return values[id], err
}
