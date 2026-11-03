package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
)

func channelWithRPM(id int, name string, rpm int64) *biz.Channel {
	rl := &objects.ChannelRateLimit{}
	if rpm > 0 {
		rl.RPM = lo.ToPtr(rpm)
	}

	return &biz.Channel{
		Channel: &ent.Channel{
			ID:   id,
			Name: name,
			Settings: &objects.ChannelSettings{
				RateLimit: rl,
			},
		},
	}
}

func TestRateLimitAdmission_AllowsOnlyConfiguredRPM(t *testing.T) {
	tracker := NewChannelRequestTracker()
	channel := channelWithRPM(1, "strict-rpm", 2)
	outbound := newTestOutbound(channel)
	middleware := withRateLimitAdmission(outbound, tracker).(*rateLimitAdmissionMiddleware)

	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.NoError(t, err)

	_, err = middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.NoError(t, err)

	_, err = middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.Error(t, err)

	var rpmErr *LocalRPMExhaustedError
	require.ErrorAs(t, err, &rpmErr)
	assert.Equal(t, channel.ID, rpmErr.ChannelID)
	assert.Equal(t, int64(2), rpmErr.Limit)
	assert.Equal(t, int64(2), tracker.GetRequestCount(channel.ID))
}

func TestRateLimitAdmission_NoRPMTracksUsage(t *testing.T) {
	tracker := NewChannelRequestTracker()
	channel := channelWithRPM(2, "no-rpm", 0)
	outbound := newTestOutbound(channel)
	middleware := withRateLimitAdmission(outbound, tracker).(*rateLimitAdmissionMiddleware)

	for range 3 {
		_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
		require.NoError(t, err)
	}

	assert.Equal(t, int64(3), tracker.GetRequestCount(channel.ID))
}

func TestRateLimitAdmission_NoSettingsTracksUsage(t *testing.T) {
	tracker := NewChannelRequestTracker()
	channel := &biz.Channel{Channel: &ent.Channel{ID: 6}}
	middleware := withRateLimitAdmission(newTestOutbound(channel), tracker)
	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), tracker.GetRequestCount(channel.ID))
}

func TestRateLimitAdmission_SharedChannelUsageAndRejection(t *testing.T) {
	manager := NewChannelLimiterManager()
	channel := channelWithRPM(7, "shared", 1)
	for i := range 2 {
		outbound := newTestOutbound(channel)
		concurrency := withChannelLimiter(outbound, manager, nil)
		rpm := withRateLimitAdmission(outbound, manager.requestTracker)
		_, err := concurrency.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
		require.NoError(t, err)
		_, err = rpm.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
		if i == 0 {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, ErrLocalRPMExhausted)
		}
		concurrency.OnOutboundRawError(t.Context(), errors.New("attempt ended"))
	}

	inFlight, count := manager.Usage(channel.ID)
	assert.Zero(t, inFlight)
	assert.Equal(t, int64(1), count)
}

func TestRateLimitAdmission_SameChannelRetryCannotBypassRPM(t *testing.T) {
	tracker := NewChannelRequestTracker()
	channel := channelWithRPM(5, "retry-rpm", 1)
	outbound := newTestOutbound(channel)
	middleware := withRateLimitAdmission(outbound, tracker).(*rateLimitAdmissionMiddleware)

	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.NoError(t, err)

	_, err = middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLocalRPMExhausted)
	assert.Equal(t, int64(1), tracker.GetRequestCount(channel.ID))
}

func TestRateLimitTracking_OnOutboundRawError_LocalRPMExhaustedIgnored(t *testing.T) {
	tracker := NewChannelRequestTracker()
	channel := channelWithRPM(3, "strict-rpm", 1)
	outbound := newTestOutbound(channel)
	middleware := &rateLimitTracking{
		outbound: outbound,
		tracker:  tracker,
	}

	middleware.OnOutboundRawError(context.Background(), newLocalRPMExhaustedError(channel, 1))

	assert.False(t, tracker.IsCoolingDown(channel.ID),
		"local RPM rejection must not trigger upstream-style cooldown")
}

func TestPersistentOutboundTransformer_CanRetry_LocalRPMExhausted(t *testing.T) {
	channel := channelWithRPM(4, "strict-rpm", 1)
	outbound := newTestOutbound(channel)
	err := newLocalRPMExhaustedError(channel, 1)

	assert.False(t, outbound.CanRetry(err))
	assert.True(t, errors.Is(err, ErrLocalRPMExhausted))
}

func TestRateLimitAdmission_RespectsUpstreamCooldown(t *testing.T) {
	tracker := NewChannelRequestTracker()
	channel := channelWithRPM(8, "cooling-channel", 0)
	outbound := newTestOutbound(channel)
	until := time.Now().Add(30 * time.Second)
	tracker.SetCooldown(channel.ID, until)
	middleware := withRateLimitAdmission(outbound, tracker)

	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.Error(t, err, "冷却中的唯一候选也不能继续向上游发送")
	var httpErr *httpclient.Error
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
	require.Equal(t, "30", httpErr.Headers.Get("Retry-After"))
	require.Zero(t, tracker.GetRequestCount(channel.ID))
	require.False(t, outbound.CanRetry(err))
	require.Equal(t, biz.HealthGateOutcomeNeutral, classifyHealthGateOutcome(t.Context(), err))

	withRateLimitTracking(outbound, tracker).OnOutboundRawError(t.Context(), err)
	currentUntil, cooling := tracker.GetCooldownUntil(channel.ID)
	require.True(t, cooling)
	require.Equal(t, until, currentUntil, "本地冷却拒绝不能延长上游指定的恢复时间")
	info := extractExecutionErrorInfo(err, "")
	require.Equal(t, "gateway", info.Failure.Source)
	require.Nil(t, info.StatusCode, "没有发到上游，不能记录上游HTTP响应码")
}

func TestRateLimitAdmission_ExpiredCooldownAllowsRequest(t *testing.T) {
	tracker := NewChannelRequestTracker()
	channel := channelWithRPM(8, "recovered-channel", 0)
	tracker.SetCooldown(channel.ID, time.Now().Add(-time.Second))
	middleware := withRateLimitAdmission(newTestOutbound(channel), tracker)
	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{})
	require.NoError(t, err)
	require.EqualValues(t, 1, tracker.GetRequestCount(channel.ID))
}
