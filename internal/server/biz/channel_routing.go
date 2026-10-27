package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
)

type ChannelTagPriorityUpdate struct {
	ChannelID      objects.GUID `json:"channelId"`
	Priority       *int         `json:"priority"`
	OrderingWeight *int         `json:"orderingWeight"`
}

// UpdateChannelTagPriorities saves priorities and shared channel weights in one
// transaction. A nil priority restores model-association inheritance for this tag.
func (svc *ChannelService) UpdateChannelTagPriorities(ctx context.Context, tag string, updates []*ChannelTagPriorityUpdate) ([]*ent.Channel, error) {
	if strings.TrimSpace(tag) == "" {
		return nil, fmt.Errorf("channel tag is required")
	}
	seen := make(map[int]bool, len(updates))
	for _, update := range updates {
		if seen[update.ChannelID.ID] {
			return nil, fmt.Errorf("duplicate channel: %d", update.ChannelID.ID)
		}
		seen[update.ChannelID.ID] = true
	}
	result := make([]*ent.Channel, 0, len(updates))
	err := svc.RunInTransaction(ctx, func(ctx context.Context) error {
		db := svc.entFromContext(ctx)
		for _, update := range updates {
			current, err := authz.RunWithScopeDecision(ctx, scopes.ScopeWriteChannels, func(queryCtx context.Context) (*ent.Channel, error) {
				return db.Channel.Get(queryCtx, update.ChannelID.ID)
			})
			if err != nil {
				return fmt.Errorf("failed to load channel: %w", err)
			}
			if !slices.Contains(current.Tags, tag) {
				return fmt.Errorf("channel %d does not have tag %q", current.ID, tag)
			}
			settings := objects.ChannelSettings{}
			if current.Settings != nil {
				settings = *current.Settings
			}
			settings.TagPriorities = slices.DeleteFunc(slices.Clone(settings.TagPriorities), func(entry objects.ChannelTagPriority) bool {
				return entry.Tag == tag || !slices.Contains(current.Tags, entry.Tag)
			})
			if update.Priority != nil {
				settings.TagPriorities = append(settings.TagPriorities, objects.ChannelTagPriority{Tag: tag, Priority: *update.Priority})
			}
			updated, err := db.Channel.UpdateOneID(current.ID).
				Where(channel.UpdatedAtEQ(current.UpdatedAt)).
				SetSettings(&settings).
				SetNillableOrderingWeight(update.OrderingWeight).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("failed to save channel routing settings: %w", err)
			}
			result = append(result, updated)
		}
		svc.reloadChannelsAfterCommit(ctx)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
