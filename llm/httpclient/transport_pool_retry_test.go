package httpclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type failNextWriteConn struct {
	net.Conn
	fail atomic.Bool
}

func (c *failNextWriteConn) Write(p []byte) (int, error) {
	if c.fail.Load() {
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(p)
}

// net/http may retry a rewindable POST after writing zero bytes on a reused
// connection. Waiting for its replacement connection is not an upload stall
// on the already failed connection, even within the same RoundTrip.
func TestUploadRecoveryIgnoresConnectionFromTransparentRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	secondDial := make(chan struct{})
	finishDial := make(chan struct{})
	defer close(finishDial)
	var dials atomic.Int32
	var first *failNextWriteConn
	pool := newRecoveringTransport(&http.Transport{
		IdleConnTimeout: time.Minute,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if dials.Add(1) == 1 {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				first = &failNextWriteConn{Conn: conn}
				return first, nil
			}
			close(secondDial)
			select {
			case <-ctx.Done():
			case <-finishDial:
			}
			return nil, context.Canceled
		},
	})
	client := NewHttpClientWithClient(&http.Client{Transport: pool})
	defer client.CloseIdleConnections()
	_, err := client.Do(t.Context(), &Request{Method: http.MethodGet, URL: server.URL})
	require.NoError(t, err)
	first.fail.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := &Request{Method: http.MethodPost, URL: server.URL, Body: []byte(`{"message":"once"}`), TransportTrace: new(TransportTrace)}
	done := poolTestAsync(func() error { _, err := client.Do(ctx, request); return err })
	poolTestWait(t, secondDial)
	request.RecoverHTTPUpload()
	got := request.TransportTrace.Snapshot()
	cancel()
	require.Error(t, poolTestWait(t, done))
	require.Empty(t, got.PoolRecovery, "waiting for the retry's connection must not retire the origin pool")
	require.Nil(t, got.ConnectMs)
	require.Zero(t, got.ConnectionID)
	require.Equal(t, 2, got.ConnectionAttempts)
}

type blockingCloseConn struct {
	net.Conn
	started chan struct{}
	finish  <-chan struct{}
	once    sync.Once
}

func (c *blockingCloseConn) Close() error {
	c.once.Do(func() { close(c.started); <-c.finish })
	return c.Conn.Close()
}

func TestRecoveryClosesOutsideTraceAndPoolLocks(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	finish := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(finish) }) }
	defer unblock()
	closing := &blockingCloseConn{Conn: conn, started: make(chan struct{}), finish: finish}
	pool := newRecoveringTransport(&http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return closing, nil
	}})
	generation := pool.newGeneration()
	_, err := generation.transport.DialContext(t.Context(), "tcp", "test.invalid:443")
	require.NoError(t, err)
	origin := &originPool{current: generation}
	pool.origins["https://test.invalid"] = &originPools{lanes: []*originPool{origin}}
	request := &Request{TransportTrace: new(TransportTrace)}
	request.TransportTrace.beginPoolAttempt(generation.id, func() (string, func()) {
		return pool.recoverUpload("https://test.invalid", origin, generation, 1024)
	}, nil)
	request.ReportHTTPProgress(TransportConnected, time.Now())
	done := poolTestAsync(func() error { request.RecoverHTTPUpload(); return nil })
	poolTestWait(t, closing.started)
	// net/http may call GetConn under its pool lock while cleanup closes a
	// connection. Both the trace and the origin map must remain accessible.
	check := poolTestAsync(func() error {
		request.TransportTrace.startConnectionAttempt()
		pool.mu.Lock()
		origin.recoveryBytes = 0 // Another comparable upload has now finished.
		current := origin.current
		pool.mu.Unlock()
		status, _ := pool.recoverUpload("https://test.invalid", origin, current, 1024)
		if status != "previous_pool_draining" {
			return fmt.Errorf("retired sockets still open but recovery returned %s", status)
		}
		return nil
	})
	require.NoError(t, poolTestWait(t, check))
	unblock()
	require.NoError(t, poolTestWait(t, done))
	pool.CloseIdleConnections()
}
