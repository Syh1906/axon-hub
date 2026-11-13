package biz

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"github.com/stretchr/testify/require"
)

func TestConsumptionInvalidationIdentifiers(t *testing.T) {
	for _, db := range []string{dialect.Postgres, dialect.MySQL, dialect.SQLite} {
		for _, kind := range []string{"Request", "RequestExecution", "UsageLog", "UsageArchive", "BudgetCharge", "CacheEffectivenessWindow"} {
			q := sql.Dialect(db).Select("id").From(sql.Table("consumption_snapshots"))
			consumptionDatePredicate([]consumptionDate{{ProjectID: 1, At: time.Now()}})(q)
			query, _ := q.Query()
			if db == dialect.Postgres {
				require.NotContains(t, query, "`", kind)
			}
			require.Contains(t, query, "project_id", kind)
		}
	}
}

func TestConsumptionSlicesKeepEmptyDayCacheSamples(t *testing.T) {
	f := newConsumptionFixture(t)
	loc := f.service.system.TimeLocation(f.ctx)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	_, execution := f.addRequest(t, start.Add(time.Hour), "model-a", request.StatusCompleted, 100, 10, 0, 0, 1, nil)
	f.addRequest(t, start.AddDate(0, 0, 1).Add(time.Hour), "model-a", request.StatusCompleted, 100, 10, 10, 0, 2, nil)
	f.client.RequestExecution.UpdateOneID(execution.ID).SetMetricsFirstByteLatencyMs(200).SaveX(f.ctx)
	for i := 0; i < 2; i++ {
		samples, observed := int64(10), int64(0)
		if i == 1 {
			samples, observed = 1, 1000
		}
		at := start.AddDate(0, 0, i).Add(time.Hour).UTC()
		f.client.CacheEffectivenessWindow.Create().SetProjectID(f.project.ID).SetChannelID(f.channel.ID).SetModelID("model-a").SetCacheTTLBucket("5m").SetWindowStart(at).SetWindowEnd(at.Add(5 * time.Minute)).SetSampleCount(samples).SetEligibleCount(samples).SetTheoreticalTokens(samples * 1000).SetObservedTokens(observed).SaveX(f.ctx)
	}
	input := ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCache, Period: ConsumptionPeriodCustom}
	first, last := start.Format("2006-01-02"), start.AddDate(0, 0, 1).Format("2006-01-02")
	input.StartDate, input.EndDate = &first, &last
	full, err := f.service.query(f.ctx, input, "test", f.project.ID)
	require.NoError(t, err)
	ctx := contexts.WithUser(f.ctx, &ent.User{ID: f.user.ID, IsOwner: true})
	input.ProjectID = &objects.GUID{Type: ent.TypeProject, ID: f.project.ID}
	page, err := f.readSlices(t, ctx, input, nil, "leaderboard")
	require.NoError(t, err)
	require.Len(t, page.Slices, 2)
	require.Empty(t, page.Slices[0].Entries)
	var count, eligible, theoretical, observed int64
	for _, day := range page.Slices {
		for _, sample := range day.CacheSamples {
			count += sample.Samples
			eligible += sample.Eligible
			theoretical += sample.Theoretical
			observed += sample.Observed
		}
	}
	require.EqualValues(t, 11, count)
	require.Equal(t, *full.Entries[0].Metrics.CacheCoefficient, usageanalytics.CacheCoefficient(count, eligible, theoretical, observed))
	require.NotEmpty(t, page.Slices[0].PerformanceSamples, "没有当日缓存账目也保留实际尝试供跨日组合")
}

