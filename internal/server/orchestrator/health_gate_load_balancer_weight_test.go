package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/providerquotastatus"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

// Use the real scorers, with selection counts updated at selection time just
// like ChannelService. Leave activity timestamps unset to avoid time decay.
type healthGateWeightMetrics struct {
	mockMetricsProvider
}

func (m *healthGateWeightMetrics) IncrementChannelSelection(channelID int) {
	metrics := m.metrics[channelID]
	if metrics == nil {
		metrics = &biz.AggregatedMetrics{}
		m.metrics[channelID] = metrics
	}
	metrics.RequestCount++
}

func healthGateWeightStrategies(metrics ChannelMetricsProvider) []LoadBalanceStrategy {
	return []LoadBalanceStrategy{
		NewWeightRoundRobinStrategy(metrics),
		NewLatencyAwareStrategy(metrics),
		NewRateLimitAwareStrategy(NewChannelRequestTracker(), nil),
		NewQuotaAwareStrategy(nil, &mockQuotaEnforcementSettingsProvider{}),
	}
}

func healthGateWeightTotalScore(t *testing.T, ctx context.Context, strategies []LoadBalanceStrategy, channel *biz.Channel) float64 {
	t.Helper()
	total := 0.0
	for _, strategy := range strategies {
		score := strategy.Score(ctx, channel)
		debugScore, _ := strategy.ScoreWithDebug(ctx, channel)
		require.InDelta(t, score, debugScore, 0.000001, strategy.Name())
		total += score
	}
	return total
}

func TestHealthGateSelector_NewSessionPrefersAddedHigherWeightWithRealScores(t *testing.T) {
	for _, mode := range []struct {
		name  string
		debug bool
	}{
		{name: "production"},
		{name: "debug", debug: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			old := healthGateSelectorCandidate(10, 0, "gpt-4")
			old.Channel.OrderingWeight = 10
			oldMetrics := &biz.AggregatedMetrics{NonStreamingSampleCount: 1, NonStreamingLatencyEWMA: 10}
			oldMetrics.RequestCount = 1
			metrics := &healthGateWeightMetrics{mockMetricsProvider: mockMetricsProvider{
				metrics: map[int]*biz.AggregatedMetrics{old.Channel.ID: oldMetrics},
			}}
			previous := &fakePreviousChannelProvider{traceChannelIDs: map[int]int{}}
			selector, _, _ := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{old}, biz.TraceStickyPreferPreviousChannel, previous)
			selector.loadBalancer.strategies = healthGateWeightStrategies(metrics)
			selector.loadBalancer.selectionTracker = metrics
			selector.loadBalancer.debug = mode.debug
			req := &llm.Request{Model: "alias"}
			firstCtx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 100})

			initial, err := selector.Select(firstCtx, req)
			require.NoError(t, err)
			require.Len(t, initial, 1)
			require.Equal(t, old.Channel.ID, initial[0].Channel.ID)
			// Model a successful initial request retaining its existing owner.
			previous.traceChannelIDs[100] = old.Channel.ID

			added := healthGateSelectorCandidate(15, 0, "gpt-4")
			added.Channel.OrderingWeight = 15
			wrapped := selector.wrapped.(*staticChannelSelector)
			wrapped.candidates = append(wrapped.candidates, added)
			newCtx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 200})
			_, found, err := (healthGateLegacySessionProvider{previous}).GetSessionOwner(newCtx, 0, 200)
			require.NoError(t, err)
			require.False(t, found, "the added channel competes for a genuinely new session")
			_, hasMetrics := metrics.metrics[added.Channel.ID]
			require.False(t, hasMetrics, "the new weight-15 channel has no historical metrics")

			// Establish the actual regression trigger: the old score-first rule
			// prefers 10 even without an owner, because its latency is excellent.
			oldScore := healthGateWeightTotalScore(t, newCtx, selector.loadBalancer.strategies, old.Channel)
			newScore := healthGateWeightTotalScore(t, newCtx, selector.loadBalancer.strategies, added.Channel)
			require.Greater(t, oldScore, newScore)

			result, err := selector.Select(newCtx, req)
			require.NoError(t, err)
			require.Len(t, result, 2)
			require.Equal(t, added.Channel.ID, result[0].Channel.ID)
			require.Equal(t, old.Channel.ID, result[1].Channel.ID)
			require.False(t, result[0].TraceSticky)
			require.EqualValues(t, 1, metrics.metrics[added.Channel.ID].RequestCount)
			require.Zero(t, metrics.metrics[added.Channel.ID].NonStreamingSampleCount)

			sticky, err := selector.Select(firstCtx, req)
			require.NoError(t, err)
			require.Equal(t, old.Channel.ID, sticky[0].Channel.ID)
			require.True(t, sticky[0].TraceSticky, "existing owners are handled before weight tiers")
			require.EqualValues(t, 1, metrics.metrics[added.Channel.ID].RequestCount, "sticky fallback sorting must not track a selection")
		})
	}
}

