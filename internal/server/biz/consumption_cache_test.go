package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"
)

func TestConsumptionSharedQuerySurvivesCallerCancellation(t *testing.T) {
	s := &ConsumptionService{flight: &singleflight.Group{}}
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	want := &ConsumptionResult{CurrencyCode: "USD"}
	_, err := s.sharedQuery(ctx, "same", func(shared context.Context) (*ConsumptionResult, error) {
		close(started)
		<-release
		if err := shared.Err(); err != nil {
			return nil, err
		}
		return want, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("shared computation did not start")
	}
	second := s.flight.DoChan("same", func() (any, error) { return nil, context.Canceled })
	_, err = s.sharedQuery(ctx, "same", func(context.Context) (*ConsumptionResult, error) {
		t.Error("same key started a duplicate computation")
		return nil, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	result := <-second
	require.NoError(t, result.Err)
	require.Same(t, want, result.Val)
	require.True(t, result.Shared)
	// A panic must settle all waiters instead of terminating the server process.
	_, err = s.sharedQuery(t.Context(), "panic", func(context.Context) (*ConsumptionResult, error) { panic("fixture panic") })
	require.ErrorContains(t, err, "fixture panic")
}