func TestConsumptionSlicesManifestAndCrossPageChange(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().AddDate(0, 0, -4)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	day := at.In(f.service.system.TimeLocation(f.ctx)).Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	queued, err := f.service.Slices(ctx, input, nil, "usage")
	require.NoError(t, err)
	require.True(t, queued.Pending)
	require.Empty(t, queued.Slices)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	unchanged, err := f.service.ReadSlices(ctx, input, nil, page.Days, &page.ResolvedAt, false, "usage")
	require.NoError(t, err)
	require.Empty(t, unchanged.Slices)
	require.False(t, unchanged.Pending)
	f.client.APIKey.UpdateOneID(f.key.ID).SetName("修改名称").SaveX(f.ctx)
	check, err := f.service.ReadSlices(ctx, input, nil, page.Days, &page.ResolvedAt, true, "usage")
	require.NoError(t, err)
	require.True(t, check.Pending, "不能交付旧归属页与新归属页的混合结果")
	fixed, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.NotEqual(t, page.Days[0].Revision, fixed.Days[0].Revision)
}

func TestConsumptionSnapshotRecoversLostInvalidation(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().AddDate(0, 0, -3)
	r, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	day := at.In(f.service.system.TimeLocation(f.ctx)).Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	_, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	fail := true
	f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, invalidating := m.Field("source_version"); invalidating && fail {
				return nil, errors.New("fixture invalidation unavailable")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err = f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(9).Save(f.ctx)
	require.NoError(t, err, "统计维护不阻塞账目保存")
	fail = false
	f.client.ConsumptionSnapshot.Update().SetCheckedAt(time.Now().Add(-6 * time.Minute)).ExecX(f.ctx)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 9.0, page.Slices[0].Entries[0].Metrics.TotalCost)
	generated := page.Slices[0].GeneratedAt
	f.client.ConsumptionSnapshot.Update().SetCheckedAt(time.Now().Add(-6 * time.Minute)).ExecX(f.ctx)
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.True(t, generated.Equal(page.Slices[0].GeneratedAt), "源字段没变只核对，不重算统计")
}

func TestConsumptionSnapshotActiveMembershipChangeRevokesWriter(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().Add(-time.Minute)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodToday, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	injected := false
	f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, saving := m.Field("payload"); saving && !injected {
				injected = true
				f.client.APIKey.UpdateOneID(f.key.ID).SetName("新名称").SaveX(f.ctx)
			}
			return next.Mutate(ctx, m)
		})
	})
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Contains(t, page.Slices[0].Entries[0].Name, "新名称")
}

func TestConsumptionSnapshotUnchangedActiveDayFinishes(t *testing.T) {
	f := newConsumptionFixture(t)
	f.addRequest(t, time.Now(), "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodToday, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	first, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	f.client.ConsumptionSnapshot.Update().SetGeneratedAt(time.Now().Add(-2 * time.Minute)).SetCheckedAt(time.Now().Add(-2 * time.Minute)).ExecX(f.ctx)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.False(t, page.Pending)
	require.Equal(t, first.Slices[0].GeneratedAt, page.Slices[0].GeneratedAt, "无变化不重算，不伪造生成时间")
	checked, err := f.service.ReadSlices(ctx, input, nil, page.Days, &page.ResolvedAt, true, "usage")
	require.NoError(t, err)
	require.False(t, checked.Pending)
}

func TestConsumptionSnapshotIgnoresUnrelatedMetadata(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().AddDate(0, 0, -3)
	f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	day := at.In(f.service.system.TimeLocation(f.ctx)).Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	first, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	f.client.User.Create().SetEmail("unrelated@example.test").SetPassword("test-only").SetFirstName("无关用户").SaveX(f.ctx)
	f.client.Project.Create().SetName("无关项目").SaveX(f.ctx)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, first.Slices[0].GeneratedAt, page.Slices[0].GeneratedAt, "其他用户和项目不使本日期完整重算")
	require.Equal(t, first.Days[0].Revision, page.Days[0].Revision, "无关元数据不使相同载荷重新下载")
}

