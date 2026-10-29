package biz

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

type consumptionFixture struct {
	client  *ent.Client
	service *ConsumptionService
	ctx     context.Context
	project *ent.Project
	user    *ent.User
	key     *ent.APIKey
	channel *ent.Channel
}

func newConsumptionFixture(t *testing.T) *consumptionFixture {
	t.Helper()
	client := enttest.NewEntClient(t, "sqlite3", fmt.Sprintf("file:consumption_%d?mode=memory&_fk=0", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(context.Background())
	project := client.Project.Create().SetName("stats").SaveX(ctx)
	user := client.User.Create().SetEmail("stats@example.test").SetPassword("unused-test-hash").SetFirstName("统计用户").SaveX(ctx)
	key := client.APIKey.Create().SetName("测试 Key").SetKey("fixture-not-a-credential").SetUserID(user.ID).SetProjectID(project.ID).SaveX(ctx)
	channel := client.Channel.Create().SetName("测试渠道").SetType("openai").SetBaseURL("https://example.test").SetSupportedModels([]string{"model-a", "model-b"}).SetDefaultTestModel("model-a").SetCredentials(objects.ChannelCredentials{}).SaveX(ctx)
	system := NewSystemService(SystemServiceParams{Ent: client})
	require.NoError(t, system.SetGeneralSettings(ctx, SystemGeneralSettings{Timezone: "Asia/Shanghai", CurrencyCode: "USD"}))
	return &consumptionFixture{client: client, service: NewConsumptionService(client, system, xcache.Config{}), ctx: ctx, project: project, user: user, key: key, channel: channel}
}

func (f *consumptionFixture) addRequest(t *testing.T, at time.Time, model string, status request.Status, input, output, read, write int64, cost float64, byteLatency *int64) (*ent.Request, *ent.RequestExecution) {
	t.Helper()
	at = at.UTC()
	req := f.client.Request.Create().SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(f.channel.ID).SetModelID("public-model").SetStatus(status).SetRequestBody([]byte(`{}`)).SetCreatedAt(at).SaveX(f.ctx)
	exec := f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID(model).SetStatus(requestexecution.Status(status)).SetRequestBody([]byte(`{}`)).SetCreatedAt(at).
		SetMetricsLatencyMs(1200).SetMetricsFirstTokenLatencyMs(700).SetNillableMetricsFirstByteLatencyMs(byteLatency).
		SetCacheCompatibilityKey("fixture-prefix").SetTheoreticalCacheTokens(1000).SetCacheTTLBucket("5m").SaveX(f.ctx)
	if input+output > 0 {
		f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetAPIKeyID(f.key.ID).SetModelID(model).
			SetPromptTokens(input).SetCompletionTokens(output).SetTotalTokens(input + output).SetPromptCachedTokens(read).SetPromptWriteCachedTokens(write).SetTotalCost(cost).SaveX(f.ctx)
	}
	return req, exec
}

func (f *consumptionFixture) viewer(t *testing.T, projectID int, grants ...string) context.Context {
	t.Helper()
	viewer := &ent.User{ID: f.user.ID, Scopes: grants}
	ctx := contexts.WithUser(context.Background(), viewer)
	ctx = contexts.WithProjectID(ctx, projectID)
	ctx, err := authz.WithPrincipal(ctx, authz.Principal{Type: authz.PrincipalTypeUser, UserID: &viewer.ID})
	require.NoError(t, err)
	return ctx
}

func TestConsumptionCountsModelsAndCacheDenominator(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().Add(-30 * time.Minute)
	req, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 1000, 100, 800, 100, 2, lo.ToPtr(int64(200)))
	f.addRequest(t, at, "model-b", request.StatusCompleted, 2000, 200, 0, 0, 3, nil)
	// A failed prior attempt must neither multiply usage nor lower final success rate.
	f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID + 999).SetModelID("retried-model").SetStatus(requestexecution.StatusFailed).SetRequestBody([]byte(`{}`)).SaveX(f.ctx)
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard))
	for _, dimension := range []ConsumptionDimension{ConsumptionDimensionUser, ConsumptionDimensionAPIKey, ConsumptionDimensionChannel} {
		result, err := f.service.Leaderboard(ctx, ConsumptionFilter{Dimension: dimension, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll})
		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		entry := result.Entries[0]
		require.Equal(t, 2, entry.Metrics.RequestCount)
		require.Equal(t, int64(3300), entry.Metrics.TotalTokens)
		require.Equal(t, 5.0, entry.Metrics.TotalCost)
		require.Len(t, entry.Models, 2)
		require.Equal(t, "model-b", entry.Models[0].Model)
		require.Equal(t, 1.0, *entry.Metrics.SuccessRate)
		require.Equal(t, 100.0, *entry.Metrics.AvgTokensPerSecond) // missing first-byte samples excluded
		require.Equal(t, 0.8, *entry.Metrics.CacheHitRate)         // denominator is 1000, not 3000 or 1900
	}
	result, err := f.service.Leaderboard(ctx, ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCache, Period: ConsumptionPeriodAll})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	require.Equal(t, 1, result.Entries[0].Metrics.RequestCount)
	require.Equal(t, 2.0, result.Entries[0].Metrics.CacheCreationCost)
	require.Len(t, result.Entries[0].Models, 1)
}

