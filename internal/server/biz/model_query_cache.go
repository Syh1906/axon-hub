package biz

import (
	"context"
	"sync"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
)

type modelAssociationCacheKey struct{}

type modelAssociationQueryCache struct {
	once     sync.Once
	channels []*ent.Channel
	err      error
}

// WithModelAssociationQueryCache shares channel catalog reads inside one read-only
// operation. It does not retain data across requests, users or mutations.
func WithModelAssociationQueryCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, modelAssociationCacheKey{}, &modelAssociationQueryCache{})
}

func (svc *ModelService) queryAssociationChannels(ctx context.Context) ([]*ent.Channel, error) {
	query := func() ([]*ent.Channel, error) {
		return svc.entFromContext(ctx).Channel.Query().Where(channel.StatusIn(channel.StatusEnabled, channel.StatusDisabled)).
			Select(channel.FieldID, channel.FieldName, channel.FieldType, channel.FieldStatus, channel.FieldTags,
				channel.FieldSupportedModels, channel.FieldSettings).All(ctx)
	}
	cache, ok := ctx.Value(modelAssociationCacheKey{}).(*modelAssociationQueryCache)
	if !ok {
		return query()
	}
	cache.once.Do(func() { cache.channels, cache.err = query() })
	return cache.channels, cache.err
}