func TestConsumptionSnapshotCrossDayArchivedUsageCorrection(t *testing.T) {
	f := newConsumptionFixture(t)
	loc := f.service.system.TimeLocation(f.ctx)
	at := time.Date(2026, 10, 1, 23, 59, 0, 0, loc)
	r, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	u := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(r.ID)).OnlyX(f.ctx)
	f.client.UsageLog.DeleteOneID(u.ID).ExecX(f.ctx)
	a := f.client.UsageArchive.Create().SetKind(usagearchive.KindUsage).SetSourceID(u.ID).SetRequestID(r.ID).SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(f.channel.ID).SetModelID("model-a").SetOccurredAt(at.Add(2 * time.Minute)).SetTotalCost(2).SetPromptTokens(100).SetCompletionTokens(10).SaveX(f.ctx)
	day := at.Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 2.0, page.Slices[0].Entries[0].Metrics.TotalCost)
	f.client.UsageArchive.UpdateOneID(a.ID).SetTotalCost(7).SaveX(f.ctx)
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 7.0, page.Slices[0].Entries[0].Metrics.TotalCost)
}

func TestConsumptionSlicesReuseAndCorrectHistoricalCost(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC().AddDate(0, 0, -4)
	r, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 20, 10, 0, 2, nil)
	loc := f.service.system.TimeLocation(f.ctx)
	day := at.In(loc).Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Len(t, page.Slices, 1)
	require.Equal(t, 2.0, page.Slices[0].Entries[0].Metrics.TotalCost)
	generated := page.Slices[0].GeneratedAt
	// 缓存不重扫源事实；明确区分计算时间与本次解析范围的时间。
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.True(t, generated.Equal(page.Slices[0].GeneratedAt))
	f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(7).SaveX(f.ctx)
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 7.0, page.Slices[0].Entries[0].Metrics.TotalCost)
	f.client.UsageLog.Delete().Where(usagelog.RequestIDEQ(r.ID)).ExecX(f.ctx)
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 1, page.Slices[0].Entries[0].Metrics.UnpricedRequestCount)
	row := f.client.ConsumptionSnapshot.Query().OnlyX(f.ctx)
	old := time.Now().Add(-6 * time.Minute)
	f.client.ConsumptionSnapshot.Update().Where(consumptionsnapshot.IDEQ(row.ID)).SetGeneratedAt(old).SaveX(f.ctx)
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, old.Unix(), f.client.ConsumptionSnapshot.Query().OnlyX(f.ctx).GeneratedAt.Unix(), "历史没有修正时不重算")
}

func TestConsumptionSlicesCorrectArchivedOnlyFacts(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC().AddDate(0, 0, -8)
	a := f.client.UsageArchive.Create().SetKind(usagearchive.KindRequest).SetSourceID(999).SetRequestID(999).
		SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(f.channel.ID).SetModelID("model-a").
		SetOccurredAt(at).SetTotalCost(2).SetPromptTokens(100).SetBillingVersion(1).SetPricingComplete(true).SaveX(f.ctx)
	day := at.In(f.service.system.TimeLocation(f.ctx)).Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 2.0, page.Slices[0].Entries[0].Metrics.TotalCost)
	f.client.UsageArchive.UpdateOneID(a.ID).SetTotalCost(7).SaveX(f.ctx)
	page, err = f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 7.0, page.Slices[0].Entries[0].Metrics.TotalCost)
}

func TestConsumptionSnapshotDoesNotPublishAcrossCorrection(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().AddDate(0, 0, -3)
	r, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 10, 0, 0, 2, nil)
	injected := false
	f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, saving := m.Field("payload"); saving && !injected {
				injected = true
				f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(7).SaveX(f.ctx)
				version := f.client.ConsumptionSnapshot.Query().OnlyX(f.ctx).SourceVersion
				f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(8).SaveX(f.ctx)
				require.NotEqual(t, version, f.client.ConsumptionSnapshot.Query().OnlyX(f.ctx).SourceVersion, "生成期间后续修正仍推进版本")
			}
			return next.Mutate(ctx, m)
		})
	})
	day := at.In(f.service.system.TimeLocation(f.ctx)).Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	_, err := f.service.Slices(ctx, input, nil, "usage")
	require.NoError(t, err)
	require.NoError(t, f.service.fillConsumptionSnapshot(f.ctx))
	snapshot := f.client.ConsumptionSnapshot.Query().OnlyX(f.ctx)
	require.NotEqual(t, snapshot.SourceVersion, snapshot.SnapshotVersion)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Equal(t, 8.0, page.Slices[0].Entries[0].Metrics.TotalCost)
}

