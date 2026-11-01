package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/eko/gocache/lib/v4/store"
	gocache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/pkg/xredis"
)

func testSessionOwnerService(t *testing.T) (*RequestService, *gocache.Cache, *gocache.Cache) {
	t.Helper()
	owners := gocache.New(0, 0)
	previous := gocache.New(0, 0)
	return &RequestService{
		sessionOwnerCache:    xcache.NewMemory[SessionOwner](owners),
		previousChannelCache: xcache.NewMemory[int](previous),
	}, owners, previous
}

func testSessionOwnerKeys(threadID, traceID int) (string, string) {
	if threadID > 0 {
		return sessionOwnerKey(threadID, traceID), buildPreviousThreadChannelCacheKey(threadID)
	}
	return sessionOwnerKey(threadID, traceID), buildPreviousTraceChannelCacheKey(traceID)
}

func testLegacySessionOwnerCacheKey(threadID, traceID int) string {
	if threadID > 0 {
		return fmt.Sprintf("axonhub:routing:session-owner:v1:thread:%d", threadID)
	}
	return fmt.Sprintf("axonhub:routing:session-owner:v1:trace:%d", traceID)
}

func setTestSessionOwner(svc *RequestService, ctx context.Context, threadID, traceID int, owner SessionOwner) {
	svc.UpdateSessionOwner(ctx, threadID, traceID, func(SessionOwner, bool) (SessionOwner, bool) {
		return owner, true
	})
}

func TestSessionOwnerThreadIsAuthoritative(t *testing.T) {
	svc, _, _ := testSessionOwnerService(t)
	ctx := t.Context()
	require.NoError(t, svc.sessionOwnerCache.Set(ctx, buildSessionTraceOwnerCacheKey(17), SessionOwner{ChannelID: 9, ExpiresAt: time.Now().Add(time.Minute)}))
	require.NoError(t, svc.previousChannelCache.Set(ctx, buildPreviousTraceChannelCacheKey(17), 10))
	owner, found, err := svc.GetSessionOwner(ctx, 23, 17)
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, owner)

	expected := SessionOwner{ChannelID: 4, ConsecutiveFailovers: 1, ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, svc.sessionOwnerCache.Set(ctx, buildSessionThreadOwnerCacheKey(23), expected))
	owner, found, err = svc.GetSessionOwner(ctx, 23, 17)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, expected, owner)
}

func TestSessionOwnerLegacyDoesNotSeed(t *testing.T) {
	svc, owners, previous := testSessionOwnerService(t)
	ctx := t.Context()
	require.NoError(t, svc.previousChannelCache.Set(ctx, buildPreviousTraceChannelCacheKey(7), 99, store.WithExpiration(30*time.Minute)))
	require.NoError(t, svc.previousChannelCache.Set(ctx, buildPreviousThreadChannelCacheKey(5), 12, store.WithExpiration(30*time.Minute)))
	previousItems := previous.Items()
	for _, ids := range [][2]int{{5, 7}, {0, 7}, {0, 0}} {
		for range 2 {
			owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
			require.NoError(t, err)
			require.False(t, found, "upgrade must reevaluate a legacy binding without a deadline")
			require.Zero(t, owner)
		}
	}
	require.Empty(t, owners.Items())
	require.Equal(t, previousItems, previous.Items(), "reads must not refresh legacy expiration")
}

