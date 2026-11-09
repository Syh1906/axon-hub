package gql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/pkg/xtime"
)

func TestDashboardCalendarStatsIncludeArchiveAndBoundaries(t *testing.T) {
	r, ctx := newDashboardUsageTestResolver(t)
	// A month starting mid-week exercises both ranges crossing the month boundary.
	day := time.Date(2026, 4, 1, 16, 0, 0, 0, time.UTC) // April 2 in UTC+8.
	week := day.AddDate(0, 0, -3)
	month := day.AddDate(0, 0, -1)
	lastWeek := week.AddDate(0, 0, -7)
	p := xtime.CalendarPeriods{
		Today: xtime.Period{Start: day}, ThisWeek: xtime.Period{Start: week},
		LastWeek: xtime.Period{Start: lastWeek, End: week}, ThisMonth: xtime.Period{Start: month},
	}
	for i, at := range []time.Time{day, month, week, lastWeek, lastWeek.Add(-time.Second)} {
		r.client.UsageLog.Create().SetProjectID(1).SetRequestID(i + 1).SetChannelID(1).
			SetModelID("calendar").SetCreatedAt(at).SetPromptTokens(10).SetCompletionTokens(4).
			SetPromptCachedTokens(3).SetTotalTokens(14).SaveX(ctx)
	}
	// A removed usage row still contributes through the numeric archive.
	r.client.UsageArchive.Create().SetKind(usagearchive.KindUsage).SetSourceID(100).
		SetProjectID(1).SetRequestID(100).SetChannelID(1).SetModelID("calendar").SetOccurredAt(day).
		SetPromptTokens(20).SetCompletionTokens(8).SetPromptCachedTokens(6).SetTotalTokens(28).SaveX(ctx)
	counts, err := r.calendarRequestStats(ctx, p)
	require.NoError(t, err)
	require.Equal(t, &RequestStats{RequestsToday: 2, RequestsThisWeek: 4, RequestsLastWeek: 1, RequestsThisMonth: 3}, counts)
	tokens, err := r.calendarTokenStats(ctx, p)
	require.NoError(t, err)
	require.Equal(t, &TokenStats{
		TotalInputTokensToday: 30, TotalOutputTokensToday: 12, TotalCachedTokensToday: 9,
		TotalInputTokensThisWeek: 50, TotalOutputTokensThisWeek: 20, TotalCachedTokensThisWeek: 15,
		TotalInputTokensThisMonth: 40, TotalOutputTokensThisMonth: 16, TotalCachedTokensThisMonth: 12,
	}, tokens)
}

func TestDashboardCalendarStatsEmpty(t *testing.T) {
	r, ctx := newDashboardUsageTestResolver(t)
	p := xtime.GetCalendarPeriods(time.UTC)
	counts, err := r.calendarRequestStats(ctx, p)
	require.NoError(t, err)
	require.Equal(t, &RequestStats{}, counts)
	tokens, err := r.calendarTokenStats(ctx, p)
	require.NoError(t, err)
	require.Equal(t, &TokenStats{}, tokens)
}
