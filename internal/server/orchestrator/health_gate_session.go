package orchestrator

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz"
)

type healthGateDecisionContextKey struct{}

func healthGateDecisionFromContext(ctx context.Context) *healthGateRequestDecision {
	if slot, ok := ctx.Value(healthGateDecisionContextKey{}).(**healthGateRequestDecision); ok && slot != nil {
		return *slot
	}
	return nil
}

// Older selector callers need only the previous-channel interface. The
// production RequestService implements this additional session-owner method.
type healthGateSessionOwnerProvider interface {
	GetSessionOwner(ctx context.Context, threadID, traceID int) (biz.SessionOwner, bool, error)
}

type healthGateRequestDecision struct {
	threadID      int
	traceID       int
	sticky        bool
	owner         biz.SessionOwner
	lookupFailed  bool
	found         bool
	ownerOpen     bool
	probeEligible bool
	// candidates is the set after ordinary request admission and before health
	// gating. It is needed to recheck every owner model at completion.
	candidates []*ChannelModelsCandidate
	channelSvc *biz.ChannelService
	requestSvc *biz.RequestService
	policy     RetryPolicyProvider
	fallback   biz.HealthGatePolicy
	record     objects.RequestRoutingDecision
	threshold  int
}

func (s *LoadBalancedSelector) healthGateSessionDecision(ctx context.Context, mode biz.TraceStickyMode, policy *biz.RetryPolicy) (*healthGateRequestDecision, bool) {
	d := &healthGateRequestDecision{
		sticky:    mode == biz.TraceStickyPreferPreviousChannel,
		threshold: policy.HealthGateOrDefault().OwnerFailoverThreshold,
		record: objects.RequestRoutingDecision{
			Skipped: []objects.RoutingDecisionCombo{},
		},
	}
	if slot, ok := ctx.Value(healthGateDecisionContextKey{}).(**healthGateRequestDecision); ok && slot != nil {
		*slot = d
	}
	if !d.sticky {
		return d, true
	}
	trace, hasTrace := contexts.GetTrace(ctx)
	if hasTrace && trace != nil {
		d.traceID = trace.ID
		d.threadID = trace.ThreadID
	}
	if thread, ok := contexts.GetThread(ctx); ok && thread != nil {
		d.threadID = thread.ID
	}
	provider, ok := s.previousChannelProvider.(healthGateSessionOwnerProvider)
	if !ok || provider == nil {
		return d, s.previousChannelProvider == nil
	}
	owner, found, err := provider.GetSessionOwner(ctx, d.threadID, d.traceID)
	if err != nil {
		d.lookupFailed = true
		log.Warn(ctx, "failed to read health gate session owner", log.Cause(err))
		return d, false
	}
	d.owner, d.found = owner, found && owner.ChannelID != 0
	if d.found {
		d.record.ConsecutiveFailovers = owner.ConsecutiveFailovers
	}
	return d, !d.found
}

func healthGateCombo(candidate *ChannelModelsCandidate, actualModel string, state biz.HealthGateState) objects.RoutingDecisionCombo {
	return objects.RoutingDecisionCombo{
		ChannelID: candidate.Channel.ID, ChannelName: candidate.Channel.Name,
		ActualModel: actualModel, State: string(state),
	}
}

func (d *healthGateRequestDecision) ownerSessionReuseEnabled(channelID int) bool {
	if d.channelSvc != nil {
		if current := d.channelSvc.GetEnabledChannel(channelID); current != nil {
			return current.SessionReuseEnabled()
		}
	}
	for _, candidate := range d.candidates {
		if candidate != nil && candidate.Channel != nil && candidate.Channel.ID == channelID {
			return candidate.Channel.SessionReuseEnabled()
		}
	}
	return true
}

// The owner may have been excluded by model, permission, or quota filtering.
// Capture its name at selection time while keeping its model/state empty.
func healthGateOwnerName(ctx context.Context, channelID int, candidates []*ChannelModelsCandidate, service *biz.ChannelService, requests *biz.RequestService) string {
	for _, candidate := range candidates {
		if candidate != nil && candidate.Channel != nil && candidate.Channel.ID == channelID {
			return candidate.Channel.Name
		}
	}
	if service != nil {
		if current := service.GetEnabledChannel(channelID); current != nil {
			return current.Name
		}
	}
	if requests != nil {
		if name, err := requests.SessionOwnerChannelName(ctx, channelID); err == nil {
			return name
		}
	}
	return ""
}

