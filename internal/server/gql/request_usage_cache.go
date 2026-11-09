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

type requestUsageCacheKey struct{}
type requestUsageCache struct {
	sync.Mutex
	values map[int]*biz.RequestUsageSummary
}

func withRequestUsageCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestUsageCacheKey{}, &requestUsageCache{values: make(map[int]*biz.RequestUsageSummary)})
}

func cacheRequestUsageSummaries(ctx context.Context, client *ent.Client, page *ent.RequestConnection) error {
	cache, ok := ctx.Value(requestUsageCacheKey{}).(*requestUsageCache)
	if !ok || page == nil || len(page.Edges) == 0 {
		return nil
	}
	op := graphql.GetOperationContext(ctx)
	fields := requestLogFields(op, requestLogFields(op, graphql.CollectFieldsCtx(ctx, nil), "edges"), "node")
	if !slices.ContainsFunc(fields, func(field graphql.CollectedField) bool { return field.Name == "usageSummary" }) {
		return nil
	}
	ids := make([]int, 0, len(page.Edges))
	for _, edge := range page.Edges {
		ids = append(ids, edge.Node.ID)
	}
	values, err := biz.RequestUsageSummaries(ctx, client, ids)
	if err != nil {
		return err
	}
	cache.Lock()
	defer cache.Unlock()
	maps.Copy(cache.values, values)
	return nil
}

func requestUsageSummary(ctx context.Context, client *ent.Client, id int) (*biz.RequestUsageSummary, error) {
	if cache, ok := ctx.Value(requestUsageCacheKey{}).(*requestUsageCache); ok {
		cache.Lock()
		value, found := cache.values[id]
		cache.Unlock()
		if found {
			return value, nil
		}
	}
	values, err := biz.RequestUsageSummaries(ctx, client, []int{id})
	return values[id], err
}
