package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type healthGateCandidateInfo struct {
	probeEligible bool
	lastResort    bool
	decision      *healthGateRequestDecision
}

type gatedModel struct {
	candidate *ChannelModelsCandidate
	index     int
	view      biz.HealthGateView
}

func (s *LoadBalancedSelector) selectHealthGated(
	ctx context.Context,
	req *llm.Request,
	candidates []*ChannelModelsCandidate,
	policy *biz.RetryPolicy,
	stickyMode biz.TraceStickyMode,
	lb *LoadBalancer,
) ([]*ChannelModelsCandidate, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	decision, probeEligible := s.healthGateSessionDecision(ctx, stickyMode, policy)
	var channelService *biz.ChannelService
	if service, ok := lb.selectionTracker.(*biz.ChannelService); ok {
		channelService = service
	}
	decision.probeEligible = probeEligible
	decision.candidates = candidates
	decision.channelSvc = channelService
	if decision.found && !decision.ownerSessionReuseEnabled(decision.owner.ChannelID) {
		probeEligible = true
		decision.probeEligible = true
	}
	decision.requestSvc, _ = s.previousChannelProvider.(*biz.RequestService)
	decision.policy = s.policy
	decision.fallback = policy.HealthGateOrDefault()
	if decision.found {
		decision.record.Owner = &objects.RoutingDecisionCombo{
			ChannelID:   decision.owner.ChannelID,
			ChannelName: healthGateOwnerName(ctx, decision.owner.ChannelID, candidates, channelService, decision.requestSvc),
		}
	}
	filtered := make([]*ChannelModelsCandidate, 0, len(candidates))
	var lastResort *gatedModel
	ownerSeen, ownerAllowed := false, false
	for _, candidate := range candidates {
		if candidate == nil || candidate.Channel == nil {
			continue
		}
		ownerCandidate := decision.found && candidate.Channel.ID == decision.owner.ChannelID && candidate.Channel.SessionReuseEnabled()
		if ownerCandidate {
			ownerSeen = true
			decision.record.Owner.ChannelName = candidate.Channel.Name
		}
		cfg := currentHealthGateConfig(ctx, s.policy, channelService, candidate.Channel, policy.HealthGateOrDefault())
		indices := make([]int, 0, len(candidate.Models))
		for index, entry := range candidate.Models {
			view, err := lb.healthGate.Inspect(ctx, biz.HealthGateKey{ChannelID: candidate.Channel.ID, ActualModel: entry.ActualModel}, cfg)
			if err != nil {
				return nil, err
			}
			combo := healthGateCombo(candidate, entry.ActualModel, view.State)
			if ownerCandidate && decision.record.Owner.ActualModel == "" {
				decision.record.Owner.ActualModel = combo.ActualModel
				decision.record.Owner.State = combo.State
			}
			if view.State == biz.HealthGateStateHealthy || view.State == biz.HealthGateStateUnstable ||
				(view.State == biz.HealthGateStateProbing && probeEligible && !view.ProbeBusy) {
				indices = append(indices, index)
				if ownerCandidate {
					ownerAllowed = true
				}
				continue
			}
			decision.record.Skipped = append(decision.record.Skipped, combo)
			if lastResort == nil || healthGateLastResortBefore(view, lastResort.view) {
				lastResort = &gatedModel{candidate: candidate, index: index, view: view}
			}
		}
		if len(indices) != 0 {
			filtered = append(filtered, cloneHealthGateCandidate(ctx, req, candidate, indices, &healthGateCandidateInfo{probeEligible: probeEligible, decision: decision}))
		}
	}
	decision.ownerOpen = decision.found && ownerSeen && !ownerAllowed
	if len(filtered) == 0 {
		if lastResort == nil {
			return filtered, nil
		}
		selected := healthGateCombo(lastResort.candidate, lastResort.candidate.Models[lastResort.index].ActualModel, lastResort.view.State)
		for i, skipped := range decision.record.Skipped {
			if skipped.ChannelID == selected.ChannelID && skipped.ActualModel == selected.ActualModel {
				decision.record.Skipped = append(decision.record.Skipped[:i], decision.record.Skipped[i+1:]...)
				break
			}
		}
		decision.record.LastResort = true
		clone := cloneHealthGateCandidate(ctx, req, lastResort.candidate, []int{lastResort.index}, &healthGateCandidateInfo{lastResort: true, decision: decision})
		clone.TraceSticky = false
		return []*ChannelModelsCandidate{clone}, nil
	}
	requiredCount := 1
	if policy.Enabled {
		requiredCount += policy.MaxChannelRetries
	}
	if decision.sticky && decision.found && ownerAllowed {
		if sticky, remaining := extractStickyCandidate(filtered, decision.owner.ChannelID); sticky != nil {
			sticky.TraceSticky = true
			fallbacks := s.sortCandidates(ctx, lb, remaining, req, max(requiredCount-1, 0), false)
			lb.TrackSelection(sticky)
			return append([]*ChannelModelsCandidate{sticky}, fallbacks...), nil
		}
	}
	return s.sortCandidates(ctx, lb, filtered, req, requiredCount, true), nil
}

func healthGateLastResortBefore(a, b biz.HealthGateView) bool {
	if a.State == biz.HealthGateStateProbing && b.State != biz.HealthGateStateProbing {
		return true
	}
	if b.State == biz.HealthGateStateProbing && a.State != biz.HealthGateStateProbing {
		return false
	}
	return a.OpenUntil.Before(b.OpenUntil)
}

func cloneHealthGateCandidate(ctx context.Context, req *llm.Request, source *ChannelModelsCandidate, indices []int, info *healthGateCandidateInfo) *ChannelModelsCandidate {
	clone := *source
	clone.Models = make([]biz.ChannelModelEntry, 0, len(indices))
	if len(source.modelAPIFormats) == len(source.Models) {
		clone.modelAPIFormats = make([]string, 0, len(indices))
	} else {
		clone.modelAPIFormats = nil
	}
	for _, index := range indices {
		clone.Models = append(clone.Models, source.Models[index])
		if clone.modelAPIFormats != nil {
			clone.modelAPIFormats = append(clone.modelAPIFormats, source.modelAPIFormats[index])
		}
	}
	if len(clone.modelAPIFormats) != 0 {
		clone.APIFormat = clone.modelAPIFormats[0]
	} else if indices[0] != 0 {
		endpoints := applyForcedAPIFormats(ctx, clone.Channel, clone.Models[:1], req.Model, clone.Channel.ResolveEndpoints())
		clone.APIFormat = SelectAPIFormat(endpoints, req)
	}
	clone.healthGate = info
	return &clone
}
