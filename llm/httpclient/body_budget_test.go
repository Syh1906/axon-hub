package httpclient

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBodyBudgetSharesHTTPTurnsAndGrowingBodies(t *testing.T) {
	const m = LargeBodyThreshold
	b := NewBodyBudget(2 * m)
	first, err := b.Admit(t.Context(), 2*m)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = b.Admit(ctx, m)
	require.ErrorIs(t, err, ErrBodyBudget)
	small, err := b.Admit(t.Context(), 64)
	require.NoError(t, err)
	small.Release()
	waiting := make(chan *BodyLease, 1)
	go func() {
		defer func() {
			if cause := recover(); cause != nil {
				t.Errorf("budget waiter panicked: %v", cause)
				waiting <- nil
			}
		}()
		l, err := b.Admit(t.Context(), m)
		if err != nil {
			t.Errorf("admit waiting body: %v", err)
		}
		waiting <- l
	}()
	first.Release()
	select {
	case l := <-waiting:
		require.NotNil(t, l)
		l.Release()
	case <-time.After(time.Second):
		t.Fatal("waiter did not resume")
	}
	lease, _ := b.Admit(t.Context(), 0)
	_, err = io.Copy(io.Discard, lease.Reader(bytes.NewReader(make([]byte, m+1)), 0))
	require.NoError(t, err)
	warm, err := lease.Retain(nil, m)
	require.NoError(t, err)
	lease.Release()
	other, _ := b.Admit(t.Context(), m)
	r := httptest.NewRequest("POST", "/", nil)
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, err = w.Write(make([]byte, 2*m))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	r.Body = io.NopCloser(&compressed)
	r.Header.Set("Content-Encoding", "gzip")
	grow, _ := b.Admit(t.Context(), -1)
	_, err = InspectBudgetedBody(r, grow)
	require.ErrorIs(t, err, ErrBodyBudget)
	other.Release()
	warm.Release()
	grow.Release()
	all, err := b.Admit(t.Context(), 2*m)
	require.NoError(t, err)
	all.Release()
}
