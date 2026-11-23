package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDialContextScopesValuesToSocketCreation(t *testing.T) {
	type key struct{}
	trace := &httptrace.ClientTrace{GetConn: func(string) {}}
	base := httptrace.WithClientTrace(context.WithValue(t.Context(), key{}, "route"), trace)
	deadline := time.Now().Add(time.Minute)
	base, stop := context.WithDeadline(base, deadline)
	defer stop()
	for _, cancelDuringDial := range []bool{false, true} {
		caller, cancel := context.WithCancelCause(base)
		cause := fmt.Errorf("dial canceled by caller")
		var observed context.Context
		var done <-chan struct{}
		probe := func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			observed, done = dialCtx, dialCtx.Done()
			require.Equal(t, "route", dialCtx.Value(key{}))
			require.Same(t, trace, httptrace.ContextClientTrace(dialCtx))
			got, ok := dialCtx.Deadline()
			require.True(t, ok)
			require.Equal(t, deadline, got)
			require.NoError(t, dialCtx.Err())
			if cancelDuringDial {
				cancel(cause)
				require.ErrorIs(t, context.Cause(dialCtx), cause)
			}
			return nil, dialCtx.Err()
		}
		_, err := dialWithScopedValues(caller, probe, "tcp", "fixture")
		if cancelDuringDial {
			require.ErrorIs(t, err, context.Canceled)
			require.ErrorIs(t, context.Cause(observed), cause)
		} else {
			require.NoError(t, err)
			require.NoError(t, caller.Err())
			require.ErrorIs(t, context.Cause(observed), context.Canceled)
		}
		require.Equal(t, done, observed.Done())
		require.Nil(t, observed.Value(key{}))
		require.Nil(t, httptrace.ContextClientTrace(observed))
		cancel(nil)
	}
}

func poolTestAsync(f func() error) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				done <- fmt.Errorf("test worker panicked: %v", value)
			}
		}()
		done <- f()
	}()
	return done
}

func poolTestWait[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for transport test")
		var zero T
		return zero
	}
}

func poolTestServer(t *testing.T, h2 bool, handler http.HandlerFunc) (*httptest.Server, *HttpClient) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = h2
	server.StartTLS()
	t.Cleanup(server.Close)
	client := NewHttpClientWithProxy(&ProxyConfig{Type: ProxyTypeDisabled}, WithInsecureSkipVerify(true))
	t.Cleanup(client.CloseIdleConnections)
	return server, client
}

func TestResponsesHTTP1EndpointKeepsConnectionsAndScopesRedirects(t *testing.T) {
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("X-Received-Encoding", r.Header.Get("Content-Encoding"))
		_, _ = io.WriteString(w, r.Proto)
	})
	endpoint := server.URL + "/responses"
	body := bytes.Repeat([]byte(" "), minZstdRequestBytes)
	call := func(path string) (*Response, TransportTimings) {
		t.Helper()
		req := &Request{
			Method: http.MethodPost, URL: server.URL + path, ContentType: "application/json", Body: body,
			HTTP1Endpoints: []string{endpoint, server.URL + "/redirect"}, ZstdEndpoints: []string{endpoint},
		}
		resp, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		return resp, req.TransportTrace.Snapshot()
	}
	other, _ := call("/other")
	require.Equal(t, "HTTP/2.0", string(other.Body))
	first, firstTiming := call("/responses")
	require.Equal(t, "HTTP/1.1", string(first.Body))
	require.Equal(t, "zstd", first.Headers.Get("X-Received-Encoding"))
	_, nextTiming := call("/responses")
	require.NotZero(t, firstTiming.ConnectionID)
	require.Equal(t, firstTiming.ConnectionID, nextTiming.ConnectionID)
	require.NotNil(t, nextTiming.Reused)
	require.True(t, *nextTiming.Reused)
	redirected, _ := call("/redirect")
	require.Equal(t, "HTTP/2.0", string(redirected.Body))
	require.Empty(t, redirected.Headers.Get("X-Received-Encoding"))

	// Callers with a per-host socket limit retain one pool across both paths.
	limited := server.Client().Transport.(*http.Transport).Clone()
	limited.MaxConnsPerHost = 1
	bounded := NewHttpClientWithClient(&http.Client{Transport: newRecoveringTransport(limited)})
	t.Cleanup(bounded.CloseIdleConnections)
	for _, path := range []string{"/responses", "/other"} {
		r := &Request{Method: http.MethodPost, URL: server.URL + path, Body: []byte("{}"), HTTP1Endpoints: []string{endpoint}}
		reply, err := bounded.Do(t.Context(), r)
		require.NoError(t, err)
		require.Equal(t, "HTTP/2.0", string(reply.Body))
	}
}

