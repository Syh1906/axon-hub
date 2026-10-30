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

func upstreamConfigFingerprint(ch *ent.Channel, keyRef string) string {
	accountRef := ch.Settings.KeySettingsFor(keyRef).AccountRef
	account := ch.UpstreamCredentials.Account(accountRef)
	var token string
	var accountID int
	var provider objects.UpstreamProvider
	if account != nil {
		token, accountID, provider = account.AccountToken, account.AccountID, account.Provider
	}
	data, _ := json.Marshal(struct {
		BaseURL    string
		AccountRef string
		Token      string
		AccountID  int
		Provider   objects.UpstreamProvider
	}{ch.BaseURL, accountRef, token, accountID, provider})
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
	if snapshot.Provider.BalanceOnly() {
		applied.Source = objects.CostMultiplierManual
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
	rows, err := svc.entFromContext(ctx).ChannelUpstreamStatus.Query().Where(channelupstreamstatus.ChannelID(ch.ID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load upstream snapshots: %w", err)
	}
	byKey := make(map[string]*objects.UpstreamKeySnapshot, len(rows))
	for _, row := range rows {
		if row.ConfigFingerprint == upstreamConfigFingerprint(ch, row.KeyFingerprint) {
			byKey[row.KeyFingerprint] = row.Snapshot
		}
	}
	state := &objects.ChannelUpstreamState{ChannelID: objects.GUID{ID: ch.ID, Type: ent.TypeChannel}, Settings: ch.Settings.UpstreamSettings(), ManualMultiplier: channelManualMultiplier(ch), Provider: objects.UpstreamProviderUnknown, Keys: []*objects.ChannelUpstreamKeyState{}, KeySessionReuse: ch.Settings == nil || !ch.Settings.DisableKeySessionReuse}
	enabled := ch.Credentials.GetEnabledAPIKeys(ch.DisabledAPIKeys)
	for _, key := range lo.Uniq(ch.Credentials.GetAllAPIKeys()) {
		ref := upstream.KeyRef(key)
		snapshot := byKey[ref]
		if snapshot == nil {
			snapshot = upstream.EmptySnapshot()
		}
		if snapshot.QueryVersion < upstream.QueryVersion && snapshot.Provider == objects.UpstreamProviderUnknown && (snapshot.BalanceState == objects.UpstreamQueryUnsupported || snapshot.BalanceState == objects.UpstreamQueryAuthRequired) {
			snapshot = upstream.EmptySnapshot()
		}
		if snapshot.Provider != objects.UpstreamProviderUnknown {
			state.Provider = snapshot.Provider
		}
		keySettings := ch.Settings.KeySettingsFor(ref)
		item := &objects.ChannelUpstreamKeyState{KeyRef: ref, Label: upstream.MaskKey(key), Remark: keySettings.Remark, Priority: keySettings.Priority, AccountRef: keySettings.AccountRef, Disabled: !slices.Contains(enabled, key), Snapshot: snapshot, Applied: appliedUpstreamMultiplier(state.Settings, state.ManualMultiplier, snapshot, time.Now())}
		if snapshot.Rate != nil {
			item.UpstreamMultiplier = lo.ToPtr(snapshot.Rate.Multiplier)
		}
		state.Keys = append(state.Keys, item)
	}
	populateUpstreamAccounts(ch, state, time.Now())
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

func (svc *ChannelService) VerifyChannelUpstreamAccount(ctx context.Context, id int, token *string, accountID *int, accountRef *string) (*objects.UpstreamAccountVerification, error) {
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return nil, err
	}
	credential := objects.UpstreamAccountCredentials{}
	if accountRef != nil {
		stored := ch.UpstreamCredentials.Account(*accountRef)
		if stored == nil {
			return nil, fmt.Errorf("upstream account not found")
		}
		credential = *stored
	}
	if token != nil && strings.TrimSpace(*token) != "" {
		credential.AccountToken = strings.TrimSpace(*token)
	}
	if accountID != nil {
		credential.AccountID = *accountID
	}
	client, err := svc.upstreamClient(ch)
	if err != nil {
		return nil, err
	}
	key := ""
	if accountRef != nil {
		for _, candidate := range ch.Credentials.GetAllAPIKeys() {
			if ch.Settings.KeySettingsFor(upstream.KeyRef(candidate)).AccountRef == *accountRef {
				key = candidate
				break
			}
		}
	}
	queryCtx, cancel := upstreamQueryContext(ctx)
	defer cancel()
	return client.VerifyAccount(queryCtx, credential.AccountToken, credential.AccountID, key), nil
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
