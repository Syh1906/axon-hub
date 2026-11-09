package biz

import (
	"context"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestChannelTodayUsageReaderGetsSystemCurrency(t *testing.T) {
	f := newConsumptionFixture(t)
	require.NoError(t, f.service.system.SetGeneralSettings(f.ctx, SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "CNY"}))
	svc := &ChannelService{AbstractService: &AbstractService{db: f.client}, SystemService: f.service.system}
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadChannels))
	require.False(t, authz.HasScope(ctx, scopes.ScopeReadSettings))
	rows, err := svc.TodayUsage(ctx, []int{f.channel.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "CNY", rows[0].CurrencyCode)
	require.Equal(t, "Asia/Shanghai", rows[0].Timezone)
	_, err = svc.TodayUsage(f.viewer(t, f.project.ID), []int{f.channel.ID})
	require.Error(t, err)
}

func TestChannelTodayUsagePerformanceUsesValidFinalSamples(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	_, first := f.addRequest(t, start, "measured", request.StatusCompleted, 100, 100, 0, 0, 1, lo.ToPtr(int64(200)))
	first.Update().SetMetricsFirstTokenLatencyMs(100).SaveX(f.ctx)
	_, second := f.addRequest(t, start, "measured", request.StatusCompleted, 100, 300, 0, 0, 2, lo.ToPtr(int64(200)))
	second.Update().SetMetricsFirstTokenLatencyMs(300).SetMetricsLatencyMs(2200).SaveX(f.ctx)
	_, failed := f.addRequest(t, start, "measured", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	failed.Update().ClearMetricsFirstTokenLatencyMs().SaveX(f.ctx)
	_, excluded := f.addRequest(t, start, "measured", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	excluded.Update().SetResponseStatusCode(404).ClearMetricsFirstTokenLatencyMs().SaveX(f.ctx)
	nonStream, _ := f.addRequest(t, start, "non-stream", request.StatusCompleted, 100, 100, 0, 0, 1, lo.ToPtr(int64(200)))
	f.client.RequestExecution.Create().SetRequestID(nonStream.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).
		SetModelID("non-stream").SetStatus(requestexecution.StatusCompleted).SetRequestBody([]byte(`{}`)).
		SetStream(false).SetMetricsLatencyMs(1200).SetMetricsFirstByteLatencyMs(200).SetMetricsFirstTokenLatencyMs(700).SaveX(f.ctx)
	ambiguous, _ := f.addRequest(t, start, "ambiguous", request.StatusCompleted, 100, 100, 0, 0, 1, lo.ToPtr(int64(200)))
	f.client.UsageLog.Create().SetRequestID(ambiguous.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).
		SetModelID("ambiguous").SetCompletionTokens(100).SetTotalCost(1).SaveX(f.ctx)
	f.addRequest(t, start, "forwarded", request.StatusCompleted, 100, 100, 0, 0, 1, lo.ToPtr(int64(1150)))
	f.addRequest(t, start, "in-progress", request.StatusProcessing, 100, 100, 0, 0, 1, lo.ToPtr(int64(200)))
	rows, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	for _, row := range rows {
		switch row.Model {
		case "measured":
			require.Equal(t, 4, row.RequestCount)
			require.InDelta(t, 2.0/4, *row.SuccessRate, 1e-9)
			require.Equal(t, 200.0, *row.AvgFirstTokenMs)
			require.InDelta(t, (100.0/1+300.0/2)/2, *row.AvgTokensPerSecond, 1e-9)
		case "forwarded":
			require.Nil(t, row.AvgTokensPerSecond, "不足100毫秒的生成窗口不计入速度")
		case "in-progress":
			require.Nil(t, row.SuccessRate)
			require.Nil(t, row.AvgFirstTokenMs)
			require.Nil(t, row.AvgTokensPerSecond)
		default:
			require.Equal(t, 1.0, *row.SuccessRate)
			require.Equal(t, 700.0, *row.AvgFirstTokenMs)
			require.Nil(t, row.AvgTokensPerSecond)
		}
	}
}

func TestChannelTodayUsageCacheFreshnessAndPermission(t *testing.T) {
	f := newConsumptionFixture(t)
	svc := &ChannelService{AbstractService: &AbstractService{db: f.client}, SystemService: f.service.system}
	at := time.Now()
	f.addRequest(t, at, "cached", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadChannels))
	first, err := svc.TodayUsageDetails(ctx, f.channel.ID)
	require.NoError(t, err)
	f.addRequest(t, at, "cached", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	warm, err := svc.TodayUsageDetails(ctx, f.channel.ID)
	require.NoError(t, err)
	require.Equal(t, first.Models, warm.Models)
	require.Equal(t, 1, warm.Models[0].RequestCount)
	// Cached data never grants access to a caller without channel permission.
	_, err = svc.TodayUsageDetails(f.viewer(t, f.project.ID), f.channel.ID)
	require.Error(t, err)
	for _, key := range svc.todayUsageCache.entries.Keys() {
		entry, _ := svc.todayUsageCache.entries.Peek(key)
		entry.expires = time.Now().Add(-time.Second)
		svc.todayUsageCache.entries.Add(key, entry)
	}
	fresh, err := svc.TodayUsageDetails(ctx, f.channel.ID)
	require.NoError(t, err)
	require.Equal(t, 2, fresh.Models[0].RequestCount)
	require.Equal(t, 3.0, fresh.Models[0].TotalCost)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	transactional, err := svc.TodayUsageDetails(ent.NewContext(ctx, tx.Client()), f.channel.ID)
	require.NoError(t, err)
	require.Equal(t, fresh.Models, transactional.Models)
	require.NoError(t, tx.Rollback())
	reader := ent.NewClient(ent.Driver(f.client.Driver()))
	uncached, err := svc.TodayUsageDetails(ent.NewContext(ctx, reader), f.channel.ID)
	require.NoError(t, err, "a context reader without a transaction must start its own read snapshot")
	require.Equal(t, fresh.Models, uncached.Models)
	location := svc.SystemService.TimeLocation(ctx)
	day := at.In(location)
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, location)
	tomorrow, err := svc.cachedTodayUsageModels(f.ctx, f.channel.ID, start.AddDate(0, 0, 1), start.AddDate(0, 0, 2))
	require.NoError(t, err)
	require.Empty(t, tomorrow)
	// An errored read is never entered into the cache.
	canceled, cancel := context.WithCancel(f.ctx)
	cancel()
	_, err = svc.cachedTodayUsageModels(canceled, f.channel.ID, start.AddDate(0, 0, 2), start.AddDate(0, 0, 3))
	require.Error(t, err)
	require.Equal(t, 2, svc.todayUsageCache.entries.Len())
}

func TestChannelTodayUsageDetailsMatchesSummaryAndModels(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, time.FixedZone("report", 8*60*60))
	end := start.AddDate(0, 0, 1)
	f.addRequest(t, start.Add(-time.Second), "outside", request.StatusCompleted, 100, 10, 0, 0, 100, nil)
	f.addRequest(t, end, "outside", request.StatusCompleted, 100, 10, 0, 0, 100, nil)
	req, _ := f.addRequest(t, start, "model-a", request.StatusCompleted, 100, 10, 80, 0, 2, nil)
	// A second usage row must add tokens/cost, but not duplicate requests.
	f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("model-a").
		SetPromptTokens(100).SetCompletionTokens(10).SetTotalTokens(110).SetTotalCost(3).SaveX(f.ctx)
	// A later attempt on a different channel must not replace the final model.
	f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID + 999).
		SetModelID("wrong-channel").SetStatus(requestexecution.StatusFailed).SetRequestBody([]byte(`{}`)).SaveX(f.ctx)
	otherProject := f.client.Project.Create().SetName("unrelated").SaveX(f.ctx)
	f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(otherProject.ID).SetChannelID(f.channel.ID).
		SetModelID("wrong-project").SetStatus(requestexecution.StatusFailed).SetRequestBody([]byte(`{}`)).SaveX(f.ctx)
	f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(otherProject.ID).SetChannelID(f.channel.ID).
		SetModelID("wrong-project").SetPromptTokens(999).SetTotalCost(999).SaveX(f.ctx)
	f.addRequest(t, start.Add(time.Minute), "model-a", request.StatusCompleted, 200, 20, 0, 0, 1, nil)
	missing, _ := f.addRequest(t, start.Add(2*time.Minute), " ", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	// No actual model falls back to the client request model.
	partial, _ := f.addRequest(t, start.Add(3*time.Minute), "model-c", request.StatusCompleted, 100, 10, 0, 0, 0, nil)
	f.client.UsageLog.Create().SetRequestID(partial.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("model-c").
		SetPromptTokens(10).SetTotalTokens(10).SaveX(f.ctx)
	// Other-channel usage linked to a target request is not included.
	f.client.UsageLog.Create().SetRequestID(missing.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID + 999).SetModelID("other").
		SetPromptTokens(1000).SetTotalTokens(1000).SetTotalCost(100).SaveX(f.ctx)
	models, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, end)
	require.NoError(t, err)
	require.Len(t, models, 3)
	require.Equal(t, "model-a", models[0].Model)
	require.Equal(t, 2, models[0].RequestCount)
	require.Equal(t, int64(440), models[0].TotalTokens)
	require.Equal(t, 6.0, models[0].TotalCost)
	require.InDelta(t, 0.2, *models[0].CacheHitRate, 1e-9)
	require.Equal(t, 1, models[1].UnpricedCount)
	require.NotNil(t, models[1].CacheHitRate)
	require.Zero(t, *models[1].CacheHitRate)
	require.Equal(t, "public-model", models[2].Model)
	require.Nil(t, models[2].CacheHitRate)
	summary, err := queryChannelTodayUsage(f.ctx, f.client, []int{f.channel.ID}, start, end)
	require.NoError(t, err)
	var count, unpriced int
	var cost float64
	for _, model := range models {
		count += model.RequestCount
		unpriced += model.UnpricedCount
		cost += model.TotalCost
	}
	require.Equal(t, summary[0].RequestCount, count)
	require.Equal(t, summary[0].UnpricedCount, unpriced)
	require.Equal(t, summary[0].TotalCost, cost)
}

