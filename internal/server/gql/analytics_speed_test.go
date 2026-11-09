package gql

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/gql/qb"
)

// Failed/canceled streams can retain measured output. Every speed consumer
// must use those same samples without changing success-rate or TTFT semantics.
func TestAnalyticsSpeedSamplesMatchRequestAndLeaderboard(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:analytics-speed?mode=memory&_fk=1")
	defer db.Close()
	ctx := authz.WithTestBypass(context.Background())
	project := db.Project.Create().SetName("Speed audit").SaveX(ctx)
	ch := db.Channel.Create().SetType("openai").SetName("Speed channel").SetSupportedModels([]string{"text-model"}).SetDefaultTestModel("text-model").SetCredentials(objects.ChannelCredentials{}).SaveX(ctx)
	ctx = contexts.WithUser(ctx, &ent.User{ID: 1, IsOwner: true})
	user := db.User.Create().SetEmail("dimension@example.invalid").SetPassword("fixture").SaveX(ctx)
	key := db.APIKey.Create().SetName("dimension").SetKey("fixture").SetProjectID(project.ID).SetUserID(user.ID).SaveX(ctx)
	at := time.Now().UTC().Add(-time.Hour)
	var ids []int
	for _, sample := range []struct {
		status     request.Status
		tokens     int64
		latency    int64
		firstToken int64
	}{
		{request.StatusCompleted, 100, 2000, 1500},
		{request.StatusFailed, 1500, 4000, 2500},
		{request.StatusCanceled, 300, 2000, 1800},
	} {
		req := db.Request.Create().SetProjectID(project.ID).SetChannelID(ch.ID).SetModelID("text-model").SetRequestBody([]byte(`{}`)).SetStatus(sample.status).SetStream(true).SetCreatedAt(at).SaveX(ctx)
		ids = append(ids, req.ID)
		db.RequestExecution.Create().SetProjectID(project.ID).SetChannelID(ch.ID).SetRequestID(req.ID).SetModelID("text-model").SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.Status(sample.status)).SetStream(true).SetMetricsLatencyMs(sample.latency).SetMetricsFirstByteLatencyMs(1000).SetMetricsFirstTokenLatencyMs(sample.firstToken).SetCreatedAt(at).SaveX(ctx)
		db.UsageLog.Create().SetProjectID(project.ID).SetChannelID(ch.ID).SetRequestID(req.ID).SetAPIKeyID(key.ID).SetModelID("text-model").SetCompletionTokens(sample.tokens).SetTotalTokens(sample.tokens).SetTotalCost(0).SaveX(ctx)
	}
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: db, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}})
	speeds, err := biz.RequestOutputSpeeds(ctx, db, ids)
	require.NoError(t, err)
	for i, want := range []float64{100, 500, 300} {
		require.NotNil(t, speeds[ids[i]])
		require.Equal(t, want, *speeds[ids[i]])
	}
	consumption := biz.NewConsumptionService(db, system, xcache.Config{Mode: xcache.ModeMemory})
	leaderboard, err := consumption.Leaderboard(ctx, biz.ConsumptionFilter{Dimension: biz.ConsumptionDimensionChannel, Metric: biz.ConsumptionMetricCost, Period: biz.ConsumptionPeriodAll})
	require.NoError(t, err)
	require.Len(t, leaderboard.Entries, 1)
	require.Equal(t, int64(3), leaderboard.Entries[0].Metrics.SpeedSampleCount)
	require.InDelta(t, 900.0/3, *leaderboard.Entries[0].Metrics.AvgTokensPerSecond, 1e-9)

	// All four new analytics dimensions must match the existing request/leaderboard speed.
	resolver := &queryResolver{&Resolver{client: db, systemService: system}}
	scans := countUsageLogGroupedScans(db)
	for i, dimension := range []string{"channel", "model", "apiKey", "user"} {
		stats, err := resolver.AnalyticsDimensionStats(ctx, nil, dimension)
		require.NoError(t, err)
		require.Len(t, stats, 1)
		require.NotNil(t, stats[0].TokensPerSecond)
		require.InDelta(t, 900.0/3, *stats[0].TokensPerSecond, 1e-9)
		require.Equal(t, 3, stats[0].RequestCount)
		require.Equal(t, i+1, *scans, "one usage aggregate per dimension")
	}

	handler := NewGraphqlHandlers(Dependencies{Ent: db, SystemService: system})
	graphqlClient := client.New(handler.Graphql, func(req *client.Request) { req.HTTP = req.HTTP.WithContext(ctx) })
	var logs struct {
		RequestLogs struct {
			Edges []struct {
				Node struct{ OutputTokensPerSecond *float64 }
			}
		}
	}
	err = graphqlClient.Post(`query { requestLogs(first: 3) { edges { node { outputTokensPerSecond } } } }`, &logs)
	require.NoError(t, err)
	require.Len(t, logs.RequestLogs.Edges, 3)
	var displayedSpeeds []float64
	for _, edge := range logs.RequestLogs.Edges {
		require.NotNil(t, edge.Node.OutputTokensPerSecond)
		displayedSpeeds = append(displayedSpeeds, *edge.Node.OutputTokensPerSecond)
	}
	require.ElementsMatch(t, []float64{100, 500, 300}, displayedSpeeds)
	var result struct{ AnalyticsModelStats []*AnalyticsModelStat }
	err = graphqlClient.Post(`query { analyticsModelStats { id requestCount successRate avgFirstTokenLatencyMs avgOutputTokensPerSecond speedSampleCount channels { requestCount successRate avgFirstTokenLatencyMs avgOutputTokensPerSecond speedSampleCount } } }`, &result)
	require.NoError(t, err)
	require.Len(t, result.AnalyticsModelStats, 1)
	model := result.AnalyticsModelStats[0]
	require.Equal(t, 3, model.SpeedSampleCount)
	require.InDelta(t, 900.0/3, *model.AvgOutputTokensPerSecond, 1e-9)
	require.Equal(t, 2, model.RequestCount)
	require.Equal(t, 50.0, model.SuccessRate)
	require.InDelta(t, 5800.0/3, *model.AvgFirstTokenLatencyMs, 1e-9)
	require.Len(t, model.Channels, 1)
	require.Equal(t, 3, model.Channels[0].SpeedSampleCount)
	require.InDelta(t, 900.0/3, *model.Channels[0].AvgOutputTokensPerSecond, 1e-9)
	require.Equal(t, 2, model.Channels[0].RequestCount)
	require.Equal(t, 50.0, model.Channels[0].SuccessRate)
	require.InDelta(t, 5800.0/3, *model.Channels[0].AvgFirstTokenLatencyMs, 1e-9)

	var rows sql.Rows
	err = db.Driver().Query(ctx, qb.BuildProbeStatsQuery(false, "", qb.ThroughputModeRowNumber), []any{at.Add(-time.Minute), at.Add(time.Minute)}, &rows)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var channelID, total, success, speedCount, ttft, requests, streams int64
	var speedSum float64
	require.NoError(t, rows.Scan(&channelID, &total, &success, &speedSum, &speedCount, &ttft, &requests, &streams))
	require.EqualValues(t, 3, speedCount)
	require.InDelta(t, 900.0/3, speedSum/float64(speedCount), 1e-9)
	require.EqualValues(t, 2, total) // canceled stays outside probe availability.
	require.EqualValues(t, 1, success)
	require.EqualValues(t, 2, requests)
	require.EqualValues(t, 3, streams)
	require.EqualValues(t, 5800, ttft)
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())
}

