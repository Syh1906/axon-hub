package biz

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/ent/request"
)

func TestConsumptionWorkerBudgetKeepsProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newConsumptionFixture(t)
		loc := f.service.system.TimeLocation(f.ctx)
		at := time.Now().In(loc).AddDate(0, 0, -2)
		start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
		f.addRequest(t, start.Add(time.Hour), "model-a", request.StatusCompleted, 100, 20, 0, 0, 2, nil)
		day := start.Format("2006-01-02")
		input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost,
			Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
		ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
		_, err := f.service.Slices(ctx, input, nil, "usage")
		require.NoError(t, err)
		stalled := false
		f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if from, ok := m.Field("range_start"); m.Op().Is(ent.OpCreate) && ok && from.(time.Time).Equal(start.Add(3*time.Hour)) && !stalled {
					stalled = true
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return next.Mutate(ctx, m)
			})
		})
		filled, err := f.service.fillNextConsumptionSnapshot(f.ctx)
		require.NoError(t, err, "计算额度耗尽不能把完整页面标成失败")
		require.False(t, filled)
		job := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryNotNil()).OnlyX(f.ctx)
		require.True(t, job.Pending)
		require.False(t, job.Failed)
		require.False(t, job.LeaseUntil.After(time.Now()))
		hours := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryIsNil()).AllX(f.ctx)
		require.Len(t, hours, 3)
		page, err := f.readSlices(t, ctx, input, nil, "usage")
		require.NoError(t, err)
		require.Equal(t, 2.0, page.Slices[0].Entries[0].Metrics.TotalCost)
		for _, hour := range hours {
			require.Equal(t, hour.GeneratedAt, f.client.ConsumptionSnapshot.GetX(f.ctx, hour.ID).GeneratedAt)
		}
	})
}

func TestConsumptionWorkerDatesHaveIndependentBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newConsumptionFixture(t)
		loc := f.service.system.TimeLocation(f.ctx)
		at := time.Now().In(loc).AddDate(0, 0, -3)
		start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
		f.addRequest(t, start.Add(time.Hour), "model-a", request.StatusCompleted, 100, 20, 0, 0, 2, nil)
		f.addRequest(t, start.Add(25*time.Hour), "model-a", request.StatusCompleted, 100, 20, 0, 0, 3, nil)
		from, to := start.Format("2006-01-02"), start.AddDate(0, 0, 1).Format("2006-01-02")
		input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost,
			Period: ConsumptionPeriodCustom, StartDate: &from, EndDate: &to, OmitPerformance: true}
		ctx := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
		_, err := f.service.Slices(ctx, input, nil, "usage")
		require.NoError(t, err)
		f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if from, ok := m.Field("range_start"); m.Op().Is(ent.OpCreate) && ok {
					switch {
					case from.(time.Time).Equal(start):
						time.Sleep(20 * time.Second)
					case from.(time.Time).Equal(start.AddDate(0, 0, 1)):
						time.Sleep(10 * time.Second)
					}
					// Simulate the existing viewer heartbeat using this transaction,
					// so the next date remains requested while fake time advances.
					f.service.demand.Store(time.Now().Add(time.Minute).UnixNano())
					if err := m.(consumptionMutation).Client().ConsumptionSnapshot.Update().SetRequestedAt(time.Now()).Exec(ctx); err != nil {
						return nil, err
					}
				}
				return next.Mutate(ctx, m)
			})
		})
		require.NoError(t, f.service.fillRequestedConsumptionSnapshots(f.ctx))
		page, err := f.service.Slices(ctx, input, nil, "usage")
		require.NoError(t, err)
		require.False(t, page.Pending, "两天分别耗时 20 秒和 10 秒，不应共享 25 秒期限")
		require.Len(t, page.Slices, 2)
		require.Equal(t, 5.0, page.Slices[0].Entries[0].Metrics.TotalCost+page.Slices[1].Entries[0].Metrics.TotalCost)
	})
}

