package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
)

func executionSelection(ctx context.Context, state *PersistenceState) *objects.RequestExecutionSelection {
	candidate := state.ChannelModelsCandidates[state.CurrentCandidateIndex]
	channel := candidate.Channel
	selection := &objects.RequestExecutionSelection{
		ChannelName: channel.Name, Provider: objects.ChannelProvider(string(channel.Type)),
		SessionState: candidate.SessionState, Strategy: state.RoutingPolicy.LoadBalancerStrategy,
		Priority: candidate.Priority, Candidates: []objects.RequestRoutingCandidate{},
	}
	if selection.SessionState == "" {
		selection.SessionState = "unrecorded"
	}
	if !channel.SessionReuseEnabled() {
		selection.SessionState = "disabled"
	}
	if candidate.TraceSticky {
		selection.SessionState = "reused"
	}
	if info := candidate.healthGate; info != nil {
		if !info.decision.sticky {
			selection.SessionState = "disabled"
		} else if info.decision.found {
			if candidate.TraceSticky {
				selection.SessionState = "reused"
			} else {
				selection.SessionState = "switched"
			}
		} else if info.decision.lookupFailed {
			selection.SessionState = "unrecorded"
		} else {
			selection.SessionState = "new"
		}
	}
	for index, item := range state.ChannelModelsCandidates {
		models := make([]string, 0, len(item.Models))
		for _, model := range item.Models {
			models = append(models, model.ActualModel)
		}
		selection.Candidates = append(selection.Candidates, objects.RequestRoutingCandidate{
			ChannelID: item.Channel.ID, ChannelName: item.Channel.Name,
			Provider: objects.ChannelProvider(string(item.Channel.Type)), Priority: item.Priority,
			Models: models, SessionReuse: item.TraceSticky,
		})
		if index < state.CurrentCandidateIndex && item.TraceSticky {
			selection.SessionState = "switched"
		}
	}
	if key, ok := contexts.GetChannelAPIKey(ctx); ok && key != "" {
		selection.KeyHint = upstream.MaskKey(key)
		selection.KeyRemark = channel.Settings.KeySettingsFor(upstream.KeyRef(key)).Remark
	}
	return selection
}

// Retry decisions are captured at the actual transition/stop, never reconstructed
// later from status codes or from the channel's current policy.
func (p *PersistentOutboundTransformer) recordRetryDecision(ctx context.Context, decision string) {
	if p.state.RequestExec == nil {
		return
	}
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.state.RequestService.SetExecutionRetryDecision(persistCtx, p.state.RequestExec.ID, decision); err != nil {
		log.Warn(ctx, "failed to persist retry decision", log.Cause(err))
	}
}

func (p *PersistentOutboundTransformer) OnRetryStopped(ctx context.Context, reason string) {
	if p.state.BudgetStop != nil {
		reason = "budget_denied"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		reason = "client_canceled"
	}
	p.recordRetryDecision(ctx, reason)
}

var _ interface{ OnRetryStopped(context.Context, string) } = (*PersistentOutboundTransformer)(nil)

// Keep the accepted routing policy's semantics explicit at the selector.
func markCandidateSessionState(candidates []*ChannelModelsCandidate, state string) {
	for _, candidate := range candidates {
		candidate.SessionState = state
	}
}