func TestSessionOwnerCommitRefreshesBothKeyVersions(t *testing.T) {
	svc, owners, previous := testSessionOwnerService(t)
	ctx := t.Context()
	owner := SessionOwner{ChannelID: 42, ConsecutiveFailovers: 1}
	setTestSessionOwner(svc, ctx, 5, 7, owner)
	cachedOwner, found, err := svc.GetSessionOwner(ctx, 5, 7)
	require.NoError(t, err)
	require.True(t, found)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), cachedOwner.ExpiresAt, 5*time.Second)
	owner.ExpiresAt = cachedOwner.ExpiresAt
	for _, key := range []string{buildSessionThreadOwnerCacheKey(5), buildSessionTraceOwnerCacheKey(7)} {
		cached, err := svc.sessionOwnerCache.Get(ctx, key)
		require.NoError(t, err)
		require.Equal(t, owner, cached)
		_, expireAt, ok := owners.GetWithExpiration(key)
		require.True(t, ok)
		require.WithinDuration(t, owner.ExpiresAt.Add(30*time.Minute), expireAt, 5*time.Second)
	}
	for _, key := range []string{buildPreviousThreadChannelCacheKey(5), buildPreviousTraceChannelCacheKey(7)} {
		cached, err := svc.previousChannelCache.Get(ctx, key)
		require.NoError(t, err)
		require.Equal(t, owner.ChannelID, cached)
		_, expireAt, ok := previous.GetWithExpiration(key)
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(30*time.Minute), expireAt, 5*time.Second)
	}
}

func TestSessionOwnerDefaultWindowExpiresLogicallyButRetainsIdentity(t *testing.T) {
	svc, owners, previous := testSessionOwnerService(t)
	ctx := t.Context()
	setTestSessionOwner(svc, ctx, 5, 7, SessionOwner{ChannelID: 42})
	owner, found, err := svc.GetSessionOwner(ctx, 5, 7)
	require.NoError(t, err)
	require.True(t, found)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), owner.ExpiresAt, 5*time.Second)

	// Move only the logical deadline into the past; no wall-clock wait or
	// physical eviction is needed to exercise expiration.
	owner.ExpiresAt = time.Now().Add(-time.Minute)
	for _, key := range []string{buildSessionThreadOwnerCacheKey(5), buildSessionTraceOwnerCacheKey(7)} {
		require.NoError(t, svc.cacheSessionOwner(ctx, key, owner))
		_, physicalExpiry, ok := owners.GetWithExpiration(key)
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(30*time.Minute), physicalExpiry, 5*time.Second)
	}
	for _, ids := range [][2]int{{5, 7}, {0, 7}} {
		key, previousKey := testSessionOwnerKeys(ids[0], ids[1])
		_, physicalExpiry, ok := owners.GetWithExpiration(key)
		require.True(t, ok)
		for range 2 {
			got, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
			require.NoError(t, err)
			require.False(t, found)
			require.Zero(t, got)
		}
		cached, afterRead, ok := owners.GetWithExpiration(key)
		require.True(t, ok, "logical expiration must retain the owner's identity")
		require.Equal(t, owner, cached)
		require.Equal(t, physicalExpiry, afterRead, "reads must not prolong retention")
		legacy, _, ok := previous.GetWithExpiration(previousKey)
		require.True(t, ok, "legacy is still physically live when the owner logically expires")
		require.Equal(t, owner.ChannelID, legacy)
	}
}

func TestSessionOwnerExplicitOneHourWindow(t *testing.T) {
	svc, owners, previous := testSessionOwnerService(t)
	ctx := t.Context()
	owner := SessionOwner{ChannelID: 42, ExpiresAt: time.Now().Add(time.Hour)}
	setTestSessionOwner(svc, ctx, 5, 7, owner)
	for _, ids := range [][2]int{{5, 7}, {0, 7}} {
		got, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, owner, got, "explicit deadlines must not be replaced with the default")
		key, previousKey := testSessionOwnerKeys(ids[0], ids[1])
		_, physicalExpiry, ok := owners.GetWithExpiration(key)
		require.True(t, ok)
		require.WithinDuration(t, owner.ExpiresAt.Add(30*time.Minute), physicalExpiry, 5*time.Second)
		_, legacyExpiry, ok := previous.GetWithExpiration(previousKey)
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(30*time.Minute), legacyExpiry, 5*time.Second)
	}
}

