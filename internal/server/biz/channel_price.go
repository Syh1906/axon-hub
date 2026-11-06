package biz

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelprice"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xtime"
	"github.com/looplj/axonhub/internal/scopes"
)

type SaveChannelModelPriceInput struct {
	ModelID string             `json:"modelId"`
	Price   objects.ModelPrice `json:"price"`
}

type ActionType string

const (
	ActionTypeCreate ActionType = "create"
	ActionTypeUpdate ActionType = "update"
	ActionTypeDelete ActionType = "delete"
	ActionTypeSkip   ActionType = "skip"
)

type PriceChangeAction struct {
	Type          ActionType
	ModelID       string
	Price         objects.ModelPrice
	ExistingPrice *ent.ChannelModelPrice // nil if create
}

const channelModelPriceQueryBatchSize = 500

func uniqueNonEmptyModelIDs(modelIDs []string) []string {
	return lo.Uniq(lo.Filter(modelIDs, func(modelID string, _ int) bool {
		return modelID != ""
	}))
}

func modelCardToChannelModelPrice(card *objects.ModelCard) (objects.ModelPrice, bool) {
	if card == nil || card.Price == nil {
		return objects.ModelPrice{}, false
	}
	if len(card.Price.Items) == 0 && len(card.Price.VolumeTiers) == 0 &&
		(card.Price.Schedule == nil || len(card.Price.Schedule.Overrides) == 0) {
		return objects.ModelPrice{}, false
	}

	return *card.Price, true
}

func (svc *ChannelService) createChannelModelPriceVersion(
	ctx context.Context,
	entity *ent.ChannelModelPrice,
	now time.Time,
) error {
	_, err := svc.entFromContext(ctx).ChannelModelPriceVersion.Create().
		SetChannelID(entity.ChannelID).
		SetModelID(entity.ModelID).
		SetChannelModelPriceID(entity.ID).
		SetPrice(entity.Price).
		SetStatus(channelmodelpriceversion.StatusActive).
		SetEffectiveStartAt(now).
		SetReferenceID(entity.ReferenceID).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create channel model price version: %w", err)
	}

	return nil
}

// createChannelModelPrice creates the current price and its initial active
// version. The caller must run it inside the logical channel mutation
// transaction so the two records cannot be committed independently.
func (svc *ChannelService) createChannelModelPrice(
	ctx context.Context,
	channelID int,
	modelID string,
	price objects.ModelPrice,
	now time.Time,
) (*ent.ChannelModelPrice, error) {
	entity, err := svc.entFromContext(ctx).ChannelModelPrice.Create().
		SetChannelID(channelID).
		SetModelID(modelID).
		SetPrice(price).
		SetReferenceID(generateReferenceID()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create channel model price: %w", err)
	}

	if err := svc.createChannelModelPriceVersion(ctx, entity, now); err != nil {
		return nil, err
	}

	return entity, nil
}

func calculatePriceChanges(prices []*ent.ChannelModelPrice, inputs []SaveChannelModelPriceInput) []PriceChangeAction {
	existingMap := lo.KeyBy(prices, func(p *ent.ChannelModelPrice) string {
		return p.ModelID
	})

	inputSet := make(map[string]struct{}, len(inputs))

	var actions []PriceChangeAction

	// 1. Identify updates and creates
	// Iterate over inputs in order to keep deterministic action ordering.
	for _, input := range inputs {
		modelID := input.ModelID
		inputSet[modelID] = struct{}{}

		existing, ok := existingMap[modelID]
		if !ok {
			actions = append(actions, PriceChangeAction{
				Type:          ActionTypeCreate,
				ModelID:       modelID,
				Price:         input.Price,
				ExistingPrice: nil,
			})
		} else {
			// Only update if price changed
			if existing.Price.Equals(input.Price) {
				actions = append(actions, PriceChangeAction{
					Type:          ActionTypeSkip,
					ModelID:       modelID,
					Price:         input.Price,
					ExistingPrice: existing,
				})
			} else {
				actions = append(actions, PriceChangeAction{
					Type:          ActionTypeUpdate,
					ModelID:       modelID,
					Price:         input.Price,
					ExistingPrice: existing,
				})
			}
		}
	}

	// 2. Identify deletes: present in existing but not in inputs
	for _, existing := range prices {
		if _, ok := inputSet[existing.ModelID]; !ok {
			actions = append(actions, PriceChangeAction{
				Type:          ActionTypeDelete,
				ModelID:       existing.ModelID,
				ExistingPrice: existing,
			})
		}
	}

	return actions
}

