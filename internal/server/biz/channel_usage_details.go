package biz

import (
	"context"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
)

// Model details are read only on demand, with the same visibility and natural
// day as the lightweight channel summary. No provider calls are involved.
func (svc *ChannelService) TodayUsageDetails(ctx context.Context, id int) (*objects.ChannelTodayUsageDetails, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeReadChannels); err != nil {
		return nil, err
	}
	visible, err := authz.RunWithScopeDecision(ctx, scopes.ScopeReadChannels, func(ctx context.Context) (bool, error) {
		return svc.entFromContext(ctx).Channel.Query().Where(channel.IDEQ(id)).Exist(ctx)
	})
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, fmt.Errorf("channel not found")
	}
	return authz.RunWithSystemBypass(ctx, "channel-today-usage-details", func(ctx context.Context) (*objects.ChannelTodayUsageDetails, error) {
		settings, err := svc.SystemService.GeneralSettings(ctx)
		if err != nil {
			return nil, err
		}
		location := svc.SystemService.TimeLocation(ctx)
		now := time.Now().In(location)
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		models, err := svc.cachedTodayUsageModels(ctx, id, start, start.AddDate(0, 0, 1))
		if err != nil {
			return nil, err
		}
		return &objects.ChannelTodayUsageDetails{
			ChannelID: objects.GUID{ID: id, Type: ent.TypeChannel}, Timezone: location.String(), CurrencyCode: settings.CurrencyCode, Models: models,
		}, nil
	})
}