// A response held open while another request's upload stalls is the failure
// mode that makes closing the shared TCP connection unsafe under HTTP/2.
func TestUploadRecoveryDrainsWithoutInterruptingOtherRequests(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", h2), func(t *testing.T) {
			blocked := make(chan struct{}, 2)
			finish := make(chan struct{})
			var finishOnce sync.Once
			unblock := func() { finishOnce.Do(func() { close(finish) }) }
			t.Cleanup(unblock)
			server, client := poolTestServer(t, h2, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hold":
					_, _ = io.WriteString(w, "before|")
					w.(http.Flusher).Flush()
					select {
					case <-finish:
						_, _ = io.WriteString(w, "after")
					case <-r.Context().Done():
					}
				case "/blocked":
					blocked <- struct{}{}
					select {
					case <-finish:
					case <-r.Context().Done():
					}
				default:
					_, _ = io.Copy(io.Discard, r.Body)
					_, _ = io.WriteString(w, "ok")
				}
			})
			// Ensure a failed assertion cannot leave server.Close waiting on hold.
			defer unblock()
			good := &Request{TransportTrace: new(TransportTrace)}
			raw, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/hold", nil)
			require.NoError(t, err)
			response, err := client.GetNativeClient().Do(traceHTTPRequest(good, raw))
			require.NoError(t, err)
			defer response.Body.Close()
			prefix := make([]byte, 7)
			_, err = io.ReadFull(response.Body, prefix)
			require.NoError(t, err)
			require.Equal(t, "before|", string(prefix))
			secondRaw, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/hold", nil)
			require.NoError(t, err)
			secondResponse, err := client.GetNativeClient().Do(secondRaw)
			require.NoError(t, err)
			defer secondResponse.Body.Close()
			_, err = io.ReadFull(secondResponse.Body, make([]byte, 7))
			require.NoError(t, err)
			managed := client.client.Transport.(*recoveringTransport)
			managed.mu.Lock()
			oldPool := managed.origins[server.URL].lanes[0]
			oldGeneration := oldPool.current
			managed.mu.Unlock()

			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "other") }))
			defer other.Close()
			otherBefore := &Request{Method: http.MethodGet, URL: other.URL}
			_, err = client.Do(t.Context(), otherBefore)
			require.NoError(t, err)

			payload := bytes.Repeat([]byte("x"), 16<<20)
			requests := make([]*Request, 2)
			cancels := make([]context.CancelFunc, 2)
			done := make([]<-chan error, 2)
			for i := range requests {
				ctx, cancel := context.WithCancel(t.Context())
				cancels[i] = cancel
				defer cancel()
				requests[i] = &Request{Method: http.MethodPost, URL: server.URL + "/blocked", Body: payload, TransportTrace: new(TransportTrace)}
				request := requests[i]
				done[i] = poolTestAsync(func() error { _, err := client.Do(ctx, request); return err })
			}
			for range requests {
				poolTestWait(t, blocked)
			}
			for _, request := range requests {
				require.False(t, request.TransportTrace.Snapshot().UploadCompleted)
			}
			old := requests[0].TransportTrace.Snapshot()
			require.Equal(t, good.TransportTrace.Snapshot().PoolGeneration, old.PoolGeneration)
			if h2 {
				require.Equal(t, "HTTP/2.0", old.Protocol)
				require.Equal(t, good.TransportTrace.Snapshot().ConnectionID, old.ConnectionID)
			} else {
				require.Equal(t, "HTTP/1.1", old.Protocol)
			}
			startRecovery := make(chan struct{})
			recoveries := make([]<-chan error, len(requests))
			for i, request := range requests {
				recoveries[i] = poolTestAsync(func() error { <-startRecovery; request.RecoverHTTPUpload(); return nil })
			}
			close(startRecovery)
			for _, completed := range recoveries {
				require.NoError(t, poolTestWait(t, completed))
			}
			require.ElementsMatch(t, []string{"pool_replaced", "already_replaced"}, []string{
				requests[0].TransportTrace.Snapshot().PoolRecovery, requests[1].TransportTrace.Snapshot().PoolRecovery,
			})
			for i, cancel := range cancels {
				cancel()
				require.Error(t, poolTestWait(t, done[i]))
			}

			next := &Request{Method: http.MethodGet, URL: server.URL}
			_, err = client.Do(t.Context(), next)
			require.NoError(t, err)
			require.NotEqual(t, old.PoolGeneration, next.TransportTrace.Snapshot().PoolGeneration)
			require.NotEqual(t, old.ConnectionID, next.TransportTrace.Snapshot().ConnectionID)
			otherAfter := &Request{Method: http.MethodGet, URL: other.URL}
			_, err = client.Do(t.Context(), otherAfter)
			require.NoError(t, err)
			require.Equal(t, otherBefore.TransportTrace.Snapshot().ConnectionID, otherAfter.TransportTrace.Snapshot().ConnectionID)

			// Even a comparable successful upload cannot create a third pool
			// while the first pool still carries the long-running response.
			_, err = client.Do(t.Context(), &Request{Method: http.MethodPost, URL: server.URL, Body: payload})
			require.NoError(t, err)
			currentCtx, cancelCurrent := context.WithCancel(t.Context())
			defer cancelCurrent()
			current := &Request{Method: http.MethodPost, URL: server.URL + "/blocked", Body: payload, TransportTrace: new(TransportTrace)}
			currentDone := poolTestAsync(func() error { _, err := client.Do(currentCtx, current); return err })
			poolTestWait(t, blocked)
			current.RecoverHTTPUpload()
			require.Equal(t, "previous_pool_draining", current.TransportTrace.Snapshot().PoolRecovery)
			require.Equal(t, next.TransportTrace.Snapshot().PoolGeneration, current.TransportTrace.Snapshot().PoolGeneration)
			cancelCurrent()
			require.Error(t, poolTestWait(t, currentDone))

			unblock()
			rest, err := io.ReadAll(response.Body)
			require.NoError(t, err, "pool recovery must not interrupt an existing response")
			require.Equal(t, "after", string(rest))
			oldGeneration.connMu.Lock()
			closedBeforeLastResponse := oldGeneration.closed
			oldGeneration.connMu.Unlock()
			require.False(t, closedBeforeLastResponse, "the last unread response still owns the retired pool")
			rest, err = io.ReadAll(secondResponse.Body)
			require.NoError(t, err)
			require.Equal(t, "after", string(rest))
			oldGeneration.connMu.Lock()
			closedAfterLastResponse := oldGeneration.closed
			remainingConns := len(oldGeneration.conns)
			oldGeneration.connMu.Unlock()
			require.True(t, closedAfterLastResponse)
			require.Zero(t, remainingConns, "reclaimed pool must release actual owned connections")
			client.CloseIdleConnections()
			pool := client.client.Transport.(*recoveringTransport)
			pool.mu.Lock()
			remaining := len(pool.origins)
			pool.mu.Unlock()
			require.Zero(t, remaining)
		})
	}
}

