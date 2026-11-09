package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthGateCanceledLockWait(t *testing.T) {
	gate, _, cfg, key := testHealthGate(t)
	lock := gate.observationLock(key.ChannelID)
	require.NoError(t, lock.Acquire(t.Context(), 1))
	defer lock.Release(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	resolve := func() (HealthGateConfig, bool) {
		t.Error("a canceled lock waiter must not read configuration")
		return cfg, true
	}
	_, err := gate.Inspect(ctx, key, resolve)
	require.ErrorIs(t, err, context.Canceled)
	_, ok, err := gate.Begin(ctx, key, resolve, true, true)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ok)
	_, err = gate.Snapshot(ctx, key.ChannelID, resolve)
	require.ErrorIs(t, err, context.Canceled)
}

func TestHealthGateCanceledResolutionDoesNotChangeConfiguration(t *testing.T) {
	gate, _, cfg, key := testHealthGate(t)
	for range cfg.FailureThreshold {
		finishHealthGate(t, gate, key, cfg, HealthGateOutcomeFailure)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := gate.Inspect(ctx, key, func() (HealthGateConfig, bool) {
		cancel()
		return HealthGateConfig{}, true // Failed settings reads can fall back to defaults.
	})
	require.ErrorIs(t, err, context.Canceled)
	requireHealthGateState(t, gate, key, cfg, HealthGateStateOpen)
	require.Equal(t, cfg.FailureThreshold, healthGateSnapshot(t, gate, key.ChannelID, healthGateTestResolver(cfg))[0].ConsecutiveFailures)
}

func TestHealthGateCanceledFinishReleasesOnlyItsProbe(t *testing.T) {
	gate, _, cfg, key := testHealthGate(t)
	for range cfg.FailureThreshold {
		finishHealthGate(t, gate, key, cfg, HealthGateOutcomeFailure)
	}
	old, ok := healthGateBegin(t, gate, key, healthGateTestResolver(cfg), false, true)
	require.True(t, ok)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	lock := gate.observationLock(key.ChannelID)
	require.NoError(t, lock.Acquire(t.Context(), 1))
	err := gate.Finish(ctx, old, healthGateTestResolver(cfg), HealthGateOutcomeSuccess, HealthGateErrorInfo{})
	lock.Release(1)
	require.ErrorIs(t, err, context.Canceled)
	view := healthGateInspect(t, gate, key, healthGateTestResolver(cfg))
	require.False(t, view.ProbeBusy)
	require.Equal(t, HealthGateStateProbing, view.State, "canceled finalization must not falsely recover the channel")
	fresh, ok := healthGateBegin(t, gate, key, healthGateTestResolver(cfg), true, false)
	require.True(t, ok)
	require.ErrorIs(t, gate.Finish(ctx, old, healthGateTestResolver(cfg), HealthGateOutcomeSuccess, HealthGateErrorInfo{}), context.Canceled)
	require.True(t, healthGateInspect(t, gate, key, healthGateTestResolver(cfg)).ProbeBusy, "old completion must not release a new probe")
	healthGateFinish(t, gate, fresh, healthGateTestResolver(cfg), HealthGateOutcomeNeutral, HealthGateErrorInfo{})
}

func TestSessionOwnerCanceledWaitDoesNotUpdate(t *testing.T) {
	lock := ownerUpdateLock(buildSessionThreadOwnerCacheKey(123))
	require.NoError(t, lock.Acquire(t.Context(), 1))
	defer lock.Release(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// No storage is needed: cancellation must return before any cache access.
	service := &RequestService{}
	service.UpdateSessionOwner(ctx, 123, 0, func(SessionOwner, bool) (SessionOwner, bool) {
		t.Error("canceled owner update called its mutation")
		return SessionOwner{ChannelID: 1}, true
	})
}

func TestHealthGateBatchSnapshotReadsConfigurationOnceAndReleasesLocks(t *testing.T) {
	gate, _, cfg, key := testHealthGate(t)
	finishHealthGate(t, gate, key, cfg, HealthGateOutcomeFailure)
	reads := 0
	views, _, err := gate.SnapshotChannels(t.Context(), []int{key.ChannelID + 1, key.ChannelID, key.ChannelID}, func() (map[int]HealthGateConfig, error) {
		reads++
		// Configuration is read only after every involved observation lock is held.
		require.False(t, gate.observationLock(key.ChannelID).TryAcquire(1))
		require.False(t, gate.observationLock(key.ChannelID+1).TryAcquire(1))
		return map[int]HealthGateConfig{key.ChannelID: cfg, key.ChannelID + 1: cfg}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	require.Len(t, views, 2)
	require.Equal(t, 1, views[key.ChannelID][0].ConsecutiveFailures)
	for _, id := range []int{key.ChannelID, key.ChannelID + 1} {
		require.True(t, gate.observationLock(id).TryAcquire(1))
		gate.observationLock(id).Release(1)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, _, err = gate.SnapshotChannels(ctx, []int{key.ChannelID}, func() (map[int]HealthGateConfig, error) {
		cancel()
		return map[int]HealthGateConfig{key.ChannelID: {}}, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, healthGateSnapshot(t, gate, key.ChannelID, healthGateTestResolver(cfg))[0].ConsecutiveFailures)
}
