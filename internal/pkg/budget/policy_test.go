package budget

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEveryConfiguredWindowAndGroupRemainConstraints(t *testing.T) {
	now := instant(t, "2026-10-30T16:00:00Z")
	key := Policy{Timezone: "UTC", Rules: []Rule{
		{Period: Daily, Mode: Fixed, Limit: 60},
		{Period: Monthly, Mode: Rolling, Limit: 400},
	}}
	group := Policy{Timezone: "UTC", Rules: []Rule{{Period: Daily, Mode: Fixed, Limit: 100}}}
	keyUsage := map[Period]Usage{Daily: {Settled: 20}}
	groupUsage := map[Period]Usage{Daily: {Settled: 70}}
	blocked, err := Check(now, "key", key, keyUsage, 40)
	require.NoError(t, err)
	require.Nil(t, blocked)
	blocked, err = Check(now, "group", group, groupUsage, 40)
	require.NoError(t, err)
	require.Equal(t, &Blocked{Scope: "group", Period: Daily, Reason: "insufficient_budget"}, blocked)
	keyUsage[Monthly] = Usage{Settled: 400}
	blocked, err = Check(now, "key", key, keyUsage, 1)
	require.NoError(t, err)
	require.Equal(t, Monthly, blocked.Period)
}

func TestReservationsRemainConstraintsAfterAccountingReset(t *testing.T) {
	p := Policy{Timezone: "UTC", Rules: []Rule{{Period: FiveHour, Mode: Fixed, Limit: 10}}}
	blocked, err := Check(time.Now(), "key", p, map[Period]Usage{FiveHour: {Reserved: 8}}, 3)
	require.NoError(t, err)
	require.NotNil(t, blocked)
	blocked, err = Check(time.Now(), "key", p, map[Period]Usage{FiveHour: {Reserved: 8}}, 2)
	require.NoError(t, err)
	require.Nil(t, blocked)
}

func TestExactExpiryAndWindowEdges(t *testing.T) {
	now := instant(t, "2026-10-30T16:00:00Z")
	p := Policy{Timezone: "UTC", ExpiresAt: &now}
	require.False(t, p.Expired(now.Add(-time.Nanosecond)))
	require.True(t, p.Expired(now))
	for _, mode := range []Mode{Fixed, Rolling} {
		w, err := (Rule{Period: Weekly, Mode: mode}).Bounds(now, Clock{}, time.UTC)
		require.NoError(t, err)
		require.Equal(t, mode == Fixed, w.Contains(*w.Start))
		require.Equal(t, mode == Rolling, w.Contains(*w.End))
		require.True(t, w.Contains(w.Start.Add(time.Nanosecond)))
	}
}

func TestInactiveFiveHourIsEmptyButTotalIsUnbounded(t *testing.T) {
	now := instant(t, "2026-10-30T19:00:00Z")
	anchor := now.Add(-5 * time.Hour)
	rule := Rule{Period: FiveHour, Mode: Fixed}
	for _, clock := range []Clock{{}, {Anchor: &anchor}} {
		window, err := rule.Bounds(now, clock, time.UTC)
		require.NoError(t, err)
		require.True(t, window.Empty)
		require.False(t, window.Contains(anchor))
		require.False(t, window.Contains(now))
	}
	window, err := (Rule{Period: Total, Mode: Fixed}).Bounds(now, Clock{}, time.UTC)
	require.NoError(t, err)
	require.False(t, window.Empty)
	require.True(t, window.Contains(anchor))
	require.True(t, window.Contains(now))
}

func TestInvalidPoliciesNeverSilentlyBecomeUnlimited(t *testing.T) {
	for _, p := range []Policy{
		{Timezone: "invalid"},
		{Timezone: "UTC", Rules: []Rule{{Period: Weekly, Mode: "invalid"}}},
		{Timezone: "UTC", Rules: []Rule{{Period: Daily, Mode: Fixed}, {Period: Daily, Mode: Fixed}}},
	} {
		_, err := Check(time.Now(), "key", p, nil, 1)
		require.Error(t, err)
	}
}
