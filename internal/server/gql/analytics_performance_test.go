package gql

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

// TestAnalyticsDimensionStatsIncludesPerformanceMetrics verifies that model rows
// preserve the fork text-stream speed contract without another usage scan.
func TestAnalyticsDimensionStatsIncludesPerformanceMetrics(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:analytics-performance?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	resolver := &queryResolver{&Resolver{
		client:        client,
		systemService: biz.NewSystemService(biz.SystemServiceParams{Ent: client}),
	}}

	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenaiFake).
		SetName("Performance channel").
		SetStatus(channel.StatusEnabled).
		SetSupportedModels([]string{"performance-model"}).
		SetDefaultTestModel("performance-model").
		SetCredentials(objects.ChannelCredentials{}).
		Save(ctx)
	require.NoError(t, err)

	for _, sample := range []struct {
		completionTokens int64
		reasoningTokens  int64
		audioTokens      int64
		latencyMs        int64
		firstTokenMs     int64
		firstByteMs      int64
	}{
		// Audio is excluded; first-byte and first-token wait are different measurements.
		{completionTokens: 100, reasoningTokens: 20, audioTokens: 30, latencyMs: 2500, firstTokenMs: 500, firstByteMs: 500},
		{completionTokens: 100, latencyMs: 2500, firstTokenMs: 1500, firstByteMs: 500},
		// 首字节与结束同刻的缓冲样本缺少输出窗口，不纳入速度。
		{completionTokens: 50, latencyMs: 2500, firstTokenMs: 2500, firstByteMs: 2500},
	} {
		req, createErr := client.Request.Create().
			SetModelID("performance-model").
			SetRequestBody(objects.JSONRawMessage(`{}`)).
			SetStatus(request.StatusCompleted).
			SetChannelID(ch.ID).
			SetStream(true).
			Save(ctx)
		require.NoError(t, createErr)

		_, createErr = client.RequestExecution.Create().
			SetRequestID(req.ID).
			SetChannelID(ch.ID).
			SetModelID("performance-model").
			SetRequestBody(objects.JSONRawMessage(`{}`)).
			SetStatus(requestexecution.StatusCompleted).
			SetStream(true).
			SetMetricsLatencyMs(sample.latencyMs).
			SetMetricsFirstTokenLatencyMs(sample.firstTokenMs).
			SetMetricsFirstByteLatencyMs(sample.firstByteMs).
			Save(ctx)
		require.NoError(t, createErr)

		_, createErr = client.UsageLog.Create().
			SetRequestID(req.ID).
			SetChannelID(ch.ID).
			SetModelID("performance-model").
			SetPromptTokens(10).
			SetCompletionTokens(sample.completionTokens).
			SetCompletionReasoningTokens(sample.reasoningTokens).
			SetCompletionAudioTokens(sample.audioTokens).
			SetTotalTokens(10 + sample.completionTokens).
			Save(ctx)
		require.NoError(t, createErr)
	}

	// Both text samples use their full 2.5-second duration; audio remains excluded.
	expectedTokensPerSecond := 100.0 / 2
	expectedTTFTMs := 1500.0

	scans := countUsageLogGroupedScans(client)
	stats, err := resolver.AnalyticsDimensionStats(ctx, nil, "model")
	require.Equal(t, 1, *scans)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, 3, stats[0].RequestCount)
	require.Equal(t, 250, stats[0].OutputTokens)
	require.NotNil(t, stats[0].TokensPerSecond)
	require.InDelta(t, expectedTokensPerSecond, *stats[0].TokensPerSecond, 0.001)
	require.NotNil(t, stats[0].TtftMs)
	require.InDelta(t, expectedTTFTMs, *stats[0].TtftMs, 0.001)

	channelStats, err := resolver.AnalyticsDimensionStats(ctx, nil, "channel")
	require.Equal(t, 2, *scans)
	require.NoError(t, err)
	require.Len(t, channelStats, 1)
	require.NotNil(t, channelStats[0].TokensPerSecond)
	require.InDelta(t, expectedTokensPerSecond, *channelStats[0].TokensPerSecond, 0.001)
	require.NotNil(t, channelStats[0].TtftMs)
	require.InDelta(t, expectedTTFTMs, *channelStats[0].TtftMs, 0.001)
}

func TestAnalyticsDimensionPerformanceKeepsUsageAndFinalExecution(t *testing.T) {
	for _, scenario := range []string{"failed", "canceled", "different_channel", "duplicate_usage", "missing_first_byte", "short_stream", "non_stream", "image", "audio"} {
		t.Run(scenario, func(t *testing.T) {
			resolver, ctx := newDashboardUsageTestResolver(t)
			client := resolver.client
			ch := createDashboardUsageChannel(t, ctx, client, "timing")
			other := createDashboardUsageChannel(t, ctx, client, "other")
			req := client.Request.Create().SetModelID("timing-model").SetRequestBody([]byte(`{}`)).SetStatus(request.StatusCompleted).SetChannelID(ch.ID).SaveX(ctx)
			client.RequestExecution.Create().SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("timing-model").SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusCompleted).SetStream(true).SetMetricsLatencyMs(1100).SetMetricsFirstByteLatencyMs(100).SaveX(ctx)
			final := client.RequestExecution.Create().SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("timing-model").SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusFailed).SetStream(true).SetMetricsLatencyMs(2100).SetMetricsFirstByteLatencyMs(100).SetMetricsFirstTokenLatencyMs(1500)
			switch scenario {
			case "canceled":
				final.SetStatus(requestexecution.StatusCanceled)
			case "different_channel":
				final.SetChannelID(other.ID)
			case "missing_first_byte":
				final.Mutation().ResetMetricsFirstByteLatencyMs()
			case "short_stream":
				final.SetMetricsLatencyMs(150)
			case "non_stream":
				final.SetStream(false)
			}
			final.SaveX(ctx)
			createUsage := func() {
				usage := client.UsageLog.Create().SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("timing-model").SetPromptTokens(20).SetCompletionTokens(200).SetTotalTokens(220).SetTotalCost(1)
				if scenario == "image" {
					usage.SetCostSnapshot(&objects.RequestCostSnapshot{Images: []objects.ImageOutput{{}}})
				}
				if scenario == "audio" {
					usage.SetCompletionAudioTokens(20)
				}
				usage.SaveX(ctx)
			}
			createUsage()
			count := 1
			if scenario == "duplicate_usage" {
				createUsage()
				count = 2
			}
			rows, err := resolver.AnalyticsDimensionStats(ctx, nil, "model")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, count, rows[0].RequestCount)
			require.Equal(t, 220*count, rows[0].TotalTokens)
			require.Equal(t, float64(count), rows[0].Cost)
			if scenario == "failed" || scenario == "canceled" {
				require.NotNil(t, rows[0].TokensPerSecond)
				require.InDelta(t, 100, *rows[0].TokensPerSecond, 1e-9)
				require.Equal(t, 1500.0, *rows[0].TtftMs, "保留真实已到达内容的首字样本")
			} else {
				require.Nil(t, rows[0].TokensPerSecond)
			}
		})
	}
}