func TestSessionOwnerTemporaryFailoverDoesNotRenewEitherDeadline(t *testing.T) {
	svc, owners, previous := testSessionOwnerService(t)
	ctx := t.Context()
	owner := SessionOwner{ChannelID: 42, ExpiresAt: time.Now().Add(2 * time.Minute)}
	setTestSessionOwner(svc, ctx, 5, 7, owner)
	legacyExpiries := make(map[string]time.Time)
	for _, key := range []string{buildPreviousThreadChannelCacheKey(5), buildPreviousTraceChannelCacheKey(7)} {
		// A shorter existing TTL makes an accidental 30-minute refresh visible.
		require.NoError(t, svc.previousChannelCache.Set(ctx, key, owner.ChannelID, store.WithExpiration(10*time.Minute)))
		_, expiry, ok := previous.GetWithExpiration(key)
		require.True(t, ok)
		legacyExpiries[key] = expiry
	}
	for count := 1; count <= 2; count++ {
		svc.UpdateSessionOwner(ctx, 5, 7, func(current SessionOwner, found bool) (SessionOwner, bool) {
			require.True(t, found)
			require.Equal(t, owner.ExpiresAt, current.ExpiresAt)
			current.ConsecutiveFailovers++
			return current, true
		})
		owner.ConsecutiveFailovers = count
		for _, key := range []string{buildSessionThreadOwnerCacheKey(5), buildSessionTraceOwnerCacheKey(7)} {
			cached, err := svc.sessionOwnerCache.Get(ctx, key)
			require.NoError(t, err)
			require.Equal(t, owner, cached)
			_, expiry, ok := owners.GetWithExpiration(key)
			require.True(t, ok)
			require.WithinDuration(t, owner.ExpiresAt.Add(30*time.Minute), expiry, 5*time.Second)
		}
		for key, expiry := range legacyExpiries {
			cached, actualExpiry, ok := previous.GetWithExpiration(key)
			require.True(t, ok)
			require.Equal(t, owner.ChannelID, cached)
			require.Equal(t, expiry, actualExpiry, "temporary failover must not refresh previous-channel TTL")
		}
	}
}

func TestSessionOwnerSuccessfulCompletionRenewsBothKeys(t *testing.T) {
	for _, ttl := range []time.Duration{5 * time.Minute, time.Hour} {
		t.Run(ttl.String(), func(t *testing.T) {
			svc, owners, previous := testSessionOwnerService(t)
			ctx := t.Context()
			owner := SessionOwner{ChannelID: 42, ConsecutiveFailovers: 2, ExpiresAt: time.Now().Add(time.Minute)}
			setTestSessionOwner(svc, ctx, 5, 7, owner)
			for _, key := range []string{buildPreviousThreadChannelCacheKey(5), buildPreviousTraceChannelCacheKey(7)} {
				require.NoError(t, svc.previousChannelCache.Set(ctx, key, owner.ChannelID, store.WithExpiration(time.Minute)))
			}
			renewed := SessionOwner{ChannelID: owner.ChannelID, ExpiresAt: time.Now().Add(ttl)}
			svc.UpdateSessionOwner(ctx, 5, 7, func(current SessionOwner, found bool) (SessionOwner, bool) {
				require.True(t, found)
				require.Equal(t, owner, current)
				return renewed, true
			})
			for _, ids := range [][2]int{{5, 7}, {0, 7}} {
				got, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, renewed, got)
				key, previousKey := testSessionOwnerKeys(ids[0], ids[1])
				_, physicalExpiry, ok := owners.GetWithExpiration(key)
				require.True(t, ok)
				require.WithinDuration(t, renewed.ExpiresAt.Add(30*time.Minute), physicalExpiry, 5*time.Second)
				cached, legacyExpiry, ok := previous.GetWithExpiration(previousKey)
				require.True(t, ok)
				require.Equal(t, renewed.ChannelID, cached)
				require.WithinDuration(t, time.Now().Add(30*time.Minute), legacyExpiry, 5*time.Second)
			}
		})
	}
}

