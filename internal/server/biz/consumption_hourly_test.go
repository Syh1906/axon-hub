package biz

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

func TestConsumptionHourlyReusesUnchangedFacts(t *testing.T) {
	f := newConsumptionFixture(t)
	loc := f.service.system.TimeLocation(f.ctx)
	at := time.Now().In(loc).AddDate(0, 0, -2)
	start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
	r, _ := f.addRequest(t, start.Add(2*time.Hour), "model-a", request.StatusCompleted, 100, 20, 10, 0, 2, nil)
	f.addRequest(t, start.Add(15*time.Hour), "model-b", request.StatusCompleted, 200, 40, 0, 0, 3, nil)
	day := start.Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost,
		Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 5.0, page.Slices[0].Entries[0].Metrics.TotalCost)
	stored := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryIsNil()).AllX(f.ctx)
	require.Len(t, stored, 24)
	before := map[int]*ent.ConsumptionSnapshot{}
	for _, hour := range stored {
		before[hour.ID] = hour
		require.False(t, hour.Pending)
		require.NotContains(t, string(hour.Payload), "request_body")
	}
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(7).ExecX(f.ctx)
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 10.0, page.Slices[0].Entries[0].Metrics.TotalCost)
	changed := 0
	for _, hour := range f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryIsNil()).AllX(f.ctx) {
		if hour.Fingerprint != before[hour.ID].Fingerprint {
			changed++
			require.Equal(t, start.Add(2*time.Hour).Unix(), hour.RangeStart.Unix())
		} else {
			require.Equal(t, before[hour.ID].GeneratedAt, hour.GeneratedAt)
		}
	}
	require.Equal(t, 1, changed, "补价只重建受影响小时")
}

func TestConsumptionHourlyMatchesWholeDay(t *testing.T) {
	testConsumptionHourlyMatchesWholeDay(t, newConsumptionFixture(t))
}

func TestConsumptionHourlyMatchesWholeDayPostgres(t *testing.T) {
	testConsumptionHourlyMatchesWholeDay(t, newStatisticsPostgresFixture(t))
}

func TestConsumptionHourlyMatchesWholeDayMySQL(t *testing.T) {
	testConsumptionHourlyMatchesWholeDay(t, newStatisticsMySQLFixture(t))
}

func testConsumptionHourlyMatchesWholeDay(t *testing.T, f *consumptionFixture) {
	loc := f.service.system.TimeLocation(f.ctx)
	at := time.Now().In(loc).AddDate(0, 0, -2)
	start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
	f.addRequest(t, start.Add(2*time.Hour), "model-a", request.StatusCompleted, 100, 20, 10, 0, 2, nil)
	f.addRequest(t, start.Add(15*time.Hour), "model-b", request.StatusFailed, 200, 40, 0, 0, 3, nil)
	for _, dim := range []ConsumptionDimension{ConsumptionDimensionChannel, ConsumptionDimensionModel, ConsumptionDimensionAPIKey} {
		for _, metric := range []ConsumptionMetric{ConsumptionMetricCost, ConsumptionMetricCache} {
			day := start.Format("2006-01-02")
			input := ConsumptionFilter{Dimension: dim, Metric: metric, Period: ConsumptionPeriodCustom,
				StartDate: &day, EndDate: &day, Uncached: true, DailySnapshot: true}
			query := consumptionDayQuery{Version: consumptionSnapshotVersion, Project: f.project.ID, Timezone: loc.String(), Currency: "USD", Start: start.UTC(), End: start.AddDate(0, 0, 1).UTC(), Filter: input}
			whole, err := f.service.query(f.ctx, input, "snapshot", f.project.ID)
			require.NoError(t, err)
			parts, _, err := f.service.readConsumptionDay(f.ctx, query, &ent.ConsumptionSnapshot{})
			require.NoError(t, err)
			require.Equal(t, whole.Entries, parts.Entries, "%s/%s", dim, metric)
			require.Equal(t, whole.ModelEntries, parts.ModelEntries)
			require.ElementsMatch(t, whole.PerformanceSamples, parts.PerformanceSamples)
		}
	}
}

func TestConsumptionHourlyBillingArchivePostgres(t *testing.T) {
	f := newStatisticsPostgresFixture(t)
	r, _ := addCrossChannelBilling(t, f, lo.ToPtr(0.02))
	start := r.CreatedAt.UTC().Truncate(time.Hour)
	filter := ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost, DailySnapshot: true}
	check := func() {
		rows, err := f.service.queryGroups(f.ctx, f.project.ID, filter, &start, start.Add(time.Hour))
		require.NoError(t, err)
		total := 0.0
		for _, row := range rows {
			total += row.Cost
		}
		require.InDelta(t, 0.05, total, 1e-10)
		speeds, err := RequestOutputSpeeds(f.ctx, f.client, []int{r.ID})
		require.NoError(t, err)
		require.NotNil(t, speeds[r.ID])
	}
	check()
	require.NoError(t, f.service.writeCacheWindow(f.ctx, f.project.ID, start, start.Add(time.Hour)))
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{r.ID}))
	check()
	logs := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(r.ID)).Select(ArchivedUsageFields...).AllX(f.ctx)
	require.NoError(t, ArchiveUsageStatistics(f.ctx, f.client, logs))
	f.client.UsageLog.Delete().Where(usagelog.RequestIDEQ(r.ID)).ExecX(f.ctx)
	rows, err := f.service.queryGroups(f.ctx, f.project.ID, filter, &start, start.Add(time.Hour))
	require.NoError(t, err)
	total := 0.0
	for _, row := range rows {
		total += row.Cost
	}
	require.InDelta(t, 0.05, total, 1e-10)
}

