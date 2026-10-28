package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelupstreamstatus"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
)

func upstreamConfigFingerprint(ch *ent.Channel) string {
	data, _ := json.Marshal(struct {
		BaseURL     string
		Credentials *objects.ChannelUpstreamCredentials
	}{ch.BaseURL, ch.UpstreamCredentials})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func channelManualMultiplier(ch *ent.Channel) decimal.Decimal {
	if ch.Settings != nil && ch.Settings.ModelPriceMultiplier != nil {
		return *ch.Settings.ModelPriceMultiplier
	}
	return decimal.NewFromInt(1)
}

func appliedUpstreamMultiplier(settings objects.ChannelUpstreamSettings, manual decimal.Decimal, snapshot *objects.UpstreamKeySnapshot, now time.Time) objects.AppliedCostMultiplier {
	applied := objects.AppliedCostMultiplier{Value: manual, Source: objects.CostMultiplierManual, Provider: objects.UpstreamProviderUnknown}
	if !settings.Enabled || settings.CostMode != objects.ChannelCostModeUpstream {
		return applied
	}
	applied.Source = objects.CostMultiplierEstimated
	if snapshot == nil {
		return applied
	}
	applied.Provider = snapshot.Provider
	if snapshot.Rate == nil || (snapshot.RateState != objects.UpstreamQuerySuccess && snapshot.RateState != objects.UpstreamQueryError) {
		return applied
	}
	rate := snapshot.Rate
	if now.Sub(rate.SyncedAt) > 30*time.Minute || now.Sub(rate.ObservedAt) > 30*time.Minute || (rate.ValidUntil != nil && !now.Before(*rate.ValidUntil)) {
		return applied
	}
	applied.Value, applied.Source, applied.ObservedAt = rate.Multiplier, objects.CostMultiplierUpstream, &rate.ObservedAt
	if snapshot.RateState != objects.UpstreamQuerySuccess || now.Sub(rate.SyncedAt) > time.Duration(settings.RefreshIntervalMinutes)*time.Minute {
		applied.Source = objects.CostMultiplierCached
	}
	return applied
}

func (svc *ChannelService) upstreamChannel(ctx context.Context, id int, scope scopes.ScopeSlug) (*ent.Channel, error) {
	if err := authz.RequireScope(ctx, scope); err != nil {
		return nil, err
	}
	return authz.RunWithScopeDecision(ctx, scope, func(ctx context.Context) (*ent.Channel, error) { return svc.entFromContext(ctx).Channel.Get(ctx, id) })
}

func (svc *ChannelService) upstreamState(ctx context.Context, ch *ent.Channel) (*objects.ChannelUpstreamState, error) {
	rows, err := svc.entFromContext(ctx).ChannelUpstreamStatus.Query().Where(channelupstreamstatus.ChannelID(ch.ID), channelupstreamstatus.ConfigFingerprint(upstreamConfigFingerprint(ch))).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load upstream snapshots: %w", err)
	}
	byKey := make(map[string]*objects.UpstreamKeySnapshot, len(rows))
	for _, row := range rows {
		byKey[row.KeyFingerprint] = row.Snapshot
	}
	state := &objects.ChannelUpstreamState{ChannelID: objects.GUID{ID: ch.ID, Type: ent.TypeChannel}, Settings: ch.Settings.UpstreamSettings(), ManualMultiplier: channelManualMultiplier(ch), Provider: objects.UpstreamProviderUnknown, Keys: []*objects.ChannelUpstreamKeyState{}}
	if ch.UpstreamCredentials != nil {
		state.AccountConnected = ch.UpstreamCredentials.AccountToken != ""
		if ch.UpstreamCredentials.AccountID > 0 {
			state.AccountID = lo.ToPtr(ch.UpstreamCredentials.AccountID)
		}
	}
	enabled := ch.Credentials.GetEnabledAPIKeys(ch.DisabledAPIKeys)
	for _, key := range lo.Uniq(ch.Credentials.GetAllAPIKeys()) {
		ref := upstream.KeyRef(key)
		snapshot := byKey[ref]
		if snapshot == nil {
			snapshot = upstream.EmptySnapshot()
		}
		if snapshot.Provider != objects.UpstreamProviderUnknown {
			state.Provider = snapshot.Provider
		}
		item := &objects.ChannelUpstreamKeyState{KeyRef: ref, Label: upstream.MaskKey(key), Disabled: !slices.Contains(enabled, key), Snapshot: snapshot, Applied: appliedUpstreamMultiplier(state.Settings, state.ManualMultiplier, snapshot, time.Now())}
		if snapshot.Rate != nil {
			item.UpstreamMultiplier = lo.ToPtr(snapshot.Rate.Multiplier)
		}
		state.Keys = append(state.Keys, item)
	}
	return state, nil
}

