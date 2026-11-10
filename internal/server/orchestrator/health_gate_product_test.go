package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// Exercise real ownership storage together with the real weighted/latency
// scorers. A UI conversation with a new thread must not inherit another owner.
func TestHealthGateProductNewChannelAndSessionExpiry(t *testing.T) {
	service := biz.NewRequestService(nil, xcache.Config{Mode: xcache.ModeMemory}, nil, nil, nil, nil, biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir()}))
	old := healthGateSelectorCandidate(10, 0, "model")
	old.Channel.OrderingWeight = 10
	added := healthGateSelectorCandidate(15, 0, "model")
	added.Channel.OrderingWeight = 15
	metrics := &mockMetricsProvider{metrics: map[int]*biz.AggregatedMetrics{
		10: {NonStreamingSampleCount: 10, NonStreamingLatencyEWMA: 100},
		15: {},
	}}
	metrics.metrics[10].RequestCount = 1
	policy := &mockRetryPolicyProvider{policy: &biz.RetryPolicy{
		Enabled: true, MaxChannelRetries: 2, LoadBalancerStrategy: biz.LoadBalancerStrategyHealthGated,
		TraceStickyMode: biz.TraceStickyPreferPreviousChannel,
	}}
	base := &staticChannelSelector{candidates: []*ChannelModelsCandidate{old}}
	balancer := NewLoadBalancer(metrics, nil, NewWeightRoundRobinStrategy(), NewLatencyAwareStrategy()).WithHealthGate(biz.NewHealthGate(nil))
	selector := newLivePolicyTestSelector(base, balancer, policy, service)
	selectFor := func(threadID int) *ChannelModelsCandidate {
		t.Helper()
		ctx := contexts.WithThread(t.Context(), &ent.Thread{ID: threadID})
		candidates, err := selector.Select(ctx, &llm.Request{Model: "alias"})
		require.NoError(t, err)
		require.NotEmpty(t, candidates)
		return candidates[0]
	}
	storeOwner := func(threadID int, deadline time.Time) {
		t.Helper()
		service.UpdateSessionOwner(context.Background(), threadID, 0, func(biz.SessionOwner, bool) (biz.SessionOwner, bool) {
			return biz.SessionOwner{ChannelID: 10, ExpiresAt: deadline}, true
		})
	}

	require.Equal(t, 10, selectFor(1).Channel.ID)
	storeOwner(1, time.Now().Add(5*time.Minute))
	base.candidates = append(base.candidates, added)

	// No sleep or real provider is involved; explicit deadlines represent the
	// same request after an idle interval, including after the old 30-minute TTL.
	t.Run("new conversation uses new higher weight", func(t *testing.T) {
		chosen := selectFor(2)
		require.Equal(t, 15, chosen.Channel.ID)
		require.False(t, chosen.TraceSticky)
	})
	t.Run("valid protection keeps active owner", func(t *testing.T) {
		chosen := selectFor(1)
		require.Equal(t, 10, chosen.Channel.ID)
		require.True(t, chosen.TraceSticky)
	})
	for _, idle := range []time.Duration{6 * time.Minute, 31 * time.Minute} {
		t.Run("idle="+idle.String(), func(t *testing.T) {
			storeOwner(1, time.Now().Add(5*time.Minute-idle))
			chosen := selectFor(1)
			require.Equal(t, 15, chosen.Channel.ID)
			require.False(t, chosen.TraceSticky)
		})
	}
	t.Run("explicit hour cache protects longer", func(t *testing.T) {
		storeOwner(1, time.Now().Add(54*time.Minute))
		require.Equal(t, 10, selectFor(1).Channel.ID)
	})
}

type ownerTTLFinalizer struct {
	transformer.Outbound
}

func (ownerTTLFinalizer) FinalizeTransportRequest(request *httpclient.Request) *httpclient.Request {
	final := *request
	final.Body = []byte(`{"cache_control":{"type":"ephemeral","ttl":"1h"}}`)
	return &final
}

func TestHealthGateProtectionUsesFinalTransportRequest(t *testing.T) {
	request := &httpclient.Request{APIFormat: string(llm.APIFormatAnthropicMessage), Body: []byte(`{}`)}
	state := &PersistenceState{RawProviderRequest: request}
	outbound := &PersistentOutboundTransformer{state: state, wrapped: ownerTTLFinalizer{}}
	final, err := finalizeTransportRequest(outbound).OnOutboundRawRequest(t.Context(), request)
	require.NoError(t, err)
	require.NotSame(t, request, final)
	require.Same(t, final, state.RawProviderRequest)
	require.Equal(t, time.Hour, sessionOwnerProtectionTTL(state.RawProviderRequest))
}
