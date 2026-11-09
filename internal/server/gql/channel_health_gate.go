package gql

import (
	"context"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/samber/lo"
)

func canReadChannelHealthGate(ctx context.Context) bool {
	return authz.HasScope(ctx, scopes.ScopeReadChannels)
}

func (r *Resolver) healthGatePolicy(ctx context.Context) biz.HealthGatePolicy {
	bypassCtx := authz.WithSystemBypass(ctx, "health-gate-policy")
	return r.systemService.RetryPolicyOrDefault(bypassCtx).HealthGateOrDefault()
}

func (r *Resolver) healthGateConfigResolver(ctx context.Context, channelID int) biz.HealthGateConfigResolver {
	return func() (biz.HealthGateConfig, bool) {
		ch, err := r.client.Channel.Get(authz.WithSystemBypass(ctx, "health-gate-channel"), channelID)
		if err != nil {
			return biz.HealthGateConfig{}, false
		}
		return biz.ResolveHealthGateConfig(r.healthGatePolicy(ctx), &biz.Channel{Channel: ch}), true
	}
}

func channelHealthGateStatus(ctx context.Context, gate *biz.HealthGate, policy biz.HealthGatePolicy, ch *ent.Channel, resolve biz.HealthGateConfigResolver) (*ChannelHealthGateStatus, error) {
	cfg := biz.ResolveHealthGateConfig(policy, &biz.Channel{Channel: ch})
	snapshots, err := gate.Snapshot(ctx, ch.ID, func() (biz.HealthGateConfig, bool) {
		current, ok := resolve()
		if ok {
			cfg = current
		}
		return current, ok
	})
	if err != nil {
		return nil, err
	}
	return healthGateStatusFromSnapshots(cfg, snapshots), nil
}

func healthGateStatusFromSnapshots(cfg biz.HealthGateConfig, snapshots []biz.HealthGateModelSnapshot) *ChannelHealthGateStatus {
	status := &ChannelHealthGateStatus{
		Disabled:              cfg.Disabled(),
		FailureThreshold:      cfg.FailureThreshold,
		ProbeSuccessThreshold: cfg.ProbeSuccessThreshold,
		Models:                []*ChannelHealthGateModel{},
	}
	for _, snapshot := range snapshots {
		model := &ChannelHealthGateModel{
			ActualModel:         snapshot.ActualModel,
			State:               string(snapshot.State),
			ConsecutiveFailures: snapshot.ConsecutiveFailures,
			ProbeSuccesses:      snapshot.ProbeSuccesses,
			BackoffLevel:        snapshot.BackoffLevel,
			LastErrorAt:         snapshot.LastErrorAt,
			OpenUntil:           snapshot.OpenUntil,
		}
		if snapshot.LastError != "" {
			model.LastError = lo.ToPtr(snapshot.LastError)
		}
		if snapshot.LastStatusCode != 0 {
			model.LastStatusCode = lo.ToPtr(snapshot.LastStatusCode)
		}
		switch snapshot.State {
		case biz.HealthGateStateOpen, biz.HealthGateStateProbing:
			status.OpenCount++
		case biz.HealthGateStateUnstable:
			status.UnstableCount++
		}
		status.Models = append(status.Models, model)
	}
	return status
}

func (r *mutationResolver) resetChannelHealthGate(ctx context.Context, channelID objects.GUID, actualModel *string) (bool, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeWriteChannels); err != nil {
		return false, err
	}
	if _, err := r.client.Channel.Get(ctx, channelID.ID); err != nil {
		return false, err
	}
	r.channelService.HealthGate().Reset(channelID.ID, lo.FromPtr(actualModel))
	return true, nil
}

func (r *queryResolver) filterHealthGateAbnormal(ctx context.Context, input *biz.QueryChannelsInput) error {
	if input.HealthGateAbnormal == nil || !*input.HealthGateAbnormal {
		return nil
	}
	gate := r.channelService.HealthGate()
	ids := gate.ChannelIDsWithEntries()
	matching := make([]int, 0, len(ids))
	if len(ids) != 0 {
		channels, err := r.client.Channel.Query().Where(channel.IDIn(ids...)).All(ctx)
		if err != nil {
			return err
		}
		for _, ch := range channels {
			resolve := r.healthGateConfigResolver(ctx, ch.ID)
			snapshots, err := gate.Snapshot(ctx, ch.ID, resolve)
			if err != nil {
				return err
			}
			for _, model := range snapshots {
				if model.State == biz.HealthGateStateOpen || model.State == biz.HealthGateStateProbing || model.State == biz.HealthGateStateUnstable {
					matching = append(matching, ch.ID)
					break
				}
			}
		}
	}
	if len(matching) == 0 {
		matching = append(matching, -1)
	}
	filter := &ent.ChannelWhereInput{IDIn: matching}
	if input.Where == nil {
		input.Where = filter
	} else {
		input.Where = &ent.ChannelWhereInput{And: []*ent.ChannelWhereInput{input.Where, filter}}
	}
	return nil
}
