package gql

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/pkg/xtime"
	"github.com/looplj/axonhub/internal/server/biz"
)

// Conditional aggregates share one bounded scan across calendar ranges. Keep
// the existing usage-row unit and archive/authorization scope unchanged.
func calendarUsageSum(q *sql.Selector, field, alias string, start time.Time, end *time.Time) sql.Querier {
	return sql.ExprFunc(func(b *sql.Builder) {
		b.WriteString("COALESCE(SUM(CASE WHEN ").Ident(q.C(usagelog.FieldCreatedAt)).WriteString(" >= ").Arg(start)
		if end != nil {
			b.WriteString(" AND ").Ident(q.C(usagelog.FieldCreatedAt)).WriteString(" < ").Arg(*end)
		}
		b.WriteString(" THEN ")
		if field == "" {
			b.WriteString("1")
		} else {
			b.Ident(q.C(field))
		}
		b.WriteString(" ELSE 0 END), 0) AS ").Ident(alias)
	})
}

func (r *queryResolver) calendarRequestStats(ctx context.Context, p xtime.CalendarPeriods) (*RequestStats, error) {
	start := p.LastWeek.Start
	if p.ThisMonth.Start.Before(start) {
		start = p.ThisMonth.Start
	}
	var rows []struct {
		Today     int `json:"today"`
		ThisWeek  int `json:"this_week"`
		LastWeek  int `json:"last_week"`
		ThisMonth int `json:"this_month"`
	}
	err := r.client.UsageLog.Query().Where(usagelog.CreatedAtGTE(start)).Modify(biz.IncludeArchivedUsage).Modify(func(q *sql.Selector) {
		q.SelectExpr(calendarUsageSum(q, "", "today", p.Today.Start, nil),
			calendarUsageSum(q, "", "this_week", p.ThisWeek.Start, nil),
			calendarUsageSum(q, "", "last_week", p.LastWeek.Start, &p.LastWeek.End),
			calendarUsageSum(q, "", "this_month", p.ThisMonth.Start, nil))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	row := rows[0]
	return &RequestStats{RequestsToday: row.Today, RequestsThisWeek: row.ThisWeek, RequestsLastWeek: row.LastWeek, RequestsThisMonth: row.ThisMonth}, nil
}

func (r *queryResolver) calendarTokenStats(ctx context.Context, p xtime.CalendarPeriods) (*TokenStats, error) {
	start := p.ThisMonth.Start
	if p.ThisWeek.Start.Before(start) {
		start = p.ThisWeek.Start
	}
	var rows []struct {
		TodayInput  int `json:"today_input"`
		TodayOutput int `json:"today_output"`
		TodayCached int `json:"today_cached"`
		WeekInput   int `json:"week_input"`
		WeekOutput  int `json:"week_output"`
		WeekCached  int `json:"week_cached"`
		MonthInput  int `json:"month_input"`
		MonthOutput int `json:"month_output"`
		MonthCached int `json:"month_cached"`
	}
	err := r.client.UsageLog.Query().Where(usagelog.CreatedAtGTE(start)).Modify(biz.IncludeArchivedUsage).Modify(func(q *sql.Selector) {
		var expressions []sql.Querier
		for _, period := range []struct {
			name  string
			start time.Time
		}{{"today", p.Today.Start}, {"week", p.ThisWeek.Start}, {"month", p.ThisMonth.Start}} {
			for _, metric := range []struct{ name, field string }{
				{"input", usagelog.FieldPromptTokens}, {"output", usagelog.FieldCompletionTokens}, {"cached", usagelog.FieldPromptCachedTokens},
			} {
				expressions = append(expressions, calendarUsageSum(q, metric.field, period.name+"_"+metric.name, period.start, nil))
			}
		}
		q.SelectExpr(expressions...)
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	row := rows[0]
	return &TokenStats{TotalInputTokensToday: row.TodayInput, TotalOutputTokensToday: row.TodayOutput, TotalCachedTokensToday: row.TodayCached,
		TotalInputTokensThisWeek: row.WeekInput, TotalOutputTokensThisWeek: row.WeekOutput, TotalCachedTokensThisWeek: row.WeekCached,
		TotalInputTokensThisMonth: row.MonthInput, TotalOutputTokensThisMonth: row.MonthOutput, TotalCachedTokensThisMonth: row.MonthCached}, nil
}