func TestChannelTodayUsageKeepsAllUsageRowsBeforePaging(t *testing.T) {
	f := newConsumptionFixture(t)
	start := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	req, _ := f.addRequest(t, start, "model", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
	builders := make([]*ent.UsageLogCreate, channelUsageBatchSize)
	for i := range builders {
		builders[i] = f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).
			SetChannelID(f.channel.ID).SetModelID("model").SetPromptTokens(100).SetCompletionTokens(10).SetTotalCost(1)
	}
	f.client.UsageLog.CreateBulk(builders...).SaveX(f.ctx)
	f.addRequest(t, start, "model", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	rows, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, start.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 2, rows[0].RequestCount)
	require.Equal(t, int64((channelUsageBatchSize+2)*110), rows[0].TotalTokens)
	require.Equal(t, float64(channelUsageBatchSize+3), rows[0].TotalCost)
	require.Nil(t, rows[0].AvgTokensPerSecond, "没有首字节证据的记录不推算速度")
}

func TestChannelTodayUsageDetailsPermissionsAndEmpty(t *testing.T) {
	f := newConsumptionFixture(t)
	svc := &ChannelService{AbstractService: &AbstractService{db: f.client}, SystemService: f.service.system}
	_, err := svc.TodayUsageDetails(f.viewer(t, f.project.ID), f.channel.ID)
	require.Error(t, err)
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadChannels))
	_, err = svc.TodayUsageDetails(ctx, f.channel.ID+999)
	require.Error(t, err)
	result, err := svc.TodayUsageDetails(ctx, f.channel.ID)
	require.NoError(t, err)
	require.Empty(t, result.Models)
	require.Equal(t, "USD", result.CurrencyCode)
	require.Equal(t, "Asia/Shanghai", result.Timezone)
}

