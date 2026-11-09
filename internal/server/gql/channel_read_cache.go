package gql

import (
	"context"
	"errors"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/privacy"
)

type channelReadCacheKey struct{}

// 渠道名称与密钥序号的权限检查共用本次读取结果，不跨请求保留权限结果。
type channelReadCache struct {
	sync.Mutex
	values map[int]*ent.Channel
}

func withChannelReadCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, channelReadCacheKey{}, &channelReadCache{values: make(map[int]*ent.Channel)})
}

func cacheRequestChannels(ctx context.Context, client *ent.Client, records []*ent.Request) error {
	cache, ok := ctx.Value(channelReadCacheKey{}).(*channelReadCache)
	if !ok || len(records) == 0 {
		return nil
	}
	op := graphql.GetOperationContext(ctx)
	fields := requestLogFields(op, requestLogFields(op, graphql.CollectFieldsCtx(ctx, nil), "edges"), "node")
	ids := make(map[int]struct{})
	for _, field := range fields {
		switch field.Name {
		case "channel":
			for _, record := range records {
				ids[record.ChannelID] = struct{}{}
			}
		case "executions":
			attemptFields := requestLogFields(op, requestLogFields(op, graphql.CollectFields(op, field.Selections, nil), "edges"), "node")
			for _, child := range attemptFields {
				if child.Name != "channel" && child.Name != "channelAPIKeyIndex" && child.Name != "channelAPIKeySuffix" {
					continue
				}
				for _, record := range records {
					attempts, _ := record.NamedExecutions(field.Alias)
					for _, attempt := range attempts {
						ids[attempt.ChannelID] = struct{}{}
					}
				}
			}
		}
	}
	delete(ids, 0)
	if len(ids) == 0 {
		return nil
	}
	cache.Lock()
	defer cache.Unlock()
	missing := make([]int, 0, len(ids))
	for id := range ids {
		if _, found := cache.values[id]; !found {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	channels, err := client.Channel.Query().Where(channel.IDIn(missing...)).All(ctx)
	if err != nil && !errors.Is(err, privacy.Deny) {
		return err
	}
	for _, id := range missing {
		cache.values[id] = nil
	}
	for _, ch := range channels {
		cache.values[ch.ID] = ch
	}
	return nil
}
