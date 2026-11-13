package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsumptionSharedQueryCancellation(t *testing.T) {
	s := &ConsumptionService{flight: &consumptionFlight{}}
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	want := &ConsumptionResult{CurrencyCode: "USD"}
	var calls atomic.Int32
	compute := func(shared context.Context) (*ConsumptionResult, error) {
		calls.Add(1)
		close(started)
		select {
		case <-shared.Done():
			return nil, shared.Err()
		case <-release:
			return want, nil
		}
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				first <- context.Canceled
			}
		}()
		_, err := s.sharedQuery(ctx, "same", compute)
		first <- err
	}()
	<-started
	go func() {
		defer func() {
			if v := recover(); v != nil {
				second <- context.Canceled
			}
		}()
		result, err := s.sharedQuery(t.Context(), "same", compute)
		if err == nil && result != want {
			err = context.Canceled
		}
		second <- err
	}()
	require.Eventually(t, func() bool {
		s.flight.mu.Lock()
		defer s.flight.mu.Unlock()
		return s.flight.calls["same"].waiters == 2
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	close(release)
	require.NoError(t, <-second)
	require.EqualValues(t, 1, calls.Load())
	_, err := s.sharedQuery(t.Context(), "panic", func(context.Context) (*ConsumptionResult, error) { panic("fixture panic") })
	require.ErrorContains(t, err, "fixture panic")
}

func TestConsumptionSharedQueryStopsWithoutWaiters(t *testing.T) {
	s := &ConsumptionService{flight: &consumptionFlight{}}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	_, err := s.sharedQuery(ctx, "last", func(shared context.Context) (*ConsumptionResult, error) {
		cancel()
		<-shared.Done()
		close(stopped)
		return nil, shared.Err()
	})
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("unused query kept running")
	}
	_, err = s.sharedQuery(ctx, "canceled", func(context.Context) (*ConsumptionResult, error) { t.Error("started canceled query"); return nil, nil })
	require.ErrorIs(t, err, context.Canceled)
}
