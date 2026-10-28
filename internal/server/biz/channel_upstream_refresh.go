package biz

import (
	"context"
	"fmt"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelupstreamstatus"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
)

func (svc *ChannelService) refreshChannelUpstream(ctx context.Context, id int, force, includeDisabled, rateOnly bool, keyRef string) (*objects.ChannelUpstreamState, bool, error) {
	svc.upstreamRefreshOnce.Do(func() {
		for i := range svc.upstreamRefreshLocks {
			svc.upstreamRefreshLocks[i] = make(chan struct{}, 1)
		}
	})
	lock := svc.upstreamRefreshLocks[uint(id)%uint(len(svc.upstreamRefreshLocks))]
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	ch, err := svc.upstreamChannel(ctx, id, scopes.ScopeWriteChannels)
	if err != nil {
		return nil, false, err
	}
	state, err := svc.upstreamState(ctx, ch)
	if err != nil {
		return nil, false, err
	}
	if keyRef != "" && !lo.ContainsBy(state.Keys, func(key *objects.ChannelUpstreamKeyState) bool { return key.KeyRef == keyRef }) {
		return nil, false, fmt.Errorf("upstream key no longer exists in channel")
	}
	if (!state.Settings.Enabled && !includeDisabled) || ch.Status == channel.StatusArchived {
		return state, false, nil
	}
	keys := make(map[string]string)
	for _, key := range ch.Credentials.GetAllAPIKeys() {
		keys[upstream.KeyRef(key)] = key
	}
	client, err := svc.upstreamClient(ch)
	if err != nil {
		return nil, false, err
	}
	var session *upstream.Session
	refreshed := false
	for _, item := range state.Keys {
		if (keyRef != "" && item.KeyRef != keyRef) || (item.Disabled && keyRef == "") {
			continue
		}
		if !includeDisabled && item.Snapshot.BalanceState == objects.UpstreamQueryUnsupported && item.Snapshot.RateState == objects.UpstreamQueryUnsupported {
			continue
		}
		now := time.Now()
		balanceDue := !rateOnly && upstream.Due(item.Snapshot.BalanceState, item.Snapshot.NextBalanceCheckAt, force, now)
		rateDue := upstream.Due(item.Snapshot.RateState, item.Snapshot.NextRateCheckAt, force, now)
		if !balanceDue && !rateDue {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, refreshed, err
		}
		// 外部查询共享单 Key 总预算；保留父请求的时间用于独立保存结果。
		queryCtx, cancel := upstreamQueryContext(ctx)
		if queryCtx.Err() != nil {
			cancel()
			return nil, refreshed, context.DeadlineExceeded
		}
		if session == nil {
			session = client.Prepare(queryCtx, keys[item.KeyRef], state.Provider, ch.UpstreamCredentials)
		}
		snapshot := session.Query(queryCtx, keys[item.KeyRef], item.Snapshot, balanceDue, rateDue, time.Duration(state.Settings.RefreshIntervalMinutes)*time.Minute)
		cancel()
		if err := svc.entFromContext(ctx).ChannelUpstreamStatus.Create().SetChannelID(ch.ID).SetKeyFingerprint(item.KeyRef).
			SetConfigFingerprint(upstreamConfigFingerprint(ch)).SetSnapshot(snapshot).
			OnConflictColumns(channelupstreamstatus.FieldChannelID, channelupstreamstatus.FieldKeyFingerprint).
			UpdateNewValues().Exec(ctx); err != nil {
			return nil, refreshed, fmt.Errorf("save upstream snapshot: %w", err)
		}
		refreshed = true
	}
	state, err = svc.upstreamState(ctx, ch)
	return state, refreshed, err
}

func (svc *ChannelService) RefreshChannelUpstreams(ctx context.Context, ids []int, force, includeDisabled bool, keyRef *string) (*objects.RefreshChannelUpstreamsResult, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeWriteChannels); err != nil {
		return nil, err
	}
	if len(ids) != 1 || keyRef == nil || *keyRef == "" {
		return nil, fmt.Errorf("refresh requires exactly one channel and one key reference")
	}
	result := &objects.RefreshChannelUpstreamsResult{Channels: []*objects.ChannelUpstreamState{}}
	ref := ""
	if keyRef != nil {
		ref = *keyRef
	}
	for _, id := range lo.Uniq(ids) {
		state, refreshed, err := svc.refreshChannelUpstream(ctx, id, force, includeDisabled, false, ref)
		if err != nil {
			return nil, err
		}
		result.Channels = append(result.Channels, state)
		if !refreshed {
			result.Skipped++
			continue
		}
		result.Refreshed++
		for _, key := range state.Keys {
			if key.KeyRef != ref {
				continue
			}
			if key.Snapshot.BalanceState == objects.UpstreamQueryError || key.Snapshot.RateState == objects.UpstreamQueryError {
				result.Failed++
				break
			}
		}
	}
	return result, nil
}

func upstreamQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(24 * time.Second)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Add(-2*time.Second).Before(deadline) {
		deadline = parentDeadline.Add(-2 * time.Second)
	}
	return context.WithDeadline(ctx, deadline)
}

func (svc *ChannelService) syncUpstreamRates(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, "channel-upstream-rate-sync")
	channels, err := svc.entFromContext(ctx).Channel.Query().Where(channel.StatusEQ(channel.StatusEnabled)).All(ctx)
	if err != nil {
		log.Warn(ctx, "load channels for upstream rate sync", log.Cause(err))
		return
	}
	for _, ch := range channels {
		settings := ch.Settings.UpstreamSettings()
		if !settings.Enabled || settings.CostMode != objects.ChannelCostModeUpstream {
			continue
		}
		if _, _, err := svc.refreshChannelUpstream(ctx, ch.ID, false, false, true, ""); err != nil {
			log.Warn(ctx, "sync channel upstream rate", log.Int("channel_id", ch.ID), log.Cause(err))
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func loadRequestUpstreamSnapshot(ctx context.Context, db *ent.Client, ch *ent.Channel, key string) (*objects.UpstreamKeySnapshot, error) {
	return authz.RunWithSystemBypass(ctx, "request-selected-key-upstream-rate", func(ctx context.Context) (*objects.UpstreamKeySnapshot, error) {
		row, err := db.ChannelUpstreamStatus.Query().Where(channelupstreamstatus.ChannelID(ch.ID), channelupstreamstatus.KeyFingerprint(upstream.KeyRef(key)), channelupstreamstatus.ConfigFingerprint(upstreamConfigFingerprint(ch))).Only(ctx)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return row.Snapshot, nil
	})
}