func TestSessionOwnerV1DoesNotSeed(t *testing.T) {
	for _, ids := range [][2]int{{5, 7}, {0, 7}} {
		key := testLegacySessionOwnerCacheKey(ids[0], ids[1])
		t.Run(key, func(t *testing.T) {
			svc, owners, previous := testSessionOwnerService(t)
			ctx := t.Context()
			legacy := SessionOwner{ChannelID: 42, ConsecutiveFailovers: 2}
			require.NoError(t, svc.sessionOwnerCache.Set(ctx, key, legacy, store.WithExpiration(30*time.Minute)))
			ownerItems := owners.Items()
			for range 2 {
				owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
				require.NoError(t, err)
				require.False(t, found)
				require.Zero(t, owner)
			}
			require.Equal(t, ownerItems, owners.Items(), "v1 reads must neither import nor refresh the old value")
			require.Empty(t, previous.Items())
			fresh := SessionOwner{ChannelID: 9, ExpiresAt: time.Now().Add(5 * time.Minute)}
			svc.UpdateSessionOwner(ctx, ids[0], ids[1], func(current SessionOwner, found bool) (SessionOwner, bool) {
				require.False(t, found)
				require.Zero(t, current)
				return fresh, true
			})
			owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, fresh, owner, "a new successful selection establishes the v2 deadline")
			cached, err := svc.sessionOwnerCache.Get(ctx, key)
			require.NoError(t, err)
			require.Equal(t, legacy, cached)
			require.Equal(t, ownerItems[key], owners.Items()[key], "new success must not refresh the old v1 cache")
		})
	}
}

func TestSessionOwnerV2MissingDeadlineIsRejected(t *testing.T) {
	for _, ids := range [][2]int{{5, 7}, {0, 7}} {
		key, previousKey := testSessionOwnerKeys(ids[0], ids[1])
		t.Run(key, func(t *testing.T) {
			svc, owners, previous := testSessionOwnerService(t)
			ctx := t.Context()
			invalid := SessionOwner{ChannelID: 42, ConsecutiveFailovers: 2}
			require.NoError(t, svc.sessionOwnerCache.Set(ctx, key, invalid, store.WithExpiration(30*time.Minute)))
			require.NoError(t, svc.previousChannelCache.Set(ctx, previousKey, 99, store.WithExpiration(30*time.Minute)))
			ownerItems, previousItems := owners.Items(), previous.Items()
			for range 2 {
				owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
				require.NoError(t, err)
				require.False(t, found, "v2 values without a provable deadline must fail safe")
				require.Zero(t, owner)
			}
			svc.UpdateSessionOwner(ctx, ids[0], ids[1], func(current SessionOwner, found bool) (SessionOwner, bool) {
				require.False(t, found)
				require.Zero(t, current)
				return current, false
			})
			require.Equal(t, ownerItems, owners.Items())
			require.Equal(t, previousItems, previous.Items())
			fresh := SessionOwner{ChannelID: 9, ExpiresAt: time.Now().Add(5 * time.Minute)}
			svc.UpdateSessionOwner(ctx, ids[0], ids[1], func(current SessionOwner, found bool) (SessionOwner, bool) {
				require.False(t, found)
				require.Zero(t, current)
				return fresh, true
			})
			owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, fresh, owner)
		})
	}
}

func TestSessionOwnerExpiredThreadDoesNotFallBack(t *testing.T) {
	svc, _, _ := testSessionOwnerService(t)
	ctx := t.Context()
	expired := SessionOwner{ChannelID: 42, ConsecutiveFailovers: 2, ExpiresAt: time.Now().Add(-time.Minute)}
	require.NoError(t, svc.cacheSessionOwner(ctx, buildSessionThreadOwnerCacheKey(5), expired))
	traceOwner := SessionOwner{ChannelID: 99, ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, svc.cacheSessionOwner(ctx, buildSessionTraceOwnerCacheKey(7), traceOwner))
	require.NoError(t, svc.previousChannelCache.Set(ctx, buildPreviousThreadChannelCacheKey(5), 12))
	require.NoError(t, svc.previousChannelCache.Set(ctx, buildPreviousTraceChannelCacheKey(7), 13))
	// Even an erroring legacy cache must not be consulted for a tombstone.
	svc.previousChannelCache = legacyChannelReadErrorCache{Cache: svc.previousChannelCache, err: errors.New("unexpected legacy lookup")}
	owner, found, err := svc.GetSessionOwner(ctx, 5, 7)
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, owner)
	called := false
	svc.UpdateSessionOwner(ctx, 5, 7, func(current SessionOwner, found bool) (SessionOwner, bool) {
		called = true
		require.False(t, found)
		require.Equal(t, expired, current, "updates retain expired identity for the late-completion guard")
		return current, false
	})
	require.True(t, called)
	cached, err := svc.sessionOwnerCache.Get(ctx, buildSessionThreadOwnerCacheKey(5))
	require.NoError(t, err)
	require.Equal(t, expired, cached)
	owner, found, err = svc.GetSessionOwner(ctx, 0, 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, traceOwner, owner)
}