// ownerGatedAtCompletion considers every admitted model for the CURRENT owner.
// A missing owner is a regular failover; one healthy owner model is sufficient
// to prevent an immediate owner_open migration.
func (d *healthGateRequestDecision) ownerGatedAtCompletion(ctx context.Context, gate *biz.HealthGate, channelID int) bool {
	if gate == nil {
		return false
	}
	seen := false
	for _, candidate := range d.candidates {
		if candidate == nil || candidate.Channel == nil || candidate.Channel.ID != channelID {
			continue
		}
		cfg := currentHealthGateConfig(ctx, d.policy, d.channelSvc, candidate.Channel, d.fallback)
		for _, model := range candidate.Models {
			seen = true
			view, err := gate.Inspect(ctx, biz.HealthGateKey{ChannelID: channelID, ActualModel: model.ActualModel}, cfg)
			if err != nil {
				return false
			}
			if view.State == biz.HealthGateStateHealthy || view.State == biz.HealthGateStateUnstable ||
				(view.State == biz.HealthGateStateProbing && d.probeEligible && !view.ProbeBusy) {
				return false
			}
		}
	}
	return seen
}

// finishSession records actual completion. Read/modify/write of the current
// session owner happens in biz under its per-session lock, so a late completion
// cannot overwrite a newer migration made after this request selected candidates.
func (m *healthGateAttemptTracker) finishSession(ctx context.Context, successful bool) {
	d := m.decision
	if d == nil || m.outbound == nil || m.outbound.state == nil {
		return
	}
	ctx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()
	state := m.outbound.state
	record := d.record
	record.LastResort = m.lastResort || record.LastResort
	if successful && m.channel != nil && m.channel.SessionReuseEnabled() && d.sticky && state.RequestService != nil && (d.threadID != 0 || d.traceID != 0) {
		success := healthGateCombo(state.CurrentCandidate, m.key.ActualModel, "")
		// This is the protection window declared by the final upstream request,
		// after overrides; it does not imply an upstream cache hit.
		ttl := sessionOwnerProtectionTTL(state.RawProviderRequest)
		state.RequestService.UpdateSessionOwner(ctx, d.threadID, d.traceID, func(current biz.SessionOwner, found bool) (biz.SessionOwner, bool) {
			record.ConsecutiveFailovers = 0
			if found {
				record.ConsecutiveFailovers = current.ConsecutiveFailovers
			}
			if d.found && found && !current.Generation.Equal(d.owner.Generation) {
				return current, false
			}
			if !d.found && found && current.ChannelID != success.ChannelID {
				// Concurrent fresh requests may select different channels. The
				// first completed binding wins; a late fresh success is not a
				// failover from an owner it never selected.
				return current, false
			}
			if !found && d.found {
				// The selected lease expired while this request was in flight.
				// A fresh selection may already be using another channel; this
				// old completion cannot resurrect the expired binding.
				return current, false
			}
			// A completion selected against another owner cannot count as a
			// failover from the new binding, including success on a third channel.
			// Success on the current owner itself may still refresh its lease.
			if d.found && current.ChannelID > 0 && current.ChannelID != d.owner.ChannelID && success.ChannelID != current.ChannelID {
				return current, false
			}
			completedOwner := func(channelID int) biz.SessionOwner {
				generation := time.Now()
				if found && current.ChannelID == channelID {
					generation = current.Generation
				} else if !generation.After(current.Generation) {
					// The retained expired identity also guards late completions.
					// A coarse or corrected clock must not reuse that identity.
					generation = current.Generation.Add(time.Nanosecond)
				}
				return biz.SessionOwner{ChannelID: channelID, ExpiresAt: time.Now().Add(ttl), Generation: generation}
			}
			if !found {
				return completedOwner(success.ChannelID), true
			}
			if current.ChannelID == success.ChannelID {
				record.ConsecutiveFailovers = 0
				return completedOwner(current.ChannelID), true
			}
			fromName := healthGateOwnerName(ctx, current.ChannelID, d.candidates, d.channelSvc, d.requestSvc)
			if d.found && current.ChannelID == d.owner.ChannelID && d.record.Owner != nil {
				fromName = d.record.Owner.ChannelName
			}
			migration := func(reason string) (biz.SessionOwner, bool) {
				record.ConsecutiveFailovers = 0
				record.Migration = &objects.RoutingDecisionMigration{
					FromChannelID: current.ChannelID, FromChannelName: fromName,
					ToChannelID: success.ChannelID, ToChannelName: success.ChannelName,
					Reason: reason,
				}
				return completedOwner(success.ChannelID), true
			}
			if !d.ownerSessionReuseEnabled(current.ChannelID) {
				return migration(objects.RoutingMigrationReasonSessionReuseDisabled)
			}
			if d.ownerGatedAtCompletion(ctx, m.gate, current.ChannelID) {
				return migration(objects.RoutingMigrationReasonOwnerOpen)
			}
			count := current.ConsecutiveFailovers + 1
			if count >= d.threshold {
				return migration(objects.RoutingMigrationReasonConsecutiveFailovers)
			}
			record.ConsecutiveFailovers = count
			record.TemporaryFailover = true
			current.ConsecutiveFailovers = count
			return current, true
		})
	}
	if state.Request == nil || state.RequestService == nil {
		return
	}
	if err := state.RequestService.SetRequestRoutingDecision(ctx, state.Request.ID, &record); err != nil {
		log.Warn(ctx, "failed to persist health gate routing decision", log.Cause(err))
	}
}