func TestConsumptionPermissionsAndProjectIsolation(t *testing.T) {
	f := newConsumptionFixture(t)
	f.addRequest(t, time.Now().Add(-time.Hour), "model-a", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll}
	_, err := f.service.Leaderboard(f.viewer(t, f.project.ID), input)
	require.Error(t, err)
	_, err = f.service.Leaderboard(contexts.WithAPIKey(context.Background(), f.key), input)
	require.Error(t, err)
	_, err = f.service.Leaderboard(contexts.WithUser(context.Background(), f.user), input)
	require.Error(t, err)
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard))
	_, err = f.service.UsageStatistics(ctx, input)
	require.Error(t, err)
	other := f.client.Project.Create().SetName("other").SaveX(f.ctx)
	input.ProjectID = &objects.GUID{Type: ent.TypeProject, ID: other.ID}
	result, err := f.service.Leaderboard(f.viewer(t, other.ID, string(scopes.ScopeReadLeaderboard)), input)
	require.NoError(t, err)
	require.Empty(t, result.Entries)
	input.ProjectID = nil
	result, err = f.service.Leaderboard(f.viewer(t, other.ID, string(scopes.ScopeReadLeaderboard)), input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1) // Admin queries ignore ambient project context.
	member := &ent.User{ID: f.user.ID, Edges: ent.UserEdges{ProjectUsers: []*ent.UserProject{{ProjectID: f.project.ID, Scopes: []string{string(scopes.ScopeReadLeaderboard)}}}}}
	ctx = contexts.WithUser(f.viewer(t, other.ID), member)
	_, err = f.service.Leaderboard(ctx, input)
	require.Error(t, err)
	owner := &ent.User{ID: f.user.ID, IsOwner: true}
	ctx = contexts.WithUser(f.viewer(t, f.project.ID), owner)
	result, err = f.service.UsageStatistics(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
}