func TestSessionOwnerNewSessionsDoNotInherit(t *testing.T) {
	svc, owners, previous := testSessionOwnerService(t)
	ctx := t.Context()
	old := SessionOwner{ChannelID: 42, ConsecutiveFailovers: 2, ExpiresAt: time.Now().Add(time.Hour)}
	setTestSessionOwner(svc, ctx, 5, 7, old)
	for _, ids := range [][2]int{{6, 7}, {6, 8}, {0, 8}} {
		owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
		require.NoError(t, err)
		require.False(t, found)
		require.Zero(t, owner)
		key, previousKey := testSessionOwnerKeys(ids[0], ids[1])
		_, exists := owners.Get(key)
		require.False(t, exists)
		_, exists = previous.Get(previousKey)
		require.False(t, exists)
	}
	fresh := SessionOwner{ChannelID: 9, ExpiresAt: time.Now().Add(5 * time.Minute)}
	setTestSessionOwner(svc, ctx, 6, 8, fresh)
	for _, ids := range [][2]int{{5, 7}, {0, 7}} {
		owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, old, owner, "binding a new session must not modify the old session")
	}
	owner, found, err := svc.GetSessionOwner(ctx, 6, 8)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, fresh, owner)
}

func testRedisSessionOwnerService(t *testing.T) (*RequestService, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &RequestService{
		sessionOwnerCache:    xcache.NewRedis[SessionOwner](client),
		previousChannelCache: xcache.NewRedis[int](client),
	}, server
}

func TestSessionOwnerOldJSONDecodesWithoutImportThroughXcache(t *testing.T) {
	for _, source := range []string{"v1", "legacy"} {
		t.Run(source, func(t *testing.T) {
			svc, server := testRedisSessionOwnerService(t)
			ctx := t.Context()
			key := buildSessionThreadOwnerCacheKey(5)
			oldKey := testLegacySessionOwnerCacheKey(5, 7)
			previousKey := buildPreviousThreadChannelCacheKey(5)
			legacy := SessionOwner{ChannelID: 42}
			cachedKey, raw := oldKey, `{"channel_id":42,"consecutive_failovers":2}`
			if source == "v1" {
				legacy.ConsecutiveFailovers = 2
			} else {
				cachedKey, raw = previousKey, `42`
			}
			require.NoError(t, server.Set(cachedKey, raw))
			server.SetTTL(cachedKey, 30*time.Minute)
			if source == "v1" {
				decoded, err := svc.sessionOwnerCache.Get(ctx, oldKey)
				require.NoError(t, err)
				require.Equal(t, legacy, decoded, "xcache must still decode old JSON safely")
			} else {
				decoded, err := svc.previousChannelCache.Get(ctx, previousKey)
				require.NoError(t, err)
				require.Equal(t, legacy.ChannelID, decoded)
			}
			for range 2 {
				owner, found, err := svc.GetSessionOwner(ctx, 5, 7)
				require.NoError(t, err)
				require.False(t, found, "old JSON must not be imported into the v2 binding")
				require.Zero(t, owner)
			}
			require.False(t, server.Exists(key))
			cachedRaw, err := server.Get(cachedKey)
			require.NoError(t, err)
			require.Equal(t, raw, cachedRaw)
			require.Equal(t, 30*time.Minute, server.TTL(cachedKey))

			fresh := SessionOwner{ChannelID: 9, ExpiresAt: time.Now().Add(5 * time.Minute)}
			svc.UpdateSessionOwner(ctx, 5, 7, func(current SessionOwner, found bool) (SessionOwner, bool) {
				require.False(t, found)
				require.Zero(t, current)
				return fresh, true
			})
			v2Raw, err := server.Get(key)
			require.NoError(t, err)
			var stored SessionOwner
			require.NoError(t, json.Unmarshal([]byte(v2Raw), &stored))
			require.True(t, fresh.ExpiresAt.Equal(stored.ExpiresAt))
			require.Equal(t, fresh.ChannelID, stored.ChannelID)
			require.Zero(t, stored.ConsecutiveFailovers)
			require.InDelta(t, (35 * time.Minute).Seconds(), server.TTL(key).Seconds(), 5)
			server.FastForward(time.Minute)
			physicalTTL, legacyTTL := server.TTL(key), server.TTL(previousKey)
			for range 2 {
				owner, found, err := svc.GetSessionOwner(ctx, 5, 7)
				require.NoError(t, err)
				require.True(t, found)
				require.True(t, fresh.ExpiresAt.Equal(owner.ExpiresAt))
				require.Equal(t, fresh.ChannelID, owner.ChannelID)
				require.Zero(t, owner.ConsecutiveFailovers)
			}
			require.Equal(t, physicalTTL, server.TTL(key), "v2 reads must not refresh the deadline or physical retention")
			require.Equal(t, legacyTTL, server.TTL(previousKey))
			previous, err := svc.previousChannelCache.Get(ctx, previousKey)
			require.NoError(t, err)
			require.Equal(t, fresh.ChannelID, previous, "successful binding still updates legacy for other strategies")
			if source == "v1" {
				oldRaw, err := server.Get(oldKey)
				require.NoError(t, err)
				require.Equal(t, raw, oldRaw)
				require.Equal(t, 29*time.Minute, server.TTL(oldKey))
			}
		})
	}
}

