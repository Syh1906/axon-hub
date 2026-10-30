package biz

import (
	"context"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/pkg/xtime"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/stretchr/testify/require"
)

func TestDashboardCostsCalendarAndUnknownPrices(t *testing.T) {
	f := newConsumptionFixture(t)
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, loc)
	periods := xtime.CalendarPeriods{
		Today:     xtime.Period{Start: day, End: day.AddDate(0, 0, 1)},
		ThisWeek:  xtime.Period{Start: day.AddDate(0, 0, -5), End: day.AddDate(0, 0, 2)},
		ThisMonth: xtime.Period{Start: day.AddDate(0, 0, -30), End: day.AddDate(0, 0, 1)},
	}
	add := func(at time.Time, cost *float64) {
		req, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 0, 0, 0, 0, 0, nil)
		f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(f.channel.ID).
			SetModelID("model-a").SetPromptTokens(10).SetCompletionTokens(1).SetTotalTokens(11).
			SetCreatedAt(at.UTC()).SetNillableTotalCost(cost).SaveX(f.ctx)
	}
	zero, two, three, five, seven := 0.0, 2.0, 3.0, 5.0, 7.0
	add(day, &two)
	add(day.Add(time.Hour), &zero)
	add(day.Add(2*time.Hour), nil)
	add(day.Add(-time.Nanosecond), &three)
	add(day.AddDate(0, 0, -10), &five)
	add(day.AddDate(0, 0, -40), &seven)
	add(periods.Today.End, &seven)
	ctx := f.viewer(t, f.project.ID, string(scopes.ScopeReadDashboard))
	result, err := QueryDashboardCosts(ctx, f.client, periods, loc.String())
	require.NoError(t, err)
	require.Equal(t, &DashboardCostPeriod{TotalCost: 2, UsageCount: 3, UnpricedCount: 1}, result.Today)
	require.Equal(t, 12.0, result.ThisWeek.TotalCost)
	require.Equal(t, 10.0, result.ThisMonth.TotalCost)
	require.Equal(t, 24.0, result.AllTime.TotalCost)
	_, err = QueryDashboardCosts(context.Background(), f.client, periods, loc.String())
	require.Error(t, err)
	f.client.UsageLog.Delete().ExecX(f.ctx)
	result, err = QueryDashboardCosts(ctx, f.client, periods, loc.String())
	require.NoError(t, err)
	require.Equal(t, &DashboardCostPeriod{}, result.Today)
}