func TestConsumptionOutcomeAndSpeedSamples(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().Add(-time.Hour)
	f.addRequest(t, at, "model", request.StatusCompleted, 100, 100, 0, 0, 1, lo.ToPtr(int64(200)))
	f.addRequest(t, at, "model", request.StatusCompleted, 100, 900, 0, 0, 1, lo.ToPtr(int64(1150)))
	f.addRequest(t, at, "model", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	_, quota := f.addRequest(t, at, "model", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	f.client.RequestExecution.UpdateOne(quota).SetResponseStatusCode(429).SetErrorMessage("rate limit exceeded").SaveX(f.ctx)
	_, missing := f.addRequest(t, at, "model", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	f.client.RequestExecution.UpdateOne(missing).SetResponseStatusCode(404).SaveX(f.ctx)
	f.addRequest(t, at, "model", request.StatusCanceled, 0, 0, 0, 0, 0, lo.ToPtr(int64(100)))
	_, proxyAbort := f.addRequest(t, at, "model", request.StatusFailed, 0, 0, 0, 0, 0, lo.ToPtr(int64(100)))
	f.client.RequestExecution.UpdateOne(proxyAbort).SetResponseStatusCode(499).SaveX(f.ctx)
	_, earlyAbort := f.addRequest(t, at, "model", request.StatusCanceled, 0, 0, 0, 0, 0, nil)
	f.client.RequestExecution.UpdateOne(earlyAbort).SetMetricsFirstByteRecorded(true).SaveX(f.ctx)
	f.addRequest(t, at, "model", request.StatusCanceled, 0, 0, 0, 0, 0, nil) // Legacy cancellation has unknown observation evidence.
	result, err := f.service.Leaderboard(f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard)), ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll})
	require.NoError(t, err)
	metrics := result.Entries[0].Metrics
	require.Equal(t, 9, metrics.RequestCount)
	require.Equal(t, 0.5, *metrics.SuccessRate) // Two successes, one upstream failure, one new pre-byte cancellation.
	require.Equal(t, 100.0, *metrics.AvgTokensPerSecond)
}

func TestConsumptionLocalAdmissionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		success      bool
		upstreamFail bool
		wantRate     *float64
	}{
		{name: "only local rejections"},
		{name: "success and local rejections", success: true, wantRate: lo.ToPtr(1.0)},
		{name: "upstream 429 still counts", success: true, upstreamFail: true, wantRate: lo.ToPtr(0.5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConsumptionFixture(t)
			at := time.Now().Add(-time.Hour)
			messages := []string{
				"channel 测试渠道 is at capacity (queue_full); please retry shortly",
				"channel 测试渠道 is at capacity (queue_timeout); please retry shortly",
				"channel 测试渠道 exhausted local RPM limit 10; please retry shortly",
			}
			if tc.upstreamFail {
				messages = append(messages, "upstream is overloaded")
			}
			for _, message := range messages {
				_, execution := f.addRequest(t, at, "model", request.StatusFailed, 0, 0, 0, 0, 0, nil)
				f.client.RequestExecution.UpdateOne(execution).SetResponseStatusCode(429).SetErrorMessage(message).SaveX(f.ctx)
			}
			wantCount := len(messages)
			wantCost := 0.0
			if tc.success {
				f.addRequest(t, at, "model", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
				wantCount++
				wantCost = 2
			}
			result, err := f.service.Leaderboard(f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard)), ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll})
			require.NoError(t, err)
			require.Len(t, result.Entries, 1)
			entry := result.Entries[0]
			require.Equal(t, wantCount, entry.Metrics.RequestCount)
			require.Equal(t, wantCost, entry.Metrics.TotalCost)
			require.Equal(t, tc.wantRate, entry.Metrics.SuccessRate)
			require.Equal(t, tc.wantRate, entry.Models[0].Metrics.SuccessRate)
		})
	}
}