func TestSessionOwnerInvalidV2JSONDoesNotFallBack(t *testing.T) {
	for _, raw := range []string{`{"channel_id":"42"}`, `{"channel_id":`} {
		t.Run(raw, func(t *testing.T) {
			svc, server := testRedisSessionOwnerService(t)
			key := buildSessionThreadOwnerCacheKey(5)
			require.NoError(t, server.Set(key, raw))
			svc.previousChannelCache = legacyChannelReadErrorCache{Cache: svc.previousChannelCache, err: errors.New("unexpected legacy lookup")}
			owner, found, err := svc.GetSessionOwner(t.Context(), 5, 7)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "unexpected legacy lookup")
			require.False(t, found)
			require.Zero(t, owner)
			cached, err := server.Get(key)
			require.NoError(t, err)
			require.Equal(t, raw, cached, "invalid JSON must not be replaced with a legacy seed")
		})
	}
}

func TestSessionOwnerOldThirtyMinuteCacheExpiresWhileTrulyIdle(t *testing.T) {
	for _, source := range []string{"v1", "legacy"} {
		for _, ids := range [][2]int{{5, 7}, {0, 7}} {
			key, previousKey := testSessionOwnerKeys(ids[0], ids[1])
			t.Run(source+"/"+key, func(t *testing.T) {
				svc, server := testRedisSessionOwnerService(t)
				ctx := t.Context()
				cachedKey := testLegacySessionOwnerCacheKey(ids[0], ids[1])
				if source == "v1" {
					require.NoError(t, svc.sessionOwnerCache.Set(ctx, cachedKey, SessionOwner{ChannelID: 42}, store.WithExpiration(30*time.Minute)))
				} else {
					cachedKey = previousKey
					if ids[0] > 0 {
						svc.setPreviousThreadChannelID(ctx, ids[0], 42)
					} else {
						svc.setPreviousTraceChannelID(ctx, ids[1], 42)
					}
				}
				require.Equal(t, 30*time.Minute, server.TTL(cachedKey))
				// No GetSessionOwner call before physical eviction: this is an
				// untouched old cache whose value has never been reevaluated.
				// Legacy's ordinary 30-minute physical expiry remains valid too.
				server.FastForward(29 * time.Minute)
				require.True(t, server.Exists(cachedKey))
				server.FastForward(2 * time.Minute)
				require.False(t, server.Exists(cachedKey))
				for range 2 {
					owner, found, err := svc.GetSessionOwner(ctx, ids[0], ids[1])
					require.NoError(t, err)
					require.False(t, found)
					require.Zero(t, owner)
				}
				require.False(t, server.Exists(key), "a truly idle old entry must not be resurrected")
				require.False(t, server.Exists(previousKey))
			})
		}
	}
}