type ChannelPricingSettings struct {
	ServiceTierBilling *objects.ServiceTierSettings
	CacheWriteBilling  *objects.CacheWriteBilling
}

func (svc *ChannelService) SaveChannelModelPrices(
	ctx context.Context,
	channelID int,
	inputs []SaveChannelModelPriceInput,
	multiplier *decimal.Decimal,
	pricingSettings ...ChannelPricingSettings,
) ([]*ent.ChannelModelPrice, error) {
	return svc.saveChannelModelPrices(ctx, channelID, inputs, multiplier, true, pricingSettings...)
}

// UpdateChannelCostMultiplier versions every existing price without replacing
// the price list from a potentially stale browser snapshot.
func (svc *ChannelService) UpdateChannelCostMultiplier(ctx context.Context, channelID int, multiplier decimal.Decimal) ([]*ent.ChannelModelPrice, error) {
	var prices []*ent.ChannelModelPrice
	err := svc.RunInTransaction(ctx, func(ctx context.Context) error {
		var err error
		prices, err = svc.saveChannelModelPrices(ctx, channelID, nil, &multiplier, false)
		if err != nil {
			return err
		}
		ch, err := svc.upstreamChannel(ctx, channelID, scopes.ScopeWriteChannels)
		if err != nil {
			return err
		}
		if ch.Settings != nil && ch.Settings.Upstream != nil && ch.Settings.Upstream.CostMode != objects.ChannelCostModeManual {
			settings := *ch.Settings
			upstreamSettings := *settings.Upstream
			upstreamSettings.CostMode = objects.ChannelCostModeManual
			settings.Upstream = &upstreamSettings
			return svc.entFromContext(ctx).Channel.UpdateOneID(channelID).SetSettings(&settings).Exec(ctx)
		}
		return nil
	})
	return prices, err
}