func TestConsumptionWorkerPreservesRealFailures(t *testing.T) {
	for _, scenario := range []string{"caller canceled", "caller deadline", "business error at budget"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newConsumptionFixture(t)
				day := time.Now().In(f.service.system.TimeLocation(f.ctx)).AddDate(0, 0, -2).Format("2006-01-02")
				input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost,
					Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
				viewer := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
				_, err := f.service.Slices(viewer, input, nil, "usage")
				require.NoError(t, err)
				ctx, cancel := context.WithCancel(f.ctx)
				if scenario == "caller deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(f.ctx, 5*time.Second)
				}
				defer cancel()
				businessError := errors.New("fixture persistent calculation error")
				f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if m.Op().Is(ent.OpCreate) {
							if scenario == "caller canceled" {
								cancel()
							}
							<-ctx.Done()
							if scenario == "business error at budget" {
								return nil, businessError
							}
							return nil, ctx.Err()
						}
						return next.Mutate(ctx, m)
					})
				})
				_, err = f.service.fillNextConsumptionSnapshot(ctx)
				if scenario == "business error at budget" {
					require.ErrorIs(t, err, businessError, "真正的计算错误不能因为同时到期而被吞掉")
				} else {
					require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded), "%v", err)
				}
				job := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryNotNil()).OnlyX(f.ctx)
				require.True(t, job.Failed)
				require.True(t, job.LeaseUntil.After(time.Now()))
			})
		})
	}
}

func TestConsumptionWorkerBudgetDuringPublishReleasesLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newConsumptionFixture(t)
		day := time.Now().In(f.service.system.TimeLocation(f.ctx)).AddDate(0, 0, -2).Format("2006-01-02")
		input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost,
			Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
		viewer := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
		_, err := f.service.Slices(viewer, input, nil, "usage")
		require.NoError(t, err)
		stalled := false
		f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if _, payload := m.Field("payload"); payload && m.Op().Is(ent.OpUpdate|ent.OpUpdateOne) && !stalled {
					stalled = true
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return next.Mutate(ctx, m)
			})
		})
		_, err = f.service.fillNextConsumptionSnapshot(f.ctx)
		require.NoError(t, err, "发布结果时预算到期也应释放租约续算")
		job := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryNotNil()).OnlyX(f.ctx)
		require.True(t, job.Pending)
		require.False(t, job.Failed)
		require.False(t, job.LeaseUntil.After(time.Now()), "不能多等剩余的一分钟租约")
		page, err := f.readSlices(t, viewer, input, nil, "usage")
		require.NoError(t, err)
		require.False(t, page.Pending)
	})
}

func TestConsumptionWorkerBudgetDuringCheckReleasesLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newConsumptionFixture(t)
		day := time.Now().In(f.service.system.TimeLocation(f.ctx)).AddDate(0, 0, -2).Format("2006-01-02")
		input := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost,
			Period: ConsumptionPeriodCustom, StartDate: &day, EndDate: &day, OmitPerformance: true}
		viewer := contexts.WithProjectID(contexts.WithUser(f.ctx, f.user), f.project.ID)
		_, err := f.readSlices(t, viewer, input, nil, "usage")
		require.NoError(t, err)
		job := f.client.ConsumptionSnapshot.Query().Where(consumptionsnapshot.QueryNotNil()).OnlyX(f.ctx)
		f.client.ConsumptionSnapshot.UpdateOneID(job.ID).SetPending(true).SetRequestedAt(time.Now()).ExecX(f.ctx)
		f.service.demand.Store(time.Now().Add(time.Minute).UnixNano())
		stalled := false
		f.client.ConsumptionSnapshot.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if pending, ok := m.Field("pending"); ok && pending == false && !stalled {
					stalled = true
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return next.Mutate(ctx, m)
			})
		})
		_, err = f.service.fillNextConsumptionSnapshot(f.ctx)
		require.NoError(t, err)
		current := f.client.ConsumptionSnapshot.GetX(f.ctx, job.ID)
		require.True(t, current.Pending)
		require.False(t, current.Failed)
		require.False(t, current.LeaseUntil.After(time.Now()))
		page, err := f.readSlices(t, viewer, input, nil, "usage")
		require.NoError(t, err)
		require.False(t, page.Pending)
		require.Equal(t, job.GeneratedAt, f.client.ConsumptionSnapshot.GetX(f.ctx, job.ID).GeneratedAt)
	})
}