func TestChannelTodayUsageDateBoundaryAndMissingPrices(t *testing.T) {
	f := newConsumptionFixture(t)
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, 1)
	f.addRequest(t, start.Add(-time.Nanosecond), "outside-before", request.StatusCompleted, 10, 1, 0, 0, 100, nil)
	f.addRequest(t, end, "outside-after", request.StatusCompleted, 10, 1, 0, 0, 100, nil)
	f.addRequest(t, start, "zero-price", request.StatusCompleted, 10, 1, 0, 0, 0, nil)
	priced, _ := f.addRequest(t, start.Add(time.Hour), "priced", request.StatusCompleted, 10, 1, 0, 0, 2, nil)
	// A second usage row increases cost, but not the request count.
	f.client.UsageLog.Create().SetRequestID(priced.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("priced").
		SetPromptTokens(10).SetCompletionTokens(1).SetTotalTokens(11).SetTotalCost(3).SaveX(f.ctx)
	unpriced, _ := f.addRequest(t, start.Add(2*time.Hour), "no-price", request.StatusCompleted, 0, 0, 0, 0, 0, nil)
	f.client.UsageLog.Create().SetRequestID(unpriced.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("no-price").
		SetPromptTokens(10).SetCompletionTokens(1).SetTotalTokens(11).SaveX(f.ctx)
	f.addRequest(t, start.Add(3*time.Hour), "no-usage", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	results, err := queryChannelTodayUsage(f.ctx, f.client, []int{f.channel.ID, f.channel.ID + 1}, start, end)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, 4, results[0].RequestCount)
	require.Equal(t, 2, results[0].UnpricedCount)
	require.Equal(t, 5.0, results[0].TotalCost)
	require.Equal(t, "Asia/Shanghai", results[0].Timezone)
	require.Zero(t, results[1].RequestCount)
	require.Zero(t, results[1].UnpricedCount)
	require.Zero(t, results[1].TotalCost)
}

// 定向渠道统计必须在聚合前排除其他渠道，不能每次翻页都聚合全站当天用量。
func TestChannelTodayUsageBoundsAggregation(t *testing.T) {
	start := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	for _, db := range []string{dialect.SQLite, dialect.Postgres, dialect.MySQL} {
		t.Run(db, func(t *testing.T) {
			query, args := usageForConsumption(0, db, &start, start.AddDate(0, 0, 1), 17, 23).Query()
			require.GreaterOrEqual(t, strings.Count(query, "channel_id"), 4)
			counts := map[int]int{}
			for _, arg := range args {
				if value, ok := arg.(int); ok {
					counts[value]++
				}
			}
			// 请求范围与用量聚合各自约束相同的目标渠道。
			require.Equal(t, 2, counts[17])
			require.Equal(t, 2, counts[23])
		})
	}
}