func (svc *ChannelService) saveChannelModelPrices(
	ctx context.Context,
	channelID int,
	inputs []SaveChannelModelPriceInput,
	multiplier *decimal.Decimal,
	replacePrices bool,
	pricingSettings ...ChannelPricingSettings,
) ([]*ent.ChannelModelPrice, error) {
	var pricing ChannelPricingSettings
	if len(pricingSettings) > 0 {
		pricing = pricingSettings[0]
	}
	if err := pricing.ServiceTierBilling.Validate(); err != nil {
		return nil, err
	}
	if pricing.CacheWriteBilling != nil {
		if err := pricing.CacheWriteBilling.Validate(); err != nil {
			return nil, err
		}
	}
	if multiplier != nil && multiplier.IsNegative() {
		return nil, fmt.Errorf("model price multiplier must be non-negative")
	}

	seenModelIDs := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if _, ok := seenModelIDs[input.ModelID]; ok {
			return nil, fmt.Errorf("duplicate model price input: model_id=%s", input.ModelID)
		}

		seenModelIDs[input.ModelID] = struct{}{}

		if err := input.Price.Validate(); err != nil {
			return nil, fmt.Errorf("invalid model price: model_id=%s: %w", input.ModelID, err)
		}
	}

	var (
		results []*ent.ChannelModelPrice
		now     = xtime.UTCNow()
	)

	err := svc.RunInTransaction(ctx, func(ctx context.Context) error {
		db := svc.entFromContext(ctx)
		ch, err := authz.RunWithScopeDecision(ctx, scopes.ScopeWriteChannels, func(queryCtx context.Context) (*ent.Channel, error) {
			return db.Channel.Get(queryCtx, channelID)
		})
		if err != nil {
			return fmt.Errorf("failed to load channel pricing settings: %w", err)
		}
		settings := objects.ChannelSettings{}
		if ch.Settings != nil {
			settings = *ch.Settings
		}
		if pricing.ServiceTierBilling != nil {
			copy := *pricing.ServiceTierBilling
			settings.ServiceTierBilling = &copy
		}
		if pricing.CacheWriteBilling != nil {
			settings.CacheWriteBilling = lo.ToPtr(*pricing.CacheWriteBilling)
		}
		if multiplier != nil {
			settings.ModelPriceMultiplier = lo.ToPtr(*multiplier)
		}

		// Lock the channel before reading prices, and commit settings and every
		// price version together, even when the submitted price list is empty.
		if err := db.Channel.UpdateOneID(channelID).
			Where(channel.UpdatedAtEQ(ch.UpdatedAt)).
			SetSettings(&settings).
			SetUpdatedAt(now).
			Exec(ctx); err != nil {
			return fmt.Errorf("failed to save channel pricing settings: %w", err)
		}

		prices, err := db.ChannelModelPrice.Query().
			Where(channelmodelprice.ChannelID(channelID)).
			All(ctx)
		if err != nil {
			return fmt.Errorf("failed to query existing channel model prices: %w", err)
		}
		versionedInputs := slices.Clone(inputs)
		if !replacePrices {
			versionedInputs = lo.Map(prices, func(price *ent.ChannelModelPrice, _ int) SaveChannelModelPriceInput {
				return SaveChannelModelPriceInput{ModelID: price.ModelID, Price: price.Price}
			})
		}
		for idx := range versionedInputs {
			versionedInputs[idx].Price.Multiplier = settings.ModelPriceMultiplier
		}
		actions := calculatePriceChanges(prices, versionedInputs)

		for _, action := range actions {
			var (
				entity *ent.ChannelModelPrice
				refID  string
				err    error
			)

			switch action.Type {
			case ActionTypeSkip:
				results = append(results, action.ExistingPrice)
				continue

			case ActionTypeDelete:
				// Archive old versions
				_, err = db.ChannelModelPriceVersion.Update().
					Where(
						channelmodelpriceversion.ChannelModelPriceIDEQ(action.ExistingPrice.ID),
						channelmodelpriceversion.StatusEQ(channelmodelpriceversion.StatusActive),
					).
					SetStatus(channelmodelpriceversion.StatusArchived).
					SetEffectiveEndAt(now).
					Save(ctx)
				if err != nil {
					return fmt.Errorf("failed to archive channel model price versions for delete: %w", err)
				}

				err = db.ChannelModelPrice.DeleteOne(action.ExistingPrice).Exec(ctx)
				if err != nil {
					return fmt.Errorf("failed to delete channel model price: %w", err)
				}

				continue

			case ActionTypeCreate:
				entity, err = svc.createChannelModelPrice(ctx, channelID, action.ModelID, action.Price, now)
				if err != nil {
					return err
				}

				results = append(results, entity)
				continue

			case ActionTypeUpdate:
				entity = action.ExistingPrice
				// Archive old versions
				_, err = db.ChannelModelPriceVersion.Update().
					Where(
						channelmodelpriceversion.ChannelModelPriceIDEQ(entity.ID),
						channelmodelpriceversion.StatusEQ(channelmodelpriceversion.StatusActive),
					).
					SetStatus(channelmodelpriceversion.StatusArchived).
					SetEffectiveEndAt(now).
					Save(ctx)
				if err != nil {
					return fmt.Errorf("failed to archive old channel model price versions: %w", err)
				}

				refID = generateReferenceID()

				entity, err = db.ChannelModelPrice.UpdateOneID(entity.ID).
					SetPrice(action.Price).
					SetReferenceID(refID).
					Save(ctx)
				if err != nil {
					return fmt.Errorf("failed to update channel model price: %w", err)
				}
			}

			if err = svc.createChannelModelPriceVersion(ctx, entity, now); err != nil {
				return err
			}

			results = append(results, entity)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Do not expose uncommitted prices through the live cache in an outer transaction.
	svc.reloadChannelsAfterCommit(ctx)

	return results, nil
}

// preloadModelPrices loads active model prices for a channel and caches them.
func (svc *ChannelService) preloadModelPrices(ctx context.Context, ch *Channel) {
	prices, err := svc.entFromContext(ctx).ChannelModelPrice.Query().
		Where(
			channelmodelprice.ChannelID(ch.ID),
			channelmodelprice.DeletedAtEQ(0),
		).
		All(ctx)
	if err != nil {
		log.Warn(ctx, "failed to preload model prices", log.Int("channel_id", ch.ID), log.Cause(err))
		return
	}

	cache := make(map[string]*ent.ChannelModelPrice, len(prices))
	for _, p := range prices {
		cache[p.ModelID] = p
	}

	ch.cachedModelPrices = cache
	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "preloaded model prices", log.Int("channel_id", ch.ID), log.Int("count", len(cache)))
	}
}

const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func generateReferenceID() string {
	b := make([]byte, 8)
	for i := range b {
		//nolint:gosec // not a security issue.
		b[i] = letters[rand.IntN(len(letters))]
	}

	return string(b)
}
