package biz

import (
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/pkg/xtime"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/stretchr/testify/require"
)

func TestUsageArchivePreservesStatisticsAndAvoidsDoubleCounting(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().AddDate(0, 0, -90)
	req, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 1000, 100, 800, 100, 2, nil)
	f.addRequest(t, time.Now().Add(-time.Hour), "model-a", request.StatusCompleted, 2000, 200, 0, 0, 3, nil)
	filter := ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey, Metric: ConsumptionMetricCost, Period: ConsumptionPeriodAll}
	before, err := f.service.queryGroups(f.ctx, f.project.ID, filter, nil, time.Now())
	require.NoError(t, err)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	require.NoError(t, ArchiveRequestStatistics(f.ctx, tx.Client(), []int{req.ID}))
	usage := tx.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).Select(ArchivedUsageFields...).AllX(f.ctx)
	require.NoError(t, ArchiveUsageStatistics(f.ctx, tx.Client(), usage))
	tx.UsageLog.Delete().Where(usagelog.RequestIDEQ(req.ID)).ExecX(f.ctx)
	require.NoError(t, tx.Commit())
	after, err := f.service.queryGroups(f.ctx, f.project.ID, filter, nil, time.Now())
	require.NoError(t, err)
	require.Equal(t, before, after)
	// Archiving the same completed request twice is idempotent.
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{req.ID}))
	require.Equal(t, 1+len(usage), f.client.UsageArchive.Query().CountX(f.ctx))
	periods := xtime.GetCalendarPeriods(time.UTC)
	result, err := QueryDashboardCosts(f.viewer(t, f.project.ID, string(scopes.ScopeReadDashboard)), f.client, periods, "UTC")
	require.NoError(t, err)
	require.Equal(t, 5.0, result.AllTime.TotalCost)
}