func TestHealthGateLoadBalancer_EqualWeightUsesRealRoundRobinScores(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "debug"}[debug], func(t *testing.T) {
			low := healthGateSelectorCandidate(10, 0, "gpt-4")
			first := healthGateSelectorCandidate(15, 0, "gpt-4")
			second := healthGateSelectorCandidate(16, 0, "gpt-4")
			low.Channel.OrderingWeight = 10
			first.Channel.OrderingWeight = 15
			second.Channel.OrderingWeight = 15
			metrics := &healthGateWeightMetrics{mockMetricsProvider: mockMetricsProvider{
				metrics: map[int]*biz.AggregatedMetrics{
					low.Channel.ID: {NonStreamingSampleCount: 1, NonStreamingLatencyEWMA: 10},
				},
			}}
			policy := &mockRetryPolicyProvider{policy: &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 2}}
			lb := NewLoadBalancer(policy, metrics, healthGateWeightStrategies(metrics)...).WithHealthGate(biz.NewHealthGate(nil))
			lb.debug = debug
			counts := map[int]int{}
			previousID := 0
			for range 6 {
				result := lb.Sort(context.Background(), []*ChannelModelsCandidate{low, first, second}, "alias", false)
				require.Len(t, result, 3)
				id := result[0].Channel.ID
				require.NotEqual(t, low.Channel.ID, id, "excellent lower-tier latency cannot override the explicit tier")
				require.NotEqual(t, previousID, id, "equal-weight candidates must continue rotating by real WRR score")
				counts[id]++
				previousID = id
			}
			require.Equal(t, 3, counts[first.Channel.ID])
			require.Equal(t, 3, counts[second.Channel.ID])
		})
	}
}

func TestHealthGateSelector_AssociationPriorityPrecedesWeightTiers(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "debug"}[debug], func(t *testing.T) {
			now := time.Unix(1000, 0)
			preferred := healthGateSelectorCandidate(10, -1, "gpt-4")
			preferred.Channel.OrderingWeight = 10
			fallback := healthGateSelectorCandidate(15, 0, "gpt-4")
			fallback.Channel.OrderingWeight = 15
			metrics := &mockMetricsProvider{}
			selector, _, _ := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{fallback, preferred}, biz.TraceStickyDisabled, nil)
			selector.loadBalancer.strategies = healthGateWeightStrategies(metrics)
			selector.loadBalancer.debug = debug

			result, err := selector.Select(context.Background(), &llm.Request{Model: "alias"})
			require.NoError(t, err)
			require.Len(t, result, 2)
			require.Equal(t, preferred.Channel.ID, result[0].Channel.ID)
			require.Equal(t, fallback.Channel.ID, result[1].Channel.ID)
		})
	}
}