func TestConsumptionUserDisplayNameSearch(t *testing.T) {
	for _, tc := range []struct {
		name, first, last, search, display string
	}{
		{"full name", "John", "Smith", "John Smith", "John Smith"},
		{"case insensitive", "John", "Smith", "john SMITH", "John Smith"},
		{"partial full name", "John", "Smith", "ohn Smi", "John Smith"},
		{"first name", "John", "Smith", "John", "John Smith"},
		{"last name", "John", "Smith", "Smith", "John Smith"},
		{"empty last", "John", "", "John", "John"},
		{"empty first", "", "Smith", "Smith", "Smith"},
		{"email fallback", "", "", "stats@example.test", "stats@example.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConsumptionFixture(t)
			f.client.User.UpdateOne(f.user).SetFirstName(tc.first).SetLastName(tc.last).SaveX(f.ctx)
			f.addRequest(t, time.Now().Add(-time.Hour), "model", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
			ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard))
			input := ConsumptionFilter{Dimension: ConsumptionDimensionUser, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll, Search: &tc.search}
			result, err := f.service.Leaderboard(ctx, input)
			require.NoError(t, err)
			require.Len(t, result.Entries, 1)
			require.Equal(t, tc.display, result.Entries[0].Name)
			other := f.client.Project.Create().SetName("other").SaveX(f.ctx)
			input.ProjectID = &objects.GUID{Type: ent.TypeProject, ID: other.ID}
			result, err = f.service.Leaderboard(ctx, input)
			require.NoError(t, err)
			require.Empty(t, result.Entries)
		})
	}
}

func TestConsumptionCacheWindowsIdempotenceAndConfidence(t *testing.T) {
	f := newConsumptionFixture(t)
	now := time.Now().Truncate(cacheWindowInterval)
	for i := range 5 {
		f.addRequest(t, now.Add(-30*time.Minute), fmt.Sprintf("model-%d", i%2), request.StatusCompleted, 1000, 10, 800, 0, 1, nil)
	}
	f.addRequest(t, now.Add(-30*time.Minute), "model-0", request.StatusFailed, 0, 0, 0, 0, 0, nil)
	f.addRequest(t, now.Add(-5*time.Minute), "recent-model", request.StatusCompleted, 1000, 10, 1000, 0, 1, nil)
	require.NoError(t, f.service.aggregateCacheWindows(f.ctx, now))
	count := f.client.CacheEffectivenessWindow.Query().CountX(f.ctx)
	require.Equal(t, 2, count)
	require.NoError(t, f.service.writeCacheWindow(f.ctx, f.project.ID, now.Add(-30*time.Minute), now.Add(-25*time.Minute)))
	require.Equal(t, count, f.client.CacheEffectivenessWindow.Query().CountX(f.ctx))
	result, err := f.service.Leaderboard(f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard)), ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll})
	require.NoError(t, err)
	// floor(5/6*10000)=8333; floor(8333*3000/10000)=2499; floor(8000*2499/10000)=1999.
	require.Equal(t, 1999, *result.Entries[0].Metrics.CacheCoefficient)
	for _, model := range result.Entries[0].Models {
		if model.Model == "recent-model" {
			require.Nil(t, model.Metrics.CacheCoefficient)
		}
	}
}

func TestConsumptionCacheWindowsCombineModelsTTLsAndProjects(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(cacheWindowInterval)
	f.addRequest(t, at, "model", request.StatusCompleted, 1000, 10, 500, 0, 1, nil)
	for _, bucket := range []struct {
		projectID  int
		model, ttl string
		samples    int64
	}{
		{f.project.ID, " model ", "5m", 99},
		{f.project.ID, "model", "1h", 1},
		{f.project.ID + 1, "model", "5m", 100},
	} {
		observed := bucket.samples * 500
		if bucket.projectID != f.project.ID {
			observed = bucket.samples * 1000
		}
		f.client.CacheEffectivenessWindow.Create().SetProjectID(bucket.projectID).SetChannelID(f.channel.ID).
			SetModelID(bucket.model).SetCacheTTLBucket(bucket.ttl).SetWindowStart(at).SetWindowEnd(at.Add(cacheWindowInterval)).
			SetSampleCount(bucket.samples).SetEligibleCount(bucket.samples).SetTheoreticalTokens(bucket.samples * 1000).SetObservedTokens(observed).SaveX(f.ctx)
	}
	input := ConsumptionFilter{ProjectID: &objects.GUID{Type: ent.TypeProject, ID: f.project.ID}, Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCache, Period: ConsumptionPeriodAll}
	result, err := f.service.Leaderboard(f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard)), input)
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	entry := result.Entries[0]
	require.Equal(t, 5000, *entry.Metrics.CacheCoefficient, "combine samples before applying the 100-sample confidence threshold")
	require.Equal(t, 5000, *entry.Models[0].Metrics.CacheCoefficient, "model names are normalized before combining TTL buckets")
}

