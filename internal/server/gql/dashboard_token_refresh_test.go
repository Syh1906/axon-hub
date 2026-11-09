package gql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAllTimeTokenRefreshKeepsAuthorizedContext(t *testing.T) {
	r, ctx := newDashboardUsageTestResolver(t)
	InvalidateAllTimeTokenStatsCache()
	t.Cleanup(InvalidateAllTimeTokenStatsCache)
	createDashboardUsageLog(t, ctx, r.client, 1, "refresh", 100, 10, nil, nil)
	first, err := r.TokenStats(ctx)
	require.NoError(t, err)
	require.Equal(t, 100, first.TotalInputTokensAllTime)
	// A cached value must obey the same permission boundary as a cold query.
	denied, err := r.TokenStats(context.Background())
	require.Error(t, err)
	require.Nil(t, denied)
	createDashboardUsageLog(t, ctx, r.client, 1, "refresh", 200, 20, nil, nil)
	allTimeCacheMu.Lock()
	allTimeCacheTime = time.Now().Add(-softTTL - time.Minute)
	allTimeCacheMu.Unlock()
	requestCtx, cancel := context.WithCancel(ctx)
	stale, err := r.TokenStats(requestCtx)
	cancel()
	require.NoError(t, err)
	require.Equal(t, 100, stale.TotalInputTokensAllTime, "stale data is returned while the refresh runs")
	require.Eventually(t, func() bool {
		allTimeCacheMu.RLock()
		defer allTimeCacheMu.RUnlock()
		return allTimeCache != nil && allTimeCache.TotalInputTokensAllTime == 300 && allTimeCache.TotalOutputTokensAllTime == 30
	}, 2*time.Second, 10*time.Millisecond)
}