func TestHealthGateLoadBalancer_WeightTiersRespectHardLimitsAndQuota(t *testing.T) {
	for _, debug := range []bool{false, true} {
		for _, scenario := range []string{"rpm_exhausted", "cooldown", "quota_exhausted", "quota_warning_higher_weight", "quota_warning_equal_weight"} {
			t.Run(map[bool]string{false: "production", true: "debug"}[debug]+"/"+scenario, func(t *testing.T) {
				low := healthGateSelectorCandidate(10, 0, "gpt-4")
				high := healthGateSelectorCandidate(15, 0, "gpt-4")
				low.Channel.OrderingWeight = 10
				high.Channel.OrderingWeight = 15
				tracker := NewChannelRequestTracker()
				quota := &mockQuotaStatusProvider{statuses: map[int]*biz.QuotaChannelStatus{}}
				settings := &mockQuotaEnforcementSettingsProvider{settings: &biz.QuotaEnforcementSettings{
					Enabled: true, Mode: biz.QuotaEnforcementModeDePrioritize,
				}}
				expectedID := low.Channel.ID
				hard := true
				switch scenario {
				case "rpm_exhausted":
					rpm := int64(1)
					high.Channel.Settings = &objects.ChannelSettings{RateLimit: &objects.ChannelRateLimit{RPM: &rpm}}
					tracker.TryAcquireRequest(high.Channel.ID, rpm)
				case "cooldown":
					tracker.SetCooldown(high.Channel.ID, time.Now().Add(time.Hour))
				case "quota_exhausted":
					quota.statuses[high.Channel.ID] = &biz.QuotaChannelStatus{Status: providerquotastatus.StatusExhausted}
				case "quota_warning_higher_weight", "quota_warning_equal_weight":
					hard = false
					quota.statuses[high.Channel.ID] = &biz.QuotaChannelStatus{Status: providerquotastatus.StatusWarning, Ready: true}
					if scenario == "quota_warning_higher_weight" {
						expectedID = high.Channel.ID
					} else {
						high.Channel.OrderingWeight = low.Channel.OrderingWeight
					}
				}
				metrics := &mockMetricsProvider{}
				strategies := []LoadBalanceStrategy{
					NewWeightRoundRobinStrategy(metrics), NewLatencyAwareStrategy(metrics),
					NewRateLimitAwareStrategy(tracker, nil), NewQuotaAwareStrategy(quota, settings),
				}
				ctx := context.Background()
				highScore := healthGateWeightTotalScore(t, ctx, strategies, high.Channel)
				lowScore := healthGateWeightTotalScore(t, ctx, strategies, low.Channel)
				require.Equal(t, hard, isHardUnavailableScore(highScore))
				require.False(t, isHardUnavailableScore(lowScore))
				require.Greater(t, lowScore, highScore)
				for _, retries := range []int{0, 1} {
					policy := &mockRetryPolicyProvider{policy: &biz.RetryPolicy{Enabled: true, MaxChannelRetries: retries}}
					lb := NewLoadBalancer(policy, nil, strategies...).WithHealthGate(biz.NewHealthGate(nil))
					lb.debug = debug
					result := lb.Sort(ctx, []*ChannelModelsCandidate{high, low}, "alias", false)
					require.Len(t, result, 1+retries)
					require.Equal(t, expectedID, result[0].Channel.ID)
				}
			})
		}
	}
}

func TestHealthGateLoadBalancer_AllStrategiesRespectWeightTiers(t *testing.T) {
	for _, debug := range []bool{false, true} {
		for _, strategy := range []string{biz.LoadBalancerStrategyAdaptive, biz.LoadBalancerStrategyFailover, biz.LoadBalancerStrategyCircuitBreaker, biz.LoadBalancerStrategyRoundRobin} {
			t.Run(map[bool]string{false: "production", true: "debug"}[debug]+"/"+strategy, func(t *testing.T) {
				old := healthGateSelectorCandidate(10, 0, "gpt-4")
				added := healthGateSelectorCandidate(15, 0, "gpt-4")
				old.Channel.OrderingWeight = 10
				added.Channel.OrderingWeight = 15
				oldMetrics := &biz.AggregatedMetrics{NonStreamingSampleCount: 1, NonStreamingLatencyEWMA: 10}
				oldMetrics.RequestCount = 1
				metrics := &mockMetricsProvider{metrics: map[int]*biz.AggregatedMetrics{old.Channel.ID: oldMetrics}}
				policy := &mockRetryPolicyProvider{policy: &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 1, LoadBalancerStrategy: strategy}}
				rateLimit := NewRateLimitAwareStrategy(NewChannelRequestTracker(), nil)
				quota := NewQuotaAwareStrategy(nil, &mockQuotaEnforcementSettingsProvider{})
				var lb *LoadBalancer
				expectedID := added.Channel.ID
				switch strategy {
				case biz.LoadBalancerStrategyAdaptive:
					lb = NewLoadBalancer(policy, nil, NewErrorAwareStrategy(metrics), NewWeightRoundRobinStrategy(metrics), NewLatencyAwareStrategy(metrics), rateLimit, quota)
				case biz.LoadBalancerStrategyFailover:
					lb = NewLoadBalancer(policy, nil, NewWeightStrategy(), NewRandomStrategy(), rateLimit, quota)
				case biz.LoadBalancerStrategyCircuitBreaker:
					lb = NewLoadBalancer(policy, nil, NewWeightStrategy(), NewModelAwareCircuitBreakerStrategy(biz.NewModelCircuitBreaker()), rateLimit, quota)
				case biz.LoadBalancerStrategyRoundRobin:
					// Round robin applies within a weight tier, like the other strategies.
					oldMetrics.RequestCount = 0
					lb = NewLoadBalancer(policy, nil, NewRoundRobinStrategy(metrics), rateLimit, quota).
						WithStableTieOrder().WithRoundRobinHealthFilter(NewRoundRobinHealthStrategy(metrics))
				}
				lb.debug = debug
				require.Nil(t, lb.healthGate)
				result := lb.Sort(context.Background(), []*ChannelModelsCandidate{old, added}, "alias", false)
				require.Len(t, result, 2)
				require.Equal(t, expectedID, result[0].Channel.ID)
			})
		}
	}
}
