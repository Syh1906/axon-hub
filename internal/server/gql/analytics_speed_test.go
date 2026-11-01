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
		db.UsageLog.Create().SetProjectID(project.ID).SetChannelID(ch.ID).SetRequestID(req.ID).SetModelID("text-model").SetCompletionTokens(sample.tokens).SetTotalTokens(sample.tokens).SetTotalCost(0).SaveX(ctx)
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
	require.Equal(t, 300.0, *leaderboard.Entries[0].Metrics.AvgTokensPerSecond)

	handler := NewGraphqlHandlers(Dependencies{Ent: db, SystemService: system})
	graphqlClient := client.New(handler.Graphql, func(req *client.Request) { req.HTTP = req.HTTP.WithContext(ctx) })
	var result struct{ AnalyticsModelStats []*AnalyticsModelStat }
	err = graphqlClient.Post(`query { analyticsModelStats { id requestCount successRate avgFirstTokenLatencyMs avgOutputTokensPerSecond speedSampleCount channels { requestCount successRate avgFirstTokenLatencyMs avgOutputTokensPerSecond speedSampleCount } } }`, &result)
	require.NoError(t, err)
	require.Len(t, result.AnalyticsModelStats, 1)
	model := result.AnalyticsModelStats[0]
	require.Equal(t, 3, model.SpeedSampleCount)
	require.Equal(t, 300.0, *model.AvgOutputTokensPerSecond)
	require.Equal(t, 2, model.RequestCount)
	require.Equal(t, 50.0, model.SuccessRate)
	require.Equal(t, 1500.0, *model.AvgFirstTokenLatencyMs)
	require.Len(t, model.Channels, 1)
	require.Equal(t, 3, model.Channels[0].SpeedSampleCount)
	require.Equal(t, 300.0, *model.Channels[0].AvgOutputTokensPerSecond)
	require.Equal(t, 2, model.Channels[0].RequestCount)
	require.Equal(t, 50.0, model.Channels[0].SuccessRate)
	require.Equal(t, 1500.0, *model.Channels[0].AvgFirstTokenLatencyMs)

	var rows sql.Rows
	err = db.Driver().Query(ctx, qb.BuildProbeStatsQuery(false, "", qb.ThroughputModeRowNumber), []any{at.Add(-time.Minute), at.Add(time.Minute)}, &rows)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var channelID, total, success, speedCount, ttft, requests, streams int64
	var speedSum float64
	require.NoError(t, rows.Scan(&channelID, &total, &success, &speedSum, &speedCount, &ttft, &requests, &streams))
	require.EqualValues(t, 3, speedCount)
	require.Equal(t, 300.0, speedSum/float64(speedCount))
	require.EqualValues(t, 2, total) // canceled stays outside probe availability.
	require.EqualValues(t, 1, success)
	require.EqualValues(t, 2, requests)
	require.EqualValues(t, 1, streams)
	require.EqualValues(t, 1500, ttft)
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())
}
