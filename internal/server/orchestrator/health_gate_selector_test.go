package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

type healthGateLegacySessionProvider struct {
	PreviousChannelProvider
}

func (p healthGateLegacySessionProvider) GetSessionOwner(ctx context.Context, threadID, traceID int) (biz.SessionOwner, bool, error) {
	id := 0
	var err error
	if threadID != 0 {
		id, err = p.GetPreviousChannelIDByThread(ctx, threadID)
	} else if traceID != 0 {
		id, err = p.GetPreviousChannelID(ctx, traceID)
	}
	return biz.SessionOwner{ChannelID: id}, id != 0, err
}

func healthGateResolver(cfg biz.HealthGateConfig) biz.HealthGateConfigResolver {
	return func() (biz.HealthGateConfig, bool) { return cfg, true }
}

func healthGateSelectorFixture(now *time.Time, candidates []*ChannelModelsCandidate, stickyMode biz.TraceStickyMode, previous PreviousChannelProvider) (*LoadBalancedSelector, *biz.HealthGate, *biz.RetryPolicy) {
	gate := biz.NewHealthGate(func() time.Time { return *now })
	policy := &biz.RetryPolicy{
		Enabled: true, MaxChannelRetries: 2, LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated,
		TraceStickyMode: stickyMode, HealthGate: &biz.HealthGatePolicy{FailureThreshold: 1, OpenDurationSeconds: 10, MaxOpenDurationSeconds: 60, ProbeSuccessThreshold: 2, UnstableWindowSeconds: 30},
	}
	provider := &mockRetryPolicyProvider{policy: policy}
	if previous != nil {
		previous = healthGateLegacySessionProvider{previous}
	}
	selector := newLivePolicyTestSelector(&staticChannelSelector{candidates: candidates}, NewLoadBalancer(nil, nil).WithHealthGate(gate), provider, previous)
	return selector, gate, policy
}

func openHealthGateModel(t *testing.T, gate *biz.HealthGate, policy *biz.RetryPolicy, candidate *ChannelModelsCandidate, model string) {
	t.Helper()
	key := biz.HealthGateKey{ChannelID: candidate.Channel.ID, ActualModel: model}
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), candidate.Channel)
	ticket, ok := healthGateBegin(t, gate, key, healthGateResolver(cfg), true, false)
	require.True(t, ok)
	healthGateFinish(t, gate, ticket, healthGateResolver(cfg), biz.HealthGateOutcomeFailure, biz.HealthGateErrorInfo{StatusCode: 500})
}

func healthGateSelectorCandidate(channelID, priority int, models ...string) *ChannelModelsCandidate {
	candidate := stickyTestCandidate(channelID, priority)
	candidate.Models = make([]biz.ChannelModelEntry, 0, len(models))
	candidate.modelAPIFormats = make([]string, 0, len(models))
	for _, model := range models {
		candidate.Models = append(candidate.Models, biz.ChannelModelEntry{RequestModel: "alias", ActualModel: model})
		candidate.modelAPIFormats = append(candidate.modelAPIFormats, "format-"+model)
	}
	candidate.APIFormat = candidate.modelAPIFormats[0]
	return candidate
}

func TestHealthGateSelector_FiltersModelsWithoutMutatingCandidates(t *testing.T) {
	now := time.Unix(1000, 0)
	first := healthGateSelectorCandidate(1, 0, "broken", "working")
	second := healthGateSelectorCandidate(2, 1, "broken")
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{first, second}, biz.TraceStickyDisabled, nil)
	openHealthGateModel(t, gate, policy, first, "broken")
	openHealthGateModel(t, gate, policy, second, "broken")

	result, err := selector.Select(context.Background(), &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "working", result[0].Models[0].ActualModel)
	require.Equal(t, []string{"format-working"}, result[0].modelAPIFormats)
	require.Equal(t, "format-working", result[0].APIFormat)
	require.Len(t, first.Models, 2)
	require.Equal(t, []string{"format-broken", "format-working"}, first.modelAPIFormats)
	require.Equal(t, "format-broken", first.APIFormat)
	require.NotSame(t, first, result[0])
}

func TestHealthGateSelector_StickyGateLeavesCacheUnchanged(t *testing.T) {
	now := time.Unix(1000, 0)
	first := healthGateSelectorCandidate(1, 0, "broken")
	second := healthGateSelectorCandidate(2, 1, "working")
	previous := &fakePreviousChannelProvider{traceChannelIDs: map[int]int{10: 1}, threadChannelIDs: map[int]int{20: 1}}
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{first, second}, biz.TraceStickyPreferPreviousChannel, previous)
	openHealthGateModel(t, gate, policy, first, "broken")
	ctx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 10, ThreadID: 20})

	result, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, 2, result[0].Channel.ID)
	require.False(t, result[0].TraceSticky)
	require.Equal(t, 1, previous.traceChannelIDs[10])
	require.Equal(t, 1, previous.threadChannelIDs[20])
}