func (svc *ChannelService) ChannelUpstreamStates(ctx context.Context, ids []int) ([]*objects.ChannelUpstreamState, error) {
	if len(ids) > 100 {
		return nil, fmt.Errorf("at most 100 channels per query")
	}
	states := make([]*objects.ChannelUpstreamState, 0, len(ids))
	for _, id := range lo.Uniq(ids) {
		ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeReadChannels)
		if err != nil {
			return nil, err
		}
		state, err := svc.upstreamState(ctx, ch)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (svc *ChannelService) ChannelUpstreamRefreshTargets(ctx context.Context) ([]*objects.GUID, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeWriteChannels); err != nil {
		return nil, err
	}
	channels, err := svc.entFromContext(ctx).Channel.Query().Where(channel.StatusNEQ(channel.StatusArchived)).All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]*objects.GUID, 0, len(channels))
	for _, ch := range channels {
		if ch.Settings.UpstreamSettings().Enabled {
			ids = append(ids, &objects.GUID{ID: ch.ID, Type: ent.TypeChannel})
		}
	}
	return ids, nil
}

func (svc *ChannelService) upstreamClient(ch *ent.Channel) (*upstream.Client, error) {
	client := svc.httpClient
	if ch.Settings != nil && ch.Settings.Proxy != nil {
		client = client.WithProxy(ch.Settings.Proxy)
	}
	return upstream.NewClient(client, ch.BaseURL)
}

func (svc *ChannelService) VerifyChannelUpstreamAccount(ctx context.Context, id int, token string, accountID *int) (*objects.UpstreamAccountVerification, error) {
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return nil, err
	}
	client, err := svc.upstreamClient(ch)
	if err != nil {
		return nil, err
	}
	key := ""
	if keys := ch.Credentials.GetAllAPIKeys(); len(keys) > 0 {
		key = keys[0]
	}
	idValue := 0
	if accountID != nil {
		idValue = *accountID
	}
	queryCtx, cancel := upstreamQueryContext(ctx)
	defer cancel()
	return client.VerifyAccount(queryCtx, strings.TrimSpace(token), idValue, key), nil
}

func (svc *ChannelService) UpdateChannelUpstream(ctx context.Context, id int, input objects.UpdateChannelUpstreamInput) (*objects.ChannelUpstreamState, error) {
	if input.RefreshIntervalMinutes < 1 || input.RefreshIntervalMinutes > 1440 {
		return nil, fmt.Errorf("refresh interval must be between 1 and 1440 minutes")
	}
	if !input.CostMode.IsValid() || input.ManualMultiplier.IsNegative() {
		return nil, fmt.Errorf("invalid cost mode or multiplier")
	}
	if !input.Enabled {
		input.CostMode = objects.ChannelCostModeManual
	}
	var result *ent.Channel
	err := svc.RunInTransaction(ctx, func(ctx context.Context) error {
		ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
		if err != nil {
			return err
		}
		// Version prices through the existing pricing service so manual values remain historical.
		if !channelManualMultiplier(ch).Equal(input.ManualMultiplier) {
			if _, err := svc.UpdateChannelCostMultiplier(ctx, id, input.ManualMultiplier); err != nil {
				return err
			}
			ch, err = svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
			if err != nil {
				return err
			}
		}
		settings := objects.ChannelSettings{}
		if ch.Settings != nil {
			settings = *ch.Settings
		}
		settings.Upstream = &objects.ChannelUpstreamSettings{Enabled: input.Enabled, RefreshIntervalMinutes: input.RefreshIntervalMinutes, CostMode: input.CostMode}
		settings.ModelPriceMultiplier = lo.ToPtr(input.ManualMultiplier)
		mutation := svc.entFromContext(ctx).Channel.UpdateOneID(id).Where(channel.UpdatedAtEQ(ch.UpdatedAt)).SetSettings(&settings)
		if input.ClearAccount {
			mutation.ClearUpstreamCredentials()
		} else if input.AccountToken != nil && strings.TrimSpace(*input.AccountToken) != "" {
			credential := &objects.ChannelUpstreamCredentials{AccountToken: strings.TrimSpace(*input.AccountToken)}
			if input.AccountID != nil {
				credential.AccountID = *input.AccountID
			}
			mutation.SetUpstreamCredentials(credential)
		} else if input.AccountID != nil && ch.UpstreamCredentials != nil {
			credential := *ch.UpstreamCredentials
			credential.AccountID = *input.AccountID
			mutation.SetUpstreamCredentials(&credential)
		}
		result, err = mutation.Save(ctx)
		if err != nil {
			return err
		}
		svc.reloadChannelsAfterCommit(ctx)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return svc.upstreamState(ctx, result)
}