func TestConsumptionSummaryKeepsBillingWithoutPerformance(t *testing.T) {
	f := newConsumptionFixture(t)
	group := f.client.APIKeyGroup.Create().SetName("统计组").SetProjectID(f.project.ID).SaveX(f.ctx)
	f.client.APIKey.UpdateOneID(f.key.ID).SetGroupRef(group.ID).SaveX(f.ctx)
	f.addRequest(t, time.Now().Add(-time.Hour), "model-a", request.StatusCompleted, 100, 20, 10, 0, 2, nil)
	f.addRequest(t, time.Now().Add(-time.Hour), "model-a", request.StatusFailed, 200, 20, 0, 0, 3, nil)
	for _, dimension := range []ConsumptionDimension{ConsumptionDimensionUser, ConsumptionDimensionAPIKey, ConsumptionDimensionAPIKeyGroup, ConsumptionDimensionModel, ConsumptionDimensionChannel} {
		for _, metric := range []ConsumptionMetric{ConsumptionMetricCost, ConsumptionMetricCache} {
			input := ConsumptionFilter{Dimension: dimension, Metric: metric}
			full, err := f.service.queryGroups(f.ctx, f.project.ID, input, nil, time.Now())
			require.NoError(t, err)
			input.OmitPerformance = true
			lean, err := f.service.queryGroups(f.ctx, f.project.ID, input, nil, time.Now())
			require.NoError(t, err)
			require.Len(t, lean, len(full))
			for i := range full {
				a, b := full[i], lean[i]
				a.SuccessCount, a.OutcomeCount, a.FirstTokenSum, a.FirstTokenCount, a.SpeedSum, a.SpeedCount = 0, 0, 0, 0, 0, 0
				b.SuccessCount, b.OutcomeCount, b.FirstTokenSum, b.FirstTokenCount, b.SpeedSum, b.SpeedCount = 0, 0, 0, 0, 0, 0
				require.Equal(t, a, b, "%s/%s", dimension, metric)
			}
		}
	}
}

func TestConsumptionSlicesRollbackAndPaging(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC().AddDate(0, 0, -40)
	r, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 20, 0, 0, 2, nil)
	loc := f.service.system.TimeLocation(f.ctx)
	start, end := at.In(loc).Format("2006-01-02"), at.AddDate(0, 0, 32).In(loc).Format("2006-01-02")
	input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodCustom, StartDate: &start, EndDate: &end, OmitPerformance: true}
	ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
	page, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.Len(t, page.Slices, 31)
	require.NotNil(t, page.Next)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	tx.UsageLog.Update().Where(usagelog.RequestIDEQ(r.ID)).SetTotalCost(9).SaveX(f.ctx)
	require.NoError(t, tx.Rollback())
	again, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	require.True(t, page.Slices[0].GeneratedAt.Equal(again.Slices[0].GeneratedAt))
	next, err := f.readSlices(t, ctx, input, page.Next, "usage")
	require.NoError(t, err)
	require.Len(t, next.Slices, 2)
	require.Nil(t, next.Next)
	_, err = f.service.Slices(context.Background(), input, nil, "usage")
	require.Error(t, err)
	input.OmitPerformance = false
	full, err := f.readSlices(t, ctx, input, nil, "usage")
	require.NoError(t, err)
	// 完整字段与精简字段使用独立汇总槽位。
	require.EqualValues(t, 1, full.Slices[0].Entries[0].Metrics.FirstTokenCount)
	bytes, err := json.Marshal(full)
	require.NoError(t, err)
	require.NotContains(t, string(bytes), "request_body")
}

func (f *consumptionFixture) readSlices(t *testing.T, ctx context.Context, input ConsumptionFilter, after *time.Time, view string) (*ConsumptionSlicePage, error) {
	t.Helper()
	for i := 0; i < 40; i++ {
		page, err := f.service.Slices(ctx, input, after, view)
		if err != nil || !page.Pending {
			return page, err
		}
		require.NoError(t, f.service.fillConsumptionSnapshot(f.ctx))
	}
	t.Fatal("date backfill did not complete")
	return nil, nil
}