func TestSessionOwnerCanceledMutationDoesNotWrite(t *testing.T) {
	svc, owners, previous := testSessionOwnerService(t)
	ctx := t.Context()
	owner := SessionOwner{ChannelID: 42, ExpiresAt: time.Now().Add(time.Minute)}
	setTestSessionOwner(svc, ctx, 5, 7, owner)
	ownerItems := owners.Items()
	previousItems := previous.Items()
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	called := false
	svc.UpdateSessionOwner(canceled, 5, 7, func(current SessionOwner, found bool) (SessionOwner, bool) {
		called = true
		require.True(t, found)
		require.Equal(t, owner, current)
		cancel()
		return SessionOwner{ChannelID: 99, ExpiresAt: time.Now().Add(time.Hour)}, true
	})
	require.True(t, called)
	require.Equal(t, ownerItems, owners.Items())
	require.Equal(t, previousItems, previous.Items())
}

type sessionOwnerReadErrorCache struct {
	xcache.Cache[SessionOwner]
	err error
}

func (c sessionOwnerReadErrorCache) Get(context.Context, any) (SessionOwner, error) {
	return SessionOwner{}, c.err
}

type legacyChannelReadErrorCache struct {
	xcache.Cache[int]
	err error
}

func (c legacyChannelReadErrorCache) Get(context.Context, any) (int, error) {
	return 0, c.err
}

func TestSessionOwnerReadErrorDoesNotFallThroughToTrace(t *testing.T) {
	svc, _, _ := testSessionOwnerService(t)
	ctx := t.Context()
	require.NoError(t, svc.previousChannelCache.Set(ctx, buildPreviousTraceChannelCacheKey(7), 99))
	failure := errors.New("cache unavailable")
	svc.sessionOwnerCache = sessionOwnerReadErrorCache{Cache: svc.sessionOwnerCache, err: failure}
	owner, found, err := svc.GetSessionOwner(ctx, 5, 7)
	require.ErrorIs(t, err, failure)
	require.False(t, found)
	require.Zero(t, owner)

	svc.sessionOwnerCache = xcache.NewNoop[SessionOwner]()
	svc.previousChannelCache = legacyChannelReadErrorCache{Cache: svc.previousChannelCache, err: failure}
	owner, found, err = svc.GetSessionOwner(ctx, 5, 7)
	require.NoError(t, err, "a v2 miss must not consult the legacy cache")
	require.False(t, found)
	require.Zero(t, owner)
}

func TestSessionOwnerUpdateDoesNotWriteOnReadError(t *testing.T) {
	svc, _, _ := testSessionOwnerService(t)
	failure := errors.New("cache down")
	svc.sessionOwnerCache = sessionOwnerReadErrorCache{Cache: svc.sessionOwnerCache, err: failure}
	called := false
	svc.UpdateSessionOwner(t.Context(), 5, 7, func(SessionOwner, bool) (SessionOwner, bool) {
		called = true
		return SessionOwner{ChannelID: 3}, true
	})
	require.False(t, called)
}

func TestSessionOwnerUpdateSerializesConcurrentReadModifyWrite(t *testing.T) {
	svc, _, _ := testSessionOwnerService(t)
	ctx := t.Context()
	expected := SessionOwner{ChannelID: 1, ExpiresAt: time.Now().Add(time.Minute)}
	setTestSessionOwner(svc, ctx, 5, 7, expected)
	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("owner update panic: %v", recovered)
			}
		}()
		svc.UpdateSessionOwner(ctx, 5, 7, func(current SessionOwner, found bool) (SessionOwner, bool) {
			close(entered)
			<-release
			current.ConsecutiveFailovers++
			return current, found
		})
	}()
	<-entered
	go func() {
		defer wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("owner update panic: %v", recovered)
			}
		}()
		svc.UpdateSessionOwner(ctx, 5, 7, func(current SessionOwner, found bool) (SessionOwner, bool) {
			current.ConsecutiveFailovers++
			return current, found
		})
	}()
	close(release)
	wg.Wait()
	owner, found, err := svc.GetSessionOwner(ctx, 5, 7)
	require.NoError(t, err)
	require.True(t, found)
	expected.ConsecutiveFailovers = 2
	require.Equal(t, expected, owner)
}