func TestModelPerformanceDoesNotMultiplyAttemptsByUsageRows(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:model-attempt-samples?mode=memory&_fk=0")
	defer db.Close()
	ctx := authz.WithTestBypass(t.Context())
	ch := db.Channel.Create().SetType("openai").SetName("channel").SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetCredentials(objects.ChannelCredentials{}).SaveX(ctx)
	req := db.Request.Create().SetChannelID(ch.ID).SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus("completed").SaveX(ctx)
	for i, status := range []requestexecution.Status{requestexecution.StatusFailed, requestexecution.StatusCompleted} {
		db.RequestExecution.Create().SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("model").SetRequestBody([]byte(`{}`)).
			SetStatus(status).SetStream(true).SetMetricsLatencyMs(1000).SetMetricsFirstTokenLatencyMs(int64(100 + i*200)).SaveX(ctx)
	}
	for range 2 {
		db.UsageLog.Create().SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("model").SetTotalTokens(100).SetTotalCost(1).SaveX(ctx)
	}
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: db})
	resolver := &queryResolver{&Resolver{client: db, systemService: system}}
	stats, err := resolver.queryAnalyticsModelStats(ctx, nil)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	require.Equal(t, 2, stats[0].RequestCount)
	require.Equal(t, 50.0, stats[0].SuccessRate)
	require.Equal(t, 200.0, *stats[0].AvgFirstTokenLatencyMs)
	require.Equal(t, 200, stats[0].TotalTokens)
	require.Equal(t, 2.0, stats[0].Cost)
}
