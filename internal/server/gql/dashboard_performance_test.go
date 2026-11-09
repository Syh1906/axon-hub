package gql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

func TestChannelProbeSpeedVersionsDoNotMix(t *testing.T) {
	r, ctx := newDashboardUsageTestResolver(t)
	ch := createDashboardUsageChannel(t, ctx, r.client, "speed-version")
	at := time.Now().UTC().Truncate(24 * time.Hour).Add(time.Hour)
	r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(100).
		SetSuccessRequestCount(100).SetFirstTokenSampleCount(100).SetAvgTokensPerSecond(389).SetAvgTimeToFirstTokenMs(1000).
		SetTimestamp(at.Unix()).SaveX(ctx)
	rows, err := r.queryChannelProbeStats(ctx, at.Add(-time.Minute).Unix(), "UTC", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Zero(t, rows[0].Throughput, "the response maps unavailable legacy speed to nil")
	require.Equal(t, 100, rows[0].RequestCount)
	require.Equal(t, 1000.0, *rows[0].TTFTMs)
	r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(1).
		SetSuccessRequestCount(1).SetSpeedSampleCount(1).SetFirstTokenSampleCount(1).SetAvgTokensPerSecond(36).SetAvgTimeToFirstTokenMs(1000).
		SetSpeedVersion(usageanalytics.OutputSpeedVersion).SetTimestamp(at.Add(time.Minute).Unix()).SaveX(ctx)
	rows, err = r.queryChannelProbeStats(ctx, at.Add(-time.Minute).Unix(), "UTC", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 36.0, rows[0].Throughput)
	require.Equal(t, 101, rows[0].RequestCount)
	require.Equal(t, 1000.0, *rows[0].TTFTMs)
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
	require.Equal(t, 2, channelStats[0].RequestCount, "the untimed failed attempt has no performance sample")
	require.Equal(t, 1, channelStats[0].SpeedSampleCount)
	require.Equal(t, 2, channelStats[0].FirstTokenSampleCount)
	require.NotNil(t, channelStats[0].Throughput)
}

func TestFetchChannelNamesPropagatesLookupErrors(t *testing.T) {
	resolver, ctx := newDashboardUsageTestResolver(t)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, err := resolver.fetchChannelNames(canceledCtx, []int{1})
	require.Error(t, err, "channel-name lookup failures must not be hidden behind fallback labels")
}

func TestProbeAverageUsesEligibleSampleCounts(t *testing.T) {
	r, ctx := newDashboardUsageTestResolver(t)
	ch := createDashboardUsageChannel(t, ctx, r.client, "weighted-samples")
	at := time.Now().UTC().Truncate(24 * time.Hour)
	r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(100).SetSuccessRequestCount(1).
		SetAvgTokensPerSecond(100).SetSpeedSampleCount(1).SetAvgTimeToFirstTokenMs(1000).SetFirstTokenSampleCount(1).
		SetSpeedVersion(usageanalytics.OutputSpeedVersion).SetTimestamp(at.Unix()).SaveX(ctx)
	r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(9).SetSuccessRequestCount(9).
		SetAvgTokensPerSecond(10).SetSpeedSampleCount(9).SetAvgTimeToFirstTokenMs(100).SetFirstTokenSampleCount(9).
		SetSpeedVersion(usageanalytics.OutputSpeedVersion).SetTimestamp(at.Add(time.Minute).Unix()).SaveX(ctx)
	rows, err := r.queryChannelProbeStats(ctx, at.Unix(), "UTC", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.InDelta(t, 19, rows[0].Throughput, 1e-8)
	require.InDelta(t, 190, *rows[0].TTFTMs, 1e-8)
	require.Equal(t, 109, rows[0].RequestCount)
	r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(10).SetSuccessRequestCount(10).
		SetAvgTimeToFirstTokenMs(0).SetFirstTokenSampleCount(10).
		SetSpeedVersion(usageanalytics.OutputSpeedVersion).SetTimestamp(at.Add(2 * time.Minute).Unix()).SaveX(ctx)
	rows, err = r.queryChannelProbeStats(ctx, at.Unix(), "UTC", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.InDelta(t, 95, *rows[0].TTFTMs, 1e-8, "zero-millisecond samples remain in the weighted denominator")
	require.InDelta(t, 19, rows[0].Throughput, 1e-8, "first-token samples do not change the speed denominator")
	require.Equal(t, 10, rows[0].SpeedSampleCount)
	require.Equal(t, 20, rows[0].FirstTokenSampleCount)
}

func TestDashboardPerformancePreservesZeroAndMissingFirstToken(t *testing.T) {
	for _, test := range []struct {
		name      string
		first     *int64
		withSpeed bool
	}{
		{"zero", new(int64(0)), true},
		{"unknown", nil, true},
		{"first_token_without_usage", new(int64(0)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, ctx := newDashboardUsageTestResolver(t)
			ch := createDashboardUsageChannel(t, ctx, r.client, test.name)
			req := r.client.Request.Create().SetModelID("model").SetChannelID(ch.ID).
				SetRequestBody([]byte(`{}`)).SetStatus(request.StatusCompleted).SetStream(true).SaveX(ctx)
			execution := r.client.RequestExecution.Create().SetRequestID(req.ID).SetChannelID(ch.ID).
				SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusCompleted).
				SetStream(true).SetFormat("openai/chat_completions").SetMetricsLatencyMs(2000).
				SetNillableMetricsFirstTokenLatencyMs(test.first)
			if test.withSpeed {
				execution.SetMetricsFirstByteLatencyMs(500)
				r.client.UsageLog.Create().SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("model").
					SetCompletionTokens(150).SetTotalTokens(150).SaveX(ctx)
			}
			execution.SaveX(ctx)
			check := func(speed, first *float64, speedCount, firstCount int) {
				t.Helper()
				if test.first == nil {
					require.Nil(t, first)
					require.Zero(t, firstCount)
				} else {
					require.NotNil(t, first)
					require.Zero(t, *first)
					require.Equal(t, 1, firstCount)
				}
				if test.withSpeed {
					require.NotNil(t, speed)
					require.Equal(t, 100.0, *speed)
					require.Equal(t, 1, speedCount)
				} else {
					require.Nil(t, speed)
					require.Zero(t, speedCount)
				}
			}
			models, err := r.modelPerformanceStats(ctx)
			require.NoError(t, err)
			require.Len(t, models, 1)
			require.Equal(t, 1, models[0].RequestCount)
			check(models[0].Throughput, models[0].TtftMs, models[0].SpeedSampleCount, models[0].FirstTokenSampleCount)
			channels, err := r.channelPerformanceStats(ctx)
			require.NoError(t, err)
			require.Len(t, channels, 1)
			require.Equal(t, 1, channels[0].RequestCount)
			check(channels[0].Throughput, channels[0].TtftMs, channels[0].SpeedSampleCount, channels[0].FirstTokenSampleCount)

			probe := r.client.ChannelProbe.Create().SetChannelID(ch.ID).SetTotalRequestCount(1).
				SetSuccessRequestCount(1).SetTimestamp(time.Now().Unix()).SetSpeedVersion(usageanalytics.OutputSpeedVersion)
			if test.first != nil {
				probe.SetAvgTimeToFirstTokenMs(0).SetFirstTokenSampleCount(1)
			}
			if test.withSpeed {
				probe.SetAvgTokensPerSecond(100).SetSpeedSampleCount(1)
			}
			probe.SaveX(ctx)
			channels, err = r.channelPerformanceStats(ctx)
			require.NoError(t, err)
			require.Len(t, channels, 1)
			check(channels[0].Throughput, channels[0].TtftMs, channels[0].SpeedSampleCount, channels[0].FirstTokenSampleCount)
		})
	}
}

func TestChannelPerformanceUsesEveryMeasuredAttemptWithoutProbes(t *testing.T) {
	r, ctx := newDashboardUsageTestResolver(t)
	ch := createDashboardUsageChannel(t, ctx, r.client, "retries")
	req := r.client.Request.Create().SetModelID("model").SetChannelID(ch.ID).
		SetRequestBody([]byte(`{}`)).SetStatus(request.StatusCompleted).SetStream(true).SaveX(ctx)
	for i, tokens := range []int64{100, 500} {
		status := requestexecution.StatusFailed
		if i == 1 {
			status = requestexecution.StatusCompleted
		}
		exec := r.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(req.ProjectID).SetChannelID(ch.ID).
			SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus(status).SetStream(true).
			SetFormat("openai/chat_completions").SetMetricsLatencyMs(1200).
			SetMetricsFirstByteLatencyMs(200).SetMetricsFirstTokenLatencyMs(int64(100 + i*200)).SaveX(ctx)
		usage := r.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(req.ProjectID).SetChannelID(ch.ID).
			SetModelID("model").SetCompletionTokens(tokens).SetTotalTokens(tokens).SaveX(ctx)
		r.client.BudgetCharge.Create().SetExecutionID(exec.ID).SetUsageLogID(usage.ID).SetProjectID(req.ProjectID).
			SetRequestID(req.ID).SetKeyID(1).SetKeyAccountID(1).SetEstimate(0).
			SetKeyVersions(map[budget.Period]int64{}).SetGroupVersions(map[budget.Period]int64{}).SetCheckAfter(time.Now()).SaveX(ctx)
	}
	channels, err := r.channelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.Equal(t, 300.0, *channels[0].Throughput)
	require.Equal(t, 200.0, *channels[0].TtftMs)
	require.Equal(t, 2, channels[0].SpeedSampleCount)
	require.Equal(t, 2, channels[0].FirstTokenSampleCount)
	models, err := r.modelPerformanceStats(ctx)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, 500.0, *models[0].Throughput)
	require.Equal(t, 1, models[0].SpeedSampleCount)
}