func TestUploadRecoveryDoesNotCycleOnNewConnectionFailure(t *testing.T) {
	blocked := make(chan struct{}, 1)
	earlyHeaders := make(chan struct{})
	readUpload := make(chan struct{})
	var readOnce sync.Once
	allowRead := func() { readOnce.Do(func() { close(readUpload) }) }
	finish := make(chan struct{})
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			blocked <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-finish:
			}
			return
		}
		if r.URL.Path == "/early" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(earlyHeaders)
			<-readUpload
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	})
	defer close(finish)
	defer allowRead()
	payload := bytes.Repeat([]byte("x"), 16<<20)
	failUpload := func(want string) TransportTimings {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		request := &Request{Method: http.MethodPost, URL: server.URL + "/blocked", Body: payload, TransportTrace: new(TransportTrace)}
		done := poolTestAsync(func() error { _, err := client.Do(ctx, request); return err })
		poolTestWait(t, blocked)
		request.RecoverHTTPUpload()
		observed := request.TransportTrace.Snapshot()
		require.Equal(t, want, observed.PoolRecovery)
		cancel()
		require.Error(t, poolTestWait(t, done))
		return observed
	}
	first := failUpload("pool_replaced")
	second := failUpload("awaiting_comparable_upload")
	require.NotEqual(t, first.PoolGeneration, second.PoolGeneration)
	_, err := client.Do(t.Context(), &Request{Method: http.MethodPost, URL: server.URL, Body: []byte("small")})
	require.NoError(t, err)
	third := failUpload("awaiting_comparable_upload")
	require.Equal(t, second.PoolGeneration, third.PoolGeneration, "a small successful request does not validate a large upload")
	verified := &Request{Method: http.MethodPost, URL: server.URL + "/early", Body: payload, TransportTrace: new(TransportTrace)}
	verifiedDone := poolTestAsync(func() error { _, err := client.Do(t.Context(), verified); return err })
	poolTestWait(t, earlyHeaders)
	require.False(t, verified.TransportTrace.Snapshot().UploadCompleted, "response headers precede completion of the upload")
	allowRead()
	require.NoError(t, poolTestWait(t, verifiedDone))
	require.Equal(t, "upload_verified", verified.TransportTrace.Snapshot().PoolRecovery)
	fourth := failUpload("pool_replaced")
	require.Equal(t, third.PoolGeneration, fourth.PoolGeneration)
}