func TestRoutingDecisionIsPersisted(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:session-routing-decision?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := NewRequestService(client, xcache.Config{Mode: xcache.ModeMemory}, nil, nil, nil, nil, NewRequestPayloadService(RequestPayloadConfig{Directory: t.TempDir()}))
	req, err := client.Request.Create().SetTraceID(7).SetModelID("test-model").SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("completed").Save(ctx)
	require.NoError(t, err)
	decision := &objects.RequestRoutingDecision{
		Owner:                &objects.RoutingDecisionCombo{ChannelID: 4, ChannelName: "primary", ActualModel: "test-model"},
		Skipped:              []objects.RoutingDecisionCombo{{ChannelID: 9, ChannelName: "broken", ActualModel: "test-model", State: "open"}},
		TemporaryFailover:    true,
		ConsecutiveFailovers: 1,
	}
	require.NoError(t, svc.SetRequestRoutingDecision(ctx, req.ID, nil))
	require.NoError(t, svc.SetRequestRoutingDecision(ctx, req.ID, decision))
	stored, err := client.Request.Get(ctx, req.ID)
	require.NoError(t, err)
	require.Equal(t, decision, stored.RoutingDecision)
	require.NoError(t, svc.UpdateRequestChannelIDWithoutSticky(ctx, req.ID, 42))
	stored, err = client.Request.Get(ctx, req.ID)
	require.NoError(t, err)
	require.Equal(t, 42, stored.ChannelID)
	_, err = svc.previousChannelCache.Get(ctx, buildPreviousTraceChannelCacheKey(7))
	require.Error(t, err)
}

func TestSessionOwnerTwoLevelUsesAuthoritativeRedis(t *testing.T) {
	server := miniredis.RunT(t)
	cfg := xcache.Config{Mode: xcache.ModeTwoLevel, Redis: xredis.Config{Addrs: []string{server.Addr()}}}
	first := NewRequestService(nil, cfg, nil, nil, nil, nil, NewRequestPayloadService(RequestPayloadConfig{Directory: t.TempDir()}))
	second := NewRequestService(nil, cfg, nil, nil, nil, nil, NewRequestPayloadService(RequestPayloadConfig{Directory: t.TempDir()}))
	ctx := t.Context()
	setTestSessionOwner(first, ctx, 5, 7, SessionOwner{ChannelID: 10})
	old, found, err := second.GetSessionOwner(ctx, 5, 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 10, old.ChannelID)
	setTestSessionOwner(first, ctx, 5, 7, SessionOwner{ChannelID: 15})
	// A previously read owner must not remain in another service's L1. There
	// is no asynchronous promotion worker for the authoritative owner cache.
	for range 10 {
		current, found, err := second.GetSessionOwner(ctx, 5, 7)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, 15, current.ChannelID)
	}
	require.NotEqual(t, "chain", second.sessionOwnerCache.GetType())
	require.Equal(t, "chain", second.previousChannelCache.GetType(), "other routing strategies retain their cache policy")
	require.Equal(t, xcache.ModeTwoLevel, cfg.Mode)
	withoutRedis := NewRequestService(nil, xcache.Config{Mode: xcache.ModeTwoLevel}, nil, nil, nil, nil, NewRequestPayloadService(RequestPayloadConfig{Directory: t.TempDir()}))
	setTestSessionOwner(withoutRedis, ctx, 5, 7, SessionOwner{ChannelID: 15})
	current, found, err := withoutRedis.GetSessionOwner(ctx, 5, 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 15, current.ChannelID)
}