func TestHealthGateSelector_ProbeEligibilityAndBusyFallback(t *testing.T) {
	now := time.Unix(1000, 0)
	candidate := healthGateSelectorCandidate(1, 0, "broken")
	previous := &fakePreviousChannelProvider{traceChannelIDs: map[int]int{10: 0}, threadChannelIDs: map[int]int{20: 0}}
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{candidate}, biz.TraceStickyPreferPreviousChannel, previous)
	openHealthGateModel(t, gate, policy, candidate, "broken")
	now = now.Add(11 * time.Second)
	ctx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 10, ThreadID: 20})
	result, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.False(t, result[0].healthGate.lastResort)
	require.True(t, result[0].healthGate.probeEligible)

	previous.threadChannelIDs[20] = 9
	result, err = selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.True(t, result[0].healthGate.lastResort)
	previous.threadChannelIDs[20] = 0
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), candidate.Channel)
	key := biz.HealthGateKey{ChannelID: candidate.Channel.ID, ActualModel: "broken"}
	ticket, ok := healthGateBegin(t, gate, key, healthGateResolver(cfg), true, false)
	require.True(t, ok)
	result, err = selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.True(t, result[0].healthGate.lastResort)
	_, ok = healthGateBegin(t, gate, key, healthGateResolver(cfg), true, true)
	require.False(t, ok)
	healthGateFinish(t, gate, ticket, healthGateResolver(cfg), biz.HealthGateOutcomeNeutral, biz.HealthGateErrorInfo{})
}

type healthGateUnavailableStickyCache struct{}

func (healthGateUnavailableStickyCache) GetPreviousChannelID(context.Context, int) (int, error) {
	return 0, context.DeadlineExceeded
}

func (healthGateUnavailableStickyCache) GetPreviousChannelIDByThread(context.Context, int) (int, error) {
	return 0, context.DeadlineExceeded
}

func TestHealthGateSelector_StickyReadErrorPreventsNormalProbe(t *testing.T) {
	now := time.Unix(1000, 0)
	candidate := healthGateSelectorCandidate(1, 0, "model")
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{candidate}, biz.TraceStickyPreferPreviousChannel, healthGateUnavailableStickyCache{})
	openHealthGateModel(t, gate, policy, candidate, "model")
	now = now.Add(11 * time.Second)
	ctx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 10})
	result, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.True(t, result[0].healthGate.lastResort)
}

func TestHealthGateSelector_BusyProbeUsesHealthyCandidate(t *testing.T) {
	now := time.Unix(1000, 0)
	probing := healthGateSelectorCandidate(1, 0, "probing")
	healthy := healthGateSelectorCandidate(2, 1, "healthy")
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{probing, healthy}, biz.TraceStickyDisabled, nil)
	openHealthGateModel(t, gate, policy, probing, "probing")
	now = now.Add(11 * time.Second)
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), probing.Channel)
	ticket, ok := healthGateBegin(t, gate, biz.HealthGateKey{ChannelID: probing.Channel.ID, ActualModel: "probing"}, healthGateResolver(cfg), true, false)
	require.True(t, ok)
	result, err := selector.Select(context.Background(), &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, healthy.Channel.ID, result[0].Channel.ID)
	require.False(t, result[0].healthGate.lastResort)
	healthGateFinish(t, gate, ticket, healthGateResolver(cfg), biz.HealthGateOutcomeNeutral, biz.HealthGateErrorInfo{})
}

func TestHealthGateSelector_LastResortPrefersProbingThenEarliestExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	first := healthGateSelectorCandidate(1, 0, "a")
	second := healthGateSelectorCandidate(2, 0, "b")
	third := healthGateSelectorCandidate(3, 0, "c")
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{first, second, third}, biz.TraceStickyPreferPreviousChannel, &fakePreviousChannelProvider{traceChannelIDs: map[int]int{10: 99}})
	openHealthGateModel(t, gate, policy, first, "a")
	now = now.Add(2 * time.Second)
	openHealthGateModel(t, gate, policy, second, "b")
	now = now.Add(2 * time.Second)
	openHealthGateModel(t, gate, policy, third, "c")
	ctx := contexts.WithTrace(context.Background(), &ent.Trace{ID: 10})
	result, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, 1, result[0].Channel.ID)
	require.True(t, result[0].healthGate.lastResort)
	require.False(t, result[0].TraceSticky)

	now = now.Add(7 * time.Second) // a has expired, b and c have not.
	result, err = selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Equal(t, 1, result[0].Channel.ID)
	require.True(t, result[0].healthGate.lastResort)
}