func TestUploadRecoveryIgnoresCompletedUploadAndEarlyResponse(t *testing.T) {
	for _, phase := range []TransportPhase{TransportUploaded, TransportHeaders} {
		t.Run(string(phase), func(t *testing.T) {
			request := &Request{TransportTrace: new(TransportTrace)}
			called := false
			request.TransportTrace.beginPoolAttempt(1, func() (string, func()) { called = true; return "pool_replaced", nil }, nil)
			request.ReportHTTPProgress(TransportConnected, time.Now())
			request.ReportHTTPProgress(phase, time.Now())
			request.RecoverHTTPUpload()
			require.False(t, called)
		})
	}
}

func TestTransportPoolReclaimsIdleOriginsAndKeepsActiveBodies(t *testing.T) {
	finish := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "before")
		w.(http.Flusher).Flush()
		select {
		case <-finish:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(finish)
	pool := newRecoveringTransport(&http.Transport{IdleConnTimeout: 20 * time.Millisecond})
	client := &http.Client{Transport: pool}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	require.NoError(t, err)
	defer response.Body.Close()
	client.CloseIdleConnections()
	pool.mu.Lock()
	remaining := len(pool.origins)
	pool.mu.Unlock()
	require.Equal(t, 1, remaining, "response body is still active")
	require.NoError(t, response.Body.Close())
	pool.mu.Lock()
	remaining = len(pool.origins)
	pool.mu.Unlock()
	require.Zero(t, remaining)

	quick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer quick.Close()
	response, err = client.Get(quick.URL)
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Eventually(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return len(pool.origins) == 0
	}, time.Second, 5*time.Millisecond)
}

type deferredCloseBody struct {
	io.Reader
	started chan struct{}
	finish  <-chan struct{}
	once    sync.Once
}

func (b *deferredCloseBody) Close() error {
	b.once.Do(func() { close(b.started) })
	<-b.finish
	return nil
}

func TestTransportPoolClosesAfterDeferredHTTP2Cleanup(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.StartTLS()
	defer server.Close()
	finish := make(chan struct{})
	var finishOnce sync.Once
	unblock := func() { finishOnce.Do(func() { close(finish) }) }
	defer unblock()
	pool := newRecoveringTransport(&http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // Local test certificate.
		ForceAttemptHTTP2: true,
		IdleConnTimeout:   time.Hour,
	})
	client := &http.Client{Transport: pool}
	defer client.CloseIdleConnections()
	body := &deferredCloseBody{Reader: strings.NewReader("request"), started: make(chan struct{}), finish: finish}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, body)
	require.NoError(t, err)
	request.ContentLength = 7
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { unblock(); response.Body.Close() }()
	require.Equal(t, 2, response.ProtoMajor)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	poolTestWait(t, body.started)
	client.CloseIdleConnections()
	unblock()
	require.NoError(t, response.Body.Close())
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("pool was removed but its HTTP/2 connection survived completed transport cleanup")
	}
}

func TestRetiredPoolClosesLateDial(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(finish) }) }
	defer unblock()
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	pool := newRecoveringTransport(&http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		close(started)
		<-finish
		return conn, nil
	}})
	generation := pool.newGeneration(false)
	done := poolTestAsync(func() error {
		_, err := generation.transport.DialContext(t.Context(), "tcp", "late.invalid:443")
		return err
	})
	poolTestWait(t, started)
	closeConnections := generation.prepareClose()
	require.NotNil(t, closeConnections)
	closeConnections()
	unblock()
	require.ErrorIs(t, poolTestWait(t, done), net.ErrClosed)
	_, err := peer.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF, "a late connection must be closed, not returned to a retired pool")
	generation.connMu.Lock()
	remaining := len(generation.conns)
	generation.connMu.Unlock()
	require.Zero(t, remaining)
}
