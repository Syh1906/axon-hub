package gql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

func TestChannelProbeSpeedVersionsDoNotMix(t *testing.T) {
	r, ctx := newDashboardUsageTestResolver(t)
	ch := createDashboardUsageChannel(t, ctx, r.client, "speed-version")
	at := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(100).
		SetSuccessRequestCount(100).SetAvgTokensPerSecond(389).SetAvgTimeToFirstTokenMs(1000).
		SetTimestamp(at.Unix()).SaveX(ctx)
	rows, err := r.queryChannelProbeStats(ctx, at.Add(-time.Minute).Unix(), "UTC", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Zero(t, rows[0].Throughput, "the response maps unavailable legacy speed to nil")
	require.Equal(t, 100, rows[0].RequestCount)
	require.Equal(t, 1000.0, rows[0].TTFTMs)
	r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(1).
		SetSuccessRequestCount(1).SetAvgTokensPerSecond(36).SetAvgTimeToFirstTokenMs(1000).
		SetSpeedVersion(usageanalytics.OutputSpeedVersion).SetTimestamp(at.Add(time.Minute).Unix()).SaveX(ctx)
	rows, err = r.queryChannelProbeStats(ctx, at.Add(-time.Minute).Unix(), "UTC", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 36.0, rows[0].Throughput)
	require.Equal(t, 101, rows[0].RequestCount)
	require.Equal(t, 1000.0, rows[0].TTFTMs)
}

// TestModelPerformanceStatsUsesRequestModel verifies that the daily
// performance aggregate groups by the client-requested model, not the channel
// model stored on the execution.
func TestModelPerformanceStatsUsesRequestModel(t *testing.T) {
	resolver, ctx := newDashboardUsageTestResolver(t)
	client := resolver.client

	ch := createDashboardUsageChannel(t, ctx, client, "channel-a")

	req, err := client.Request.Create().
		SetModelID("request-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(request.StatusCompleted).
		SetChannelID(ch.ID).
		SetStream(true).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetStream(true).
		SetFormat("openai/chat_completions").
		SetMetricsLatencyMs(2000).
		SetMetricsFirstByteLatencyMs(500).SetMetricsFirstTokenLatencyMs(500).
		Save(ctx)
	require.NoError(t, err)

	// A retry produces another completed execution for the same request. The
	// dashboard must count the request once, not once per execution.
	_, err = client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusFailed).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetStream(true).
		SetFormat("openai/chat_completions").
		SetMetricsLatencyMs(3000).
		SetMetricsFirstByteLatencyMs(600).SetMetricsFirstTokenLatencyMs(600).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.UsageLog.Create().
		SetRequestID(req.ID).
		SetChannelID(ch.ID).
		SetModelID("channel-model").
		SetPromptTokens(10).
		SetCompletionTokens(100).
		SetTotalTokens(110).
		Save(ctx)
	require.NoError(t, err)

	stats, err := resolver.ModelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, "request-model", stats[0].ModelID)
	require.Equal(t, 1, stats[0].RequestCount)

	channelStats, err := resolver.ChannelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Len(t, channelStats, 1)
	require.Equal(t, "channel-a", channelStats[0].ChannelName)
	require.Equal(t, 1, channelStats[0].RequestCount)
	require.NotNil(t, channelStats[0].Throughput)
}

func TestFetchChannelNamesPropagatesLookupErrors(t *testing.T) {
	resolver, ctx := newDashboardUsageTestResolver(t)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, err := resolver.fetchChannelNames(canceledCtx, []int{1})
	require.Error(t, err, "channel-name lookup failures must not be hidden behind fallback labels")
}
