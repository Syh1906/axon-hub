package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/server/biz"
)

// WithHealthGate enables per-channel, per-upstream-model routing for this balancer.
func (lb *LoadBalancer) WithHealthGate(gate *biz.HealthGate) *LoadBalancer {
	lb.healthGate = gate
	return lb
}

// compareChannelWeightTiers keeps hard rejections behind available channels
// before applying explicit weight tiers. Exhausted quota scores use the same
// hard-unavailable threshold. Soft quota warnings only affect scores within a
// weight tier; they do not override an explicit higher weight.
func compareChannelWeightTiers(a *biz.Channel, aScore float64, b *biz.Channel, bScore float64) int {
	aUnavailable := isHardUnavailableScore(aScore)
	bUnavailable := isHardUnavailableScore(bScore)
	if aUnavailable != bUnavailable {
		if aUnavailable {
			return 1
		}
		return -1
	}

	if a != nil && b != nil {
		if a.OrderingWeight > b.OrderingWeight {
			return -1
		} else if a.OrderingWeight < b.OrderingWeight {
			return 1
		}
	}

	return 0
}

// Resolve against current settings, not the candidate snapshot: a delayed
// request must never roll back a newer health-gate configuration cycle.
func currentHealthGateConfig(
	ctx context.Context,
	provider RetryPolicyProvider,
	service *biz.ChannelService,
	channel *biz.Channel,
	fallback biz.HealthGatePolicy,
) biz.HealthGateConfigResolver {
	return func() (biz.HealthGateConfig, bool) {
		currentChannel := channel
		if service != nil && channel != nil {
			currentChannel = service.GetEnabledChannel(channel.ID)
			if currentChannel == nil {
				return biz.HealthGateConfig{}, false
			}
		}
		currentPolicy := fallback
		if provider != nil {
			currentPolicy = provider.RetryPolicyOrDefault(ctx).HealthGateOrDefault()
		}
		return biz.ResolveHealthGateConfig(currentPolicy, currentChannel), true
	}
}
