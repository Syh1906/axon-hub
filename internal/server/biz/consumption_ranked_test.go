package biz

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/stretchr/testify/require"
)

func TestConsumptionRankedPageRevisions(t *testing.T) {
	f := newConsumptionFixture(t)
	now := time.Now().Add(-3 * time.Hour)
	req, _ := f.addRequest(t, now.AddDate(0, 0, -32), "A", request.StatusCompleted, 100, 0, 40, 0, 2, nil)
	f.addRequest(t, now, "B", request.StatusCompleted, 900, 0, 60, 0, 8, nil)
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	input := ConsumptionFilter{Period: ConsumptionPeriodAll, Metric: ConsumptionMetricCost, Dimension: ConsumptionDimensionAPIKey, OmitPerformance: true}
	read := func() *ConsumptionResult {
		var asOf *time.Time
		for range 90 {
			p, err := f.service.RankedPage(ctx, input, true, 0, nil, nil, true, nil, asOf)
			require.NoError(t, err)
			if !p.Pending {
				return p.Result
			}
			asOf = &p.ResolvedAt
			require.NoError(t, f.service.fillRequestedConsumptionSnapshots(f.ctx))
		}
		t.Fatal("missing complete range")
		return nil
	}
	first := read()
	require.Len(t, first.Entries, 1)
	e := first.Entries[0]
	require.Equal(t, 10.0, e.Metrics.TotalCost)
	require.Len(t, e.Models, 2)
	require.Equal(t, "B", e.Models[0].Model)
	require.InDelta(t, 0.1, *e.Metrics.CacheHitRate, 1e-9)
	again := read()
	require.Equal(t, first.Revision, again.Revision, "unchanged ALL dates reuse their ranking")
	require.Equal(t, again.ResolvedAt, again.RangeEnd)
	require.Same(t, first.Entries[0], again.Entries[0], "cache hit skips range aggregation")
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).SetTotalCost(9).ExecX(f.ctx)
	fixed, err := f.service.RankedPage(ctx, input, true, 0, &first.Revision, nil, true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 10.0, fixed.Result.Entries[0].Metrics.TotalCost)
	f.service.rankings.Purge()
	// 另一实例只有日汇总。历史账目已变，须从新的首批继续，不能混入旧榜。
	for {
		resumed, err := f.service.RankedPage(ctx, input, true, 1, &first.Revision, nil, true, nil, &first.ResolvedAt)
		if err != nil {
			require.ErrorContains(t, err, "ranking changed")
			break
		}
		require.True(t, resumed.Pending)
		require.NoError(t, f.service.fillRequestedConsumptionSnapshots(f.ctx))
	}
	latest := read()
	require.NotEqual(t, first.Revision, latest.Revision)
	require.Equal(t, 17.0, latest.Entries[0].Metrics.TotalCost)
	_, err = f.service.RankedPage(f.ctx, input, true, 0, &first.Revision, nil, true, nil, nil)
	require.Error(t, err)
}

func TestConsumptionRankWindowSortsAllRows(t *testing.T) {
	full := &ConsumptionResult{}
	for i := 1; i <= 130; i++ {
		full.Entries = append(full.Entries, &ConsumptionEntry{ID: objects.GUID{ID: i}, Name: fmt.Sprintf("key-%03d", i), Metrics: ConsumptionMetrics{TotalCost: float64(i)}, Models: []*ConsumptionModel{{Model: "m", Metrics: ConsumptionMetrics{TotalCost: float64(i)}}}})
	}
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKeyGroup}
	key := "totalCost"
	ordered, err := orderConsumptionRanking(full, input, nil, &key, true)
	require.NoError(t, err)
	page, err := consumptionRankWindow(ordered, input.Dimension, 0)
	require.NoError(t, err)
	require.Equal(t, 64, *page.NextOffset)
	require.Equal(t, 130.0, page.Result.Entries[0].Models[0].Metrics.TotalCost)
	page, err = consumptionRankWindow(ordered, input.Dimension, *page.NextOffset)
	require.NoError(t, err)
	require.Equal(t, 66.0, page.Result.Entries[0].Metrics.TotalCost)
	require.Equal(t, 1, full.Entries[0].ID.ID)
	search := "key-130"
	input.Search = &search
	ordered, err = orderConsumptionRanking(full, input, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, ordered.Entries, 1)
	require.Equal(t, 130, ordered.Entries[0].ID.ID)
}