func TestHealthGateSelector_ProbeTakenAfterSelectionReturnsGeneric503(t *testing.T) {
	now := time.Unix(1000, 0)
	candidate := healthGateSelectorCandidate(1, 0, "model")
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{candidate}, biz.TraceStickyDisabled, nil)
	openHealthGateModel(t, gate, policy, candidate, "model")
	now = now.Add(11 * time.Second)
	ctx := context.Background()
	first, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	second, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.False(t, first[0].healthGate.lastResort)
	require.False(t, second[0].healthGate.lastResort)
	newTracker := func(candidates []*ChannelModelsCandidate) *healthGateAttemptTracker {
		state := &PersistenceState{
			RoutingPolicy:    EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated},
			CurrentCandidate: candidates[0], ChannelModelsCandidates: candidates,
		}
		return withHealthGate(&PersistentOutboundTransformer{state: state}, gate, policy.HealthGateOrDefault())
	}
	owner := newTracker(first)
	_, err = owner.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	contender := newTracker(second)
	_, err = contender.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.ErrorIs(t, err, errSkipCandidateByHealthGate)
	var unavailable *llm.ResponseError
	require.ErrorAs(t, contender.finalize(ctx, fmt.Errorf("pipeline: %w", err), false), &unavailable)
	require.Equal(t, 503, unavailable.StatusCode)
	require.Equal(t, "service_unavailable", unavailable.Detail.Code)
	view := healthGateInspect(t, gate, biz.HealthGateKey{ChannelID: 1, ActualModel: "model"}, healthGateResolver(biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), candidate.Channel)))
	require.True(t, view.ProbeBusy)
	require.NoError(t, owner.finalize(ctx, nil, false))
}

func TestHealthGateSelector_ProbeRaceSwitchesToHealthyFallback(t *testing.T) {
	now := time.Unix(1000, 0)
	probe := healthGateSelectorCandidate(1, 0, "probe")
	fallback := healthGateSelectorCandidate(2, 1, "healthy")
	selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{probe, fallback}, biz.TraceStickyDisabled, nil)
	openHealthGateModel(t, gate, policy, probe, "probe")
	now = now.Add(11 * time.Second)
	ctx := context.Background()
	first, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	second, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Equal(t, probe.Channel.ID, second[0].Channel.ID)
	firstState := &PersistenceState{RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated}, CurrentCandidate: first[0]}
	owner := withHealthGate(&PersistentOutboundTransformer{state: firstState}, gate, policy.HealthGateOrDefault())
	_, err = owner.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	state := &PersistenceState{RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated}, ChannelModelsCandidates: second, CurrentCandidate: second[0]}
	outbound := &PersistentOutboundTransformer{state: state}
	contender := withHealthGate(outbound, gate, policy.HealthGateOrDefault())
	_, err = contender.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.ErrorIs(t, err, errSkipCandidateByHealthGate)
	require.False(t, outbound.CanRetry(err))
	require.True(t, outbound.HasMoreChannels())
	require.NoError(t, outbound.NextChannel(ctx))
	_, err = contender.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	require.NoError(t, contender.finalize(ctx, nil, false))
	require.NoError(t, owner.finalize(ctx, nil, false))
}

func TestHealthGateSelector_UsesCurrentChannelThreshold(t *testing.T) {
	_, client := setupTest(t)
	service := newTestChannelServiceForChannels(client)
	t.Cleanup(service.Stop)
	candidate := healthGateSelectorCandidate(1, 0, "model")
	policy := &biz.RetryPolicy{HealthGate: &biz.HealthGatePolicy{
		FailureThreshold: 1, OpenDurationSeconds: 300, MaxOpenDurationSeconds: 600,
		ProbeSuccessThreshold: 2, UnstableWindowSeconds: 300,
	}}
	provider := &mockRetryPolicyProvider{policy: policy}
	gate := biz.NewHealthGate(nil)
	openHealthGateModel(t, gate, policy, candidate, "model")
	disabled := 0
	updatedChannel := *candidate.Channel.Channel
	updatedChannel.Settings = &objects.ChannelSettings{HealthGateFailureThreshold: &disabled}
	service.SetEnabledChannelsForTest([]*biz.Channel{{Channel: &updatedChannel}})
	selector := newLivePolicyTestSelector(&staticChannelSelector{candidates: []*ChannelModelsCandidate{candidate}}, NewLoadBalancer(service, service).WithHealthGate(gate), provider, nil)
	ctx := context.Background()
	result, err := selector.Select(ctx, &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.False(t, result[0].healthGate.lastResort)
	state := &PersistenceState{
		RoutingPolicy:    EffectiveRoutingPolicy{LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated},
		CurrentCandidate: result[0], ChannelService: service, RetryPolicyProvider: provider,
	}
	tracker := withHealthGate(&PersistentOutboundTransformer{state: state}, gate, policy.HealthGateOrDefault())
	_, err = tracker.OnOutboundRawRequest(ctx, &httpclient.Request{})
	require.NoError(t, err)
	tracker.OnOutboundRawError(ctx, healthGateServerError())
	require.Error(t, tracker.finalize(ctx, healthGateServerError(), false))
	require.Empty(t, healthGateSnapshot(t, gate, candidate.Channel.ID, healthGateResolver(biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), &biz.Channel{Channel: &updatedChannel}))))
}

func newLivePolicyTestSelector(wrapped CandidateSelector, lb *LoadBalancer, provider RetryPolicyProvider, previous PreviousChannelProvider) *LoadBalancedSelector {
	selector := WithTraceStickyLoadBalancedSelector(wrapped, lb, provider.RetryPolicyOrDefault(context.Background()), previous)
	selector.policy = provider
	return selector
}
