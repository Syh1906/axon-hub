package xcontext

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDetachedTimeoutKeepsValuesAndOwnsCancellation(t *testing.T) {
	type key struct{}
	original, stopOriginal := context.WithCancelCause(context.WithValue(t.Context(), key{}, "identity"))
	stopOriginal(errors.New("caller disconnected"))

	work, stop := DetachWithTimeout(original, time.Hour)
	defer stop()
	child, stopChild := context.WithCancel(work)
	defer stopChild()
	require.NoError(t, work.Err())
	require.NoError(t, context.Cause(child))
	require.Equal(t, "identity", child.Value(key{}))
	stop()
	require.ErrorIs(t, context.Cause(child), context.Canceled)
	require.Equal(t, "identity", work.Value(key{}))

	expired, cancel := DetachWithTimeout(original, 0)
	defer cancel()
	require.ErrorIs(t, context.Cause(expired), context.DeadlineExceeded)
}