func TestConsumptionAdaptivePoolLimits(t *testing.T) {
	base := sql.DBStats{MaxOpenConnections: 16}
	require.GreaterOrEqual(t, consumptionWorkerLimit(dialect.Postgres, 3, base, base), 1)
	busy := base
	busy.InUse = 8
	require.Equal(t, 1, consumptionWorkerLimit(dialect.Postgres, 3, base, busy))
	busy = base
	busy.WaitCount++
	require.Equal(t, 1, consumptionWorkerLimit(dialect.Postgres, 3, base, busy))
	busy.InUse = 16
	require.Zero(t, consumptionWorkerLimit(dialect.Postgres, 3, base, busy))
	require.Equal(t, 1, consumptionWorkerLimit(dialect.SQLite, 3, base, base))
	var p consumptionWritePressure
	p.usual.Store(int64(time.Millisecond))
	p.observe(time.Now().Add(-50 * time.Millisecond))
	require.Greater(t, p.yieldUntil.Load(), time.Now().UnixNano())
	now := time.Now()
	p.yieldUntil.Store(0)
	require.Equal(t, 1, p.limit(4, now, 2))
	require.Equal(t, 2, p.limit(4, now, 4))
	require.Equal(t, 4, p.limit(4, now.Add(3*time.Second), 4))
}

func TestConsumptionSharedWorkerSlotsPostgres(t *testing.T) {
	f := newStatisticsPostgresFixture(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		f.client.ConsumptionSnapshot.Create().SetCacheKey(fmt.Sprintf("date-%d", i)).SetProjectID(f.project.ID).SetRangeStart(now.AddDate(0, 0, -i)).SetRangeEnd(now).SetGeneratedAt(time.Unix(0, 0)).SetRequestedAt(now).SetQuery([]byte("{}")).SaveX(f.ctx)
	}
	var wg sync.WaitGroup
	type claim struct {
		job *ent.ConsumptionSnapshot
		err error
	}
	results := make(chan claim, 5)
	for i := range 5 {
		wg.Go(func() {
			j, err := f.service.claimConsumptionDate(f.ctx, now, fmt.Sprint(i), 2)
			results <- claim{j, err}
		})
	}
	wg.Wait()
	close(results)
	ids := map[int]bool{}
	for res := range results {
		require.NoError(t, res.err)
		if res.job != nil {
			require.False(t, ids[res.job.ID])
			ids[res.job.ID] = true
		}
	}
	require.Len(t, ids, 2)
}

func TestConsumptionRankedChannelSamplesAcrossDays(t *testing.T) {
	id := objects.GUID{Type: ent.TypeChannel, ID: 3}
	totals := newConsumptionRangeTotals()
	totals.add(&ConsumptionResult{PerformanceSamples: []*ConsumptionPerformanceSample{
		{ChannelID: id, Model: "A", Metrics: ConsumptionMetrics{OutcomeCount: 2, SuccessCount: 1, FirstTokenSum: 900, FirstTokenCount: 2}},
		{ChannelID: id, Model: "never billed", Metrics: ConsumptionMetrics{OutcomeCount: 4, SuccessCount: 4}},
	}, CacheSamples: []*ConsumptionCacheSample{{ChannelID: id, Model: "A", Samples: 10, Eligible: 10, Observed: 40, Theoretical: 100}}})
	totals.add(&ConsumptionResult{Entries: []*ConsumptionEntry{{ID: id, Metrics: ConsumptionMetrics{RequestCount: 1, TotalCost: 2}, Models: []*ConsumptionModel{
		{Model: "A", Metrics: ConsumptionMetrics{TotalCost: 2}},
	}}}})
	row := totals.finish(ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost}, &ConsumptionSlicePage{}, "r").Entries[0]
	require.Equal(t, 1, row.Metrics.RequestCount)
	for _, m := range []ConsumptionMetrics{row.Metrics, row.Models[0].Metrics} {
		require.Equal(t, 2.0, m.TotalCost)
		require.Equal(t, 0.5, *m.SuccessRate)
		require.Equal(t, 450.0, *m.AvgFirstTokenMs)
		require.EqualValues(t, 10, m.CoefficientSamples)
	}
}

