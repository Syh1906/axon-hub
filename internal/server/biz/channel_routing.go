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

type ChannelPrioritiesInput struct {
	GlobalPriority *int                         `json:"globalPriority"`
	TagPriorities  []objects.ChannelTagPriority `json:"tagPriorities"`
}

func validateChannelPriority(priority *int) error {
	if priority != nil && (*priority < -2147483648 || *priority > 2147483647) {
		return fmt.Errorf("channel priority must be an integer between -2147483648 and 2147483647")
	}
	return nil
}

// UpdateChannelPriorities replaces one channel's global and tag priorities atomically.
func (svc *ChannelService) UpdateChannelPriorities(ctx context.Context, channelID int, input ChannelPrioritiesInput) (*ent.Channel, error) {
	if err := validateChannelPriority(input.GlobalPriority); err != nil {
		return nil, err
	}
	var result *ent.Channel
	err := svc.RunInTransaction(ctx, func(ctx context.Context) error {
		db := svc.entFromContext(ctx)
		current, err := authz.RunWithScopeDecision(ctx, scopes.ScopeWriteChannels, func(queryCtx context.Context) (*ent.Channel, error) {
			return db.Channel.Get(queryCtx, channelID)
		})
		if err != nil {
			return fmt.Errorf("failed to load channel: %w", err)
		}
		seen := make(map[string]bool, len(input.TagPriorities))
		for _, entry := range input.TagPriorities {
			if err := validateChannelPriority(&entry.Priority); err != nil {
				return err
			}
			if !slices.Contains(current.Tags, entry.Tag) {
				return fmt.Errorf("channel %d does not have tag %q", channelID, entry.Tag)
			}
			if seen[entry.Tag] {
				return fmt.Errorf("duplicate channel tag: %q", entry.Tag)
			}
			seen[entry.Tag] = true
		}
		settings := objects.ChannelSettings{}
		if current.Settings != nil {
			settings = *current.Settings
		}
		settings.GlobalPriority = input.GlobalPriority
		settings.TagPriorities = slices.Clone(input.TagPriorities)
		result, err = db.Channel.UpdateOneID(channelID).
			Where(channel.UpdatedAtEQ(current.UpdatedAt)).
			SetSettings(&settings).Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to save channel priorities: %w", err)
		}
		svc.reloadChannelsAfterCommit(ctx)
		return nil
	})
	return result, err
}

// UpdateChannelTagPriorities saves priorities and shared channel weights in one
// transaction. A nil priority restores global/model-association inheritance for this tag.
func (svc *ChannelService) UpdateChannelTagPriorities(ctx context.Context, tag string, updates []*ChannelTagPriorityUpdate) ([]*ent.Channel, error) {
	if strings.TrimSpace(tag) == "" {
		return nil, fmt.Errorf("channel tag is required")
	}
	seen := make(map[int]bool, len(updates))
	for _, update := range updates {
		if err := validateChannelPriority(update.Priority); err != nil {
			return nil, err
		}
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