func TestConsumptionHourlyKeepsCommittedProgress(t *testing.T) {
	f := newConsumptionFixture(t)
	loc := f.service.system.TimeLocation(f.ctx)
	at := time.Now().In(loc).AddDate(0, 0, -2)
	start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
	f.addRequest(t, start.Add(time.Hour), "model-a", request.StatusCompleted, 100, 20, 0, 0, 2, nil)
	day := start.Format("2006-01-02")
	query := consumptionDayQuery{Version: consumptionSnapshotVersion, Project: f.project.ID, Timezone: loc.String(), Currency: "USD", Start: start.UTC(), End: start.AddDate(0, 0, 1).UTC(),
		Filter: ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	writes := 0
	f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				writes++
				if writes == 4 {
					cancel()
					return nil, context.Canceled
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	_, _, err := f.service.readConsumptionDay(ctx, query, &ent.ConsumptionSnapshot{})
	require.ErrorIs(t, err, context.Canceled)
	before := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryIsNil()).AllX(f.ctx)
	require.Len(t, before, 3, "后续小时取消不回滚已提交进度")
	result, _, err := f.service.readConsumptionDay(f.ctx, query, &ent.ConsumptionSnapshot{})
	require.NoError(t, err)
	require.Equal(t, 2.0, result.Entries[0].Metrics.TotalCost)
	for _, hour := range before {
		after := f.client.ConsumptionSnapshot.GetX(f.ctx, hour.ID)
		require.Equal(t, hour.GeneratedAt, after.GeneratedAt)
	}
}

func TestConsumptionHourlyCalendarBoundaries(t *testing.T) {
	f := newConsumptionFixture(t)
	for _, tc := range []struct {
		zone, day string
		hours     int
	}{
		{"America/New_York", "2026-03-08", 23},
		{"America/New_York", "2026-11-01", 25},
		{"Asia/Kolkata", "2026-11-16", 25},
	} {
		loc, err := time.LoadLocation(tc.zone)
		require.NoError(t, err)
		start, err := time.ParseInLocation("2006-01-02", tc.day, loc)
		require.NoError(t, err)
		end := start.AddDate(0, 0, 1)
		hours, err := f.service.consumptionHours(f.ctx, consumptionDayQuery{Project: f.project.ID, Start: start.UTC(), End: end.UTC(), Timezone: tc.zone}, false)
		require.NoError(t, err)
		require.Len(t, hours, tc.hours)
		next := start.UTC()
		for _, hour := range hours {
			require.Equal(t, next, hour.start)
			require.True(t, hour.end.After(hour.start))
			require.LessOrEqual(t, hour.end.Sub(hour.start), time.Hour)
			next = hour.end
		}
		require.Equal(t, end.UTC(), next, "夏令时与半小时时区必须完整覆盖、没有重叠")
	}
}

func TestConsumptionHourlyRechecksCorrectionPostgres(t *testing.T) {
	f := newStatisticsPostgresFixture(t)
	start := time.Date(2026, 11, 10, 16, 0, 0, 0, time.UTC)
	r, _ := f.addRequest(t, start.Add(time.Minute), "model-a", request.StatusCompleted, 100, 20, 0, 0, 2, nil)
	f.addRequest(t, start.Add(time.Hour+time.Minute), "model-a", request.StatusCompleted, 100, 20, 0, 0, 3, nil)
	injected := false
	f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if at, ok := m.Field("range_start"); m.Op().Is(ent.OpCreate) && ok && at.(time.Time).Equal(start.Add(time.Hour)) && !injected {
				injected = true
				// Another connection corrects an already committed hour while a
				// later hour is being prepared in its own repeatable-read snapshot.
				f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(7).ExecX(f.ctx)
			}
			return next.Mutate(ctx, m)
		})
	})
	day := "2026-11-11"
	query := consumptionDayQuery{Version: consumptionSnapshotVersion, Project: f.project.ID, Start: start, End: start.Add(24 * time.Hour), Timezone: "Asia/Shanghai", Currency: "USD",
		Filter: ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}}
	result, _, err := f.service.readConsumptionDay(f.ctx, query, &ent.ConsumptionSnapshot{})
	require.NoError(t, err)
	require.True(t, injected)
	require.Equal(t, 10.0, result.Entries[0].Metrics.TotalCost, "最终复核必须替换准备后被修正的小时")
}
