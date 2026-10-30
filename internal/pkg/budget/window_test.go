package budget

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func instant(t *testing.T, value string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return v
}

func TestManualResetKeepsOrRestartsFiveHour(t *testing.T) {
	r := Rule{Period: FiveHour, Mode: Fixed}
	anchor := instant(t, "2026-10-30T14:00:00+08:00")
	now := instant(t, "2026-10-30T16:00:00+08:00")
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	for _, tc := range []struct {
		restart bool
		end     string
		cycle   int64
	}{
		{false, "2026-10-30T19:00:00+08:00", 0},
		{true, "2026-10-30T21:00:00+08:00", 1},
	} {
		clock, err := r.Reset(now, Clock{Anchor: &anchor}, loc, tc.restart)
		require.NoError(t, err)
		require.Equal(t, int64(1), clock.ResetVersion)
		require.Equal(t, tc.cycle, clock.CycleVersion)
		window, err := r.Bounds(now, clock, loc)
		require.NoError(t, err)
		require.True(t, window.End.Equal(instant(t, tc.end)))
		// A subsequent charge cannot silently move an already active window.
		clock = r.StartFixedFiveHour(now.Add(time.Hour), clock, 1)
		after, err := r.Bounds(now.Add(time.Hour), clock, loc)
		require.NoError(t, err)
		require.True(t, after.End.Equal(*window.End))
	}
}

func TestFiveHourStartsOnlyOnPositiveSettlement(t *testing.T) {
	r := Rule{Period: FiveHour, Mode: Fixed}
	now := instant(t, "2026-10-30T16:00:00Z")
	clock := r.StartFixedFiveHour(now, Clock{}, 0)
	require.Nil(t, clock.Anchor)
	clock = r.StartFixedFiveHour(now, clock, 1)
	require.Equal(t, now, *clock.Anchor)
	next := now.Add(5 * time.Hour)
	w, err := r.Bounds(next, clock, time.UTC)
	require.NoError(t, err)
	require.Nil(t, w.End)
	clock, err = r.Reset(next, clock, time.UTC, false)
	require.NoError(t, err)
	require.Nil(t, clock.Anchor)
}

func TestNaturalAndRollingWindows(t *testing.T) {
	now := instant(t, "2026-11-02T10:00:00+08:00")
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	for _, tc := range []struct {
		period     Period
		mode       Mode
		start, end string
	}{
		{Weekly, Fixed, "2026-11-02T00:00:00+08:00", "2026-11-09T00:00:00+08:00"},
		{Monthly, Fixed, "2026-11-01T00:00:00+08:00", "2026-12-01T00:00:00+08:00"},
		{Weekly, Rolling, "2026-10-26T10:00:00+08:00", "2026-11-02T10:00:00+08:00"},
		{Monthly, Rolling, "2026-10-03T10:00:00+08:00", "2026-11-02T10:00:00+08:00"},
	} {
		w, err := (Rule{Period: tc.period, Mode: tc.mode}).Bounds(now, Clock{}, loc)
		require.NoError(t, err)
		require.True(t, w.Start.Equal(instant(t, tc.start)), tc.period)
		require.True(t, w.End.Equal(instant(t, tc.end)), tc.period)
		require.Equal(t, tc.mode == Rolling, w.StartExclusive)
	}
}

func TestRestartedMonthPreservesOriginalDay(t *testing.T) {
	r := Rule{Period: Monthly, Mode: Fixed}
	anchor := instant(t, "2028-01-31T16:00:00Z")
	clock, err := r.Reset(anchor, Clock{}, time.UTC, true)
	require.NoError(t, err)
	for _, tc := range []struct{ now, start, end string }{
		{"2028-02-28T18:00:00Z", "2028-01-31T16:00:00Z", "2028-02-29T16:00:00Z"},
		{"2028-02-29T16:00:00Z", "2028-02-29T16:00:00Z", "2028-03-31T16:00:00Z"},
		{"2028-04-30T16:00:00Z", "2028-04-30T16:00:00Z", "2028-05-31T16:00:00Z"},
	} {
		w, err := r.Bounds(instant(t, tc.now), clock, time.UTC)
		require.NoError(t, err)
		require.True(t, w.Start.Equal(instant(t, tc.start)))
		require.True(t, w.End.Equal(instant(t, tc.end)))
	}
}

func TestCalendarDayAndRollingDayAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	now := instant(t, "2026-03-08T12:00:00-04:00")
	w, err := (Rule{Period: Daily, Mode: Fixed}).Bounds(now, Clock{}, loc)
	require.NoError(t, err)
	require.Equal(t, 23*time.Hour, w.End.Sub(*w.Start))
	w, err = (Rule{Period: Daily, Mode: Rolling}).Bounds(now, Clock{}, loc)
	require.NoError(t, err)
	require.Equal(t, 24*time.Hour, w.End.Sub(*w.Start))
}

func TestRestartPreservesRepeatedLocalTimeInstant(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	// The second 01:30 is one hour later than the first during the fall-back.
	now := instant(t, "2026-11-01T01:30:00-05:00")
	for _, period := range []Period{Daily, Weekly, Monthly} {
		t.Run(string(period), func(t *testing.T) {
			rule := Rule{Period: period, Mode: Fixed}
			clock, err := rule.Reset(now, Clock{}, loc, true)
			require.NoError(t, err)
			window, err := rule.Bounds(now, clock, loc)
			require.NoError(t, err)
			require.True(t, window.Start.Equal(now), "restart must preserve the exact instant: %v", window.Start)
			require.False(t, window.Contains(now.Add(-time.Minute)))
		})
	}
}

func TestRollingAndTotalRejectCycleRestart(t *testing.T) {
	now := time.Now()
	for _, r := range []Rule{{Period: Weekly, Mode: Rolling}, {Period: Monthly, Mode: Rolling}, {Period: Total, Mode: Fixed}} {
		_, err := r.Reset(now, Clock{}, time.UTC, true)
		require.Error(t, err)
		clock, err := r.Reset(now, Clock{}, time.UTC, false)
		require.NoError(t, err)
		require.Nil(t, clock.Anchor)
		require.Equal(t, int64(1), clock.ResetVersion)
	}
}

func TestAmountsAndReservationBounds(t *testing.T) {
	a, err := ParseAmount("0.00000001")
	require.NoError(t, err)
	require.Equal(t, Amount(1), a)
	require.Equal(t, "0.00000001", a.String())
	for _, value := range []string{"-1", "0.000000001", "92233720368.54775808", "invalid"} {
		_, err := ParseAmount(value)
		require.Error(t, err, value)
	}
	a, err = ReserveAmount(decimal.RequireFromString("0.000000001"))
	require.NoError(t, err)
	require.Equal(t, Amount(1), a)
	require.True(t, Fits(100, 70, 10, 20))
	require.False(t, Fits(100, 70, 10, 21))
	require.False(t, Fits(100, 100, 0, 0))
	require.True(t, Fits(0, 100, 100, 100))
}