func TestConsumptionCalendarRanges(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, location)
	start, end, err := consumptionRange(ConsumptionFilter{Period: ConsumptionPeriodToday}, now, location)
	require.NoError(t, err)
	require.Equal(t, 23*time.Hour, end.Sub(*start))
	start, end, err = consumptionRange(ConsumptionFilter{Period: ConsumptionPeriodWeek}, now, location)
	require.NoError(t, err)
	require.Equal(t, time.Monday, start.Weekday())
	require.Equal(t, 2, start.Day())
	require.Equal(t, 9, end.Day())
	start, end, err = consumptionRange(ConsumptionFilter{Period: ConsumptionPeriodCustom, StartDate: lo.ToPtr("2026-03-08"), EndDate: lo.ToPtr("2026-03-08")}, now, location)
	require.NoError(t, err)
	require.Equal(t, 23*time.Hour, end.Sub(*start))
	_, _, err = consumptionRange(ConsumptionFilter{Period: ConsumptionPeriodCustom, StartDate: lo.ToPtr("2026-03-09"), EndDate: lo.ToPtr("2026-03-08")}, now, location)
	require.Error(t, err)
}

func TestConsumptionFiltersUTCRecordsByLocalDay(t *testing.T) {
	for _, timezone := range []string{"Asia/Shanghai", "America/Los_Angeles"} {
		t.Run(timezone, func(t *testing.T) {
			f := newConsumptionFixture(t)
			location, err := time.LoadLocation(timezone)
			require.NoError(t, err)
			require.NoError(t, f.service.system.SetGeneralSettings(f.ctx, SystemGeneralSettings{Timezone: timezone, CurrencyCode: "USD"}))
			now := time.Now().In(location)
			start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
			end := start.AddDate(0, 0, 1)
			// Production timestamps use UTC even when the configured calendar day does not.
			f.addRequest(t, start.Add(-time.Minute).UTC(), "yesterday", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
			f.addRequest(t, start.Add(time.Minute).UTC(), "today", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
			f.addRequest(t, end.Add(time.Minute).UTC(), "tomorrow", request.StatusCompleted, 100, 10, 0, 0, 3, nil)
			result, err := f.service.Leaderboard(f.viewer(t, f.project.ID, string(scopes.ScopeReadLeaderboard)), ConsumptionFilter{
				Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodToday,
			})
			require.NoError(t, err)
			require.Len(t, result.Entries, 1)
			require.Equal(t, 2.0, result.Entries[0].Metrics.TotalCost)
			require.Len(t, result.Entries[0].Models, 1)
			require.Equal(t, "today", result.Entries[0].Models[0].Model)
		})
	}
}

func TestConsumptionCacheWindowsWithNonUTCClock(t *testing.T) {
	f := newConsumptionFixture(t)
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	at := now.Add(-30 * time.Minute).UTC()
	f.addRequest(t, at, "model", request.StatusCompleted, 1000, 100, 800, 0, 2, nil)
	require.NoError(t, f.service.aggregateCacheWindows(f.ctx, now))
	windows, err := f.client.CacheEffectivenessWindow.Query().All(f.ctx)
	require.NoError(t, err)
	require.Len(t, windows, 1)
	require.Equal(t, int64(1), windows[0].EligibleCount)
	require.Equal(t, int64(800), windows[0].ObservedTokens)
	require.True(t, windows[0].WindowStart.Equal(at.Truncate(cacheWindowInterval)))
}