func TestConsumptionEmptyDayReopensAfterBackdatedUsage(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().AddDate(0, 0, -3)
	day := at.In(f.service.system.TimeLocation(f.ctx)).Format("2006-01-02")
	input := ConsumptionFilter{Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost}
	viewer := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	empty, err := f.readSlices(t, viewer, input, nil, "usage")
	require.NoError(t, err)
	require.Len(t, empty.Slices, 1)
	require.Empty(t, empty.Slices[0].Entries)
	require.Equal(t, 1, f.client.ConsumptionSnapshot.Query().CountX(f.ctx))
	f.addRequest(t, at, "new", request.StatusCompleted, 40, 60, 0, 0, 3, nil)
	fresh, err := f.readSlices(t, viewer, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 3.0, fresh.Slices[0].Entries[0].Models[0].Metrics.TotalCost)
}

func TestConsumptionOversizeDaySelectsRankedPage(t *testing.T) {
	f := newConsumptionFixture(t)
	today := time.Now()
	f.addRequest(t, today, "A", request.StatusCompleted, 30, 20, 0, 0, 1, nil)
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	in := ConsumptionFilter{Period: ConsumptionPeriodToday, Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost}
	page, err := f.readSlices(t, ctx, in, nil, "usage")
	require.NoError(t, err)
	row := page.Slices[0].Entries[0]
	for len(row.Models) < 10000 {
		row.Models = append(row.Models, row.Models[0])
	}
	payload, err := json.Marshal(page.Slices[0])
	require.NoError(t, err)
	job := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryNotNil()).OnlyX(f.ctx)
	f.client.ConsumptionSnapshot.UpdateOne(job).SetPayload(payload).ExecX(f.ctx)
	in.AllowRankedPage = true
	page, err = f.service.Slices(ctx, in, nil, "usage")
	require.NoError(t, err)
	require.True(t, page.UseRankedPage)
	require.Empty(t, page.Slices)
	in.AllowRankedPage = false
	page, err = f.service.Slices(ctx, in, nil, "usage")
	require.NoError(t, err)
	require.Len(t, page.Slices[0].Entries[0].Models, 10000)
}

func TestConsumptionConcurrentColdSQLite(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:consumption_shared_read?mode=memory&cache=shared&_pragma=busy_timeout(5000)")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	client.Driver().(*entsql.Driver).DB().SetMaxOpenConns(4)
	f := newConsumptionFixtureWithClient(t, client)
	ctx, cancel := context.WithTimeout(contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID), 5*time.Second)
	defer cancel()
	var arrived atomic.Int32
	both := make(chan struct{})
	client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				switch arrived.Add(1) {
				case 1:
					select {
					case <-both:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				case 2:
					close(both)
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	input := ConsumptionFilter{Period: ConsumptionPeriodToday, Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost}
	errors := make(chan error, 2)
	for range 2 {
		go func() { _, err := f.service.Slices(ctx, input, nil, "usage"); errors <- err }()
	}
	for range 2 {
		require.NoError(t, <-errors)
	}
	require.Equal(t, 1, client.ConsumptionSnapshot.Query().CountX(ctx))
}
