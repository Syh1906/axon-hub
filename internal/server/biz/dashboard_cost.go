package biz

import (
	"context"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/pkg/xtime"
	"github.com/looplj/axonhub/internal/scopes"
)

type DashboardCostPeriod struct {
	TotalCost     float64 `json:"total_cost"`
	UsageCount    int     `json:"usage_count"`
	UnpricedCount int     `json:"unpriced_count"`
}

type DashboardCostStats struct {
	AllTime      *DashboardCostPeriod
	ThisMonth    *DashboardCostPeriod
	ThisWeek     *DashboardCostPeriod
	Today        *DashboardCostPeriod
	Timezone     string
	CurrencyCode string
}

// Dashboard costs follow the dashboard's global read permission and usage-log
// calendar periods. Null costs remain distinguishable from explicitly free usage.
func QueryDashboardCosts(ctx context.Context, client *ent.Client, periods xtime.CalendarPeriods, timezone string) (*DashboardCostStats, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeReadDashboard); err != nil {
		return nil, err
	}
	ctx = authz.WithScopeDecision(ctx, scopes.ScopeReadDashboard)
	result := &DashboardCostStats{Timezone: timezone}
	for _, item := range []struct {
		period *xtime.Period
		target **DashboardCostPeriod
	}{{nil, &result.AllTime}, {&periods.ThisMonth, &result.ThisMonth}, {&periods.ThisWeek, &result.ThisWeek}, {&periods.Today, &result.Today}} {
		query := client.UsageLog.Query()
		if item.period != nil {
			query.Where(usagelog.CreatedAtGTE(item.period.Start.UTC()), usagelog.CreatedAtLT(item.period.End.UTC()))
		}
		var rows []DashboardCostPeriod
		err := query.Modify(func(q *sql.Selector) {
			q.Select(sql.As("COALESCE(SUM("+q.C(usagelog.FieldTotalCost)+"), 0)", "total_cost"),
				sql.As(sql.Count("*"), "usage_count"),
				sql.As("COALESCE(SUM(CASE WHEN "+q.C(usagelog.FieldTotalCost)+" IS NULL THEN 1 ELSE 0 END), 0)", "unpriced_count"))
		}).Scan(ctx, &rows)
		if err != nil {
			return nil, err
		}
		*item.target = &rows[0]
	}
	return result, nil
}
