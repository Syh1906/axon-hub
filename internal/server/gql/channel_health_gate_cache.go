package gql

import (
	"context"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/server/biz"
)

type channelHealthGateCacheKey struct{}
type channelHealthGateCache struct {
	sync.RWMutex
	values map[int]*ChannelHealthGateStatus
}

func withChannelHealthGateCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, channelHealthGateCacheKey{}, &channelHealthGateCache{values: make(map[int]*ChannelHealthGateStatus)})
}

func (r *queryResolver) cacheChannelHealthGates(ctx context.Context, page *ent.ChannelConnection) error {
	cache, ok := ctx.Value(channelHealthGateCacheKey{}).(*channelHealthGateCache)
	if !ok || page == nil || len(page.Edges) == 0 || !canReadChannelHealthGate(ctx) {
		return nil
	}
	op := graphql.GetOperationContext(ctx)
	fields := requestLogFields(op, requestLogFields(op, graphql.CollectFieldsCtx(ctx, nil), "edges"), "node")
	selected := false
	for _, field := range fields {
		selected = selected || field.Name == "healthGate"
	}
	if !selected {
		return nil
	}
	ids := make([]int, 0, len(page.Edges))
	for _, edge := range page.Edges {
		ids = append(ids, edge.Node.ID)
	}
	snapshots, configs, err := r.channelService.HealthGate().SnapshotChannels(ctx, ids, func() (map[int]biz.HealthGateConfig, error) {
		channels, err := r.client.Channel.Query().Where(channel.IDIn(ids...)).All(authz.WithSystemBypass(ctx, "health-gate-channel"))
		if err != nil {
			return nil, err
		}
		policy := r.healthGatePolicy(ctx)
		configs := make(map[int]biz.HealthGateConfig, len(channels))
		for _, ch := range channels {
			configs[ch.ID] = biz.ResolveHealthGateConfig(policy, &biz.Channel{Channel: ch})
		}
		return configs, nil
	})
	if err != nil {
		return err
	}
	cache.Lock()
	defer cache.Unlock()
	for _, edge := range page.Edges {
		cfg, exists := configs[edge.Node.ID]
		if !exists {
			cfg = biz.ResolveHealthGateConfig(r.healthGatePolicy(ctx), &biz.Channel{Channel: edge.Node})
		}
		cache.values[edge.Node.ID] = healthGateStatusFromSnapshots(cfg, snapshots[edge.Node.ID])
	}
	return nil
}
