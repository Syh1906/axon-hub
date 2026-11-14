package httpclient

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func adaptiveFixture(t *testing.T) (*recoveringTransport, *originPools, time.Time) {
	t.Helper()
	transport := newRecoveringTransport(&http.Transport{IdleConnTimeout: time.Minute})
	pools := &originPools{lanes: []*originPool{{current: transport.newGeneration()}}}
	transport.origins["https://upstream.invalid"] = pools
	t.Cleanup(transport.CloseIdleConnections)
	now := time.Now()
	for range 2 {
		upload := &poolUpload{ctx: t.Context(), bytes: 256 << 10, connected: now.Add(-4 * time.Second), connectionID: 1, protocol: "HTTP/2.0"}
		pools.lanes[0].current.attempts[upload] = struct{}{}
	}
	return transport, pools, now
}

func TestAdaptiveUploadIgnoresNonContention(t *testing.T) {
	for _, name := range []string{"http1", "different_connections", "single", "uploaded", "headers", "canceled", "dialing", "fast", "large_but_reasonable", "unknown_body", "reuse_disabled", "connection_cap"} {
		t.Run(name, func(t *testing.T) {
			transport, pools, now := adaptiveFixture(t)
			generation := pools.lanes[0].current
			index := uint64(0)
			for upload := range generation.attempts {
				index++
				switch name {
				case "http1":
					upload.protocol = "HTTP/1.1"
				case "different_connections":
					upload.connectionID = index
				case "single":
					if index == 1 {
						delete(generation.attempts, upload)
					}
				case "uploaded":
					upload.uploaded = now
				case "headers":
					upload.headers = true
				case "canceled":
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					upload.ctx = ctx
				case "dialing":
					upload.connected = time.Time{}
				case "fast":
					upload.connected = now.Add(-time.Second)
				case "large_but_reasonable":
					upload.bytes = 8 << 20
				case "unknown_body":
					upload.bytes = -1
				}
			}
			if name == "reuse_disabled" {
				transport.template.DisableKeepAlives = true
			}
			if name == "connection_cap" {
				transport.template.MaxConnsPerHost = 2
			}
			_, action := transport.chooseUploadPool(pools, now)
			require.Empty(t, action)
			require.Len(t, pools.lanes, 1)
		})
	}
}

func TestAdaptiveUploadNeedsComparableImprovement(t *testing.T) {
	transport, pools, now := adaptiveFixture(t)
	lane, action := transport.chooseUploadPool(pools, now)
	require.Equal(t, "expanded", action)
	require.Len(t, pools.lanes, 2)
	makeUpload := func(size int64, duration time.Duration) *poolUpload {
		return &poolUpload{ctx: t.Context(), bytes: size, protocol: "HTTP/2.0", connected: now, uploaded: now.Add(duration), headers: true, status: 200}
	}
	small := makeUpload(1024, time.Millisecond)
	require.Empty(t, transport.observeUploadProbe(pools, lane, lane.current, small, now.Add(time.Second)))
	require.NotNil(t, pools.probe, "small requests must not certify a larger stalled upload")
	first := makeUpload(256<<10, time.Second)
	require.Empty(t, transport.observeUploadProbe(pools, lane, lane.current, first, now.Add(time.Second)))
	require.Empty(t, transport.observeUploadProbe(pools, lane, lane.current, first, now.Add(time.Second)), "one upload cannot be counted twice")
	require.NotNil(t, pools.probe)
	second := makeUpload(512<<10, 2*time.Second)
	second.headers = false
	require.Empty(t, transport.observeUploadProbe(pools, lane, lane.current, second, now.Add(2*time.Second)))
	second.headers = true
	require.Equal(t, "expansion_verified", transport.observeUploadProbe(pools, lane, lane.current, second, now.Add(2*time.Second)))
	require.Nil(t, pools.probe)
	require.False(t, lane.retiring)
}

func TestAdaptiveUploadRejectsUnhelpfulProbeAndDrains(t *testing.T) {
	for _, name := range []string{"slow", "rejected", "expired", "canceled"} {
		t.Run(name, func(t *testing.T) {
			transport, pools, now := adaptiveFixture(t)
			lane, _ := transport.chooseUploadPool(pools, now)
			upload := &poolUpload{ctx: t.Context(), bytes: 256 << 10, protocol: "HTTP/2.0", connected: now, uploaded: now.Add(4 * time.Second), headers: true, status: 200}
			lane.current.active = 1
			lane.current.attempts[upload] = struct{}{}
			when := now.Add(4 * time.Second)
			if name == "rejected" {
				upload.status = 429
			}
			if name == "canceled" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				upload.ctx = ctx
				require.Empty(t, transport.observeUploadProbe(pools, lane, lane.current, upload, when))
				require.False(t, lane.retiring, "cancellation is not negative transport evidence")
			}
			if name == "expired" || name == "canceled" {
				when = now.Add(uploadProbeLifetime + time.Second)
				transport.chooseUploadPool(pools, when)
			} else {
				require.NotEmpty(t, transport.observeUploadProbe(pools, lane, lane.current, upload, when))
			}
			require.True(t, lane.retiring)
			require.False(t, lane.current.closed, "in-flight response must finish before retirement")
			next, action := transport.chooseUploadPool(pools, when)
			require.NotSame(t, lane, next)
			require.Empty(t, action, "do not expand again during cooldown")
			transport.release("https://upstream.invalid", lane, lane.current, upload)
			require.True(t, lane.current.closed)
			require.Len(t, pools.lanes, 1)
		})
	}
}

func TestAdaptiveUploadBoundAndExistingCapacity(t *testing.T) {
	transport, pools, now := adaptiveFixture(t)
	idle := &originPool{current: transport.newGeneration()}
	pools.lanes = append(pools.lanes, idle)
	chosen, action := transport.chooseUploadPool(pools, now)
	require.Same(t, idle, chosen)
	require.Empty(t, action, "use spare capacity before creating another pool")
	for len(pools.lanes) < maxUploadPools {
		pools.lanes = append(pools.lanes, &originPool{current: transport.newGeneration()})
	}
	for _, lane := range pools.lanes[1:] {
		for range 2 {
			lane.current.attempts[&poolUpload{ctx: t.Context(), bytes: 256 << 10, connected: now.Add(-time.Minute), protocol: "HTTP/2.0", connectionID: lane.current.id}] = struct{}{}
		}
	}
	for range 20 {
		_, action := transport.chooseUploadPool(pools, now)
		require.Empty(t, action)
	}
	require.Len(t, pools.lanes, maxUploadPools)
}

func TestAdaptiveUploadDoesNotExpandUntilProbeIsVerified(t *testing.T) {
	transport, pools, now := adaptiveFixture(t)
	lane, _ := transport.chooseUploadPool(pools, now)
	for range 2 {
		lane.current.attempts[&poolUpload{ctx: t.Context(), bytes: 256 << 10, protocol: "HTTP/2.0", connectionID: 2, connected: now.Add(-time.Minute)}] = struct{}{}
	}
	for range 10 {
		_, action := transport.chooseUploadPool(pools, now.Add(20*time.Second))
		require.Empty(t, action)
	}
	require.Len(t, pools.lanes, 2)
}

func TestAdaptiveUploadLearnsBurstButExpiresEvidence(t *testing.T) {
	for _, name := range []string{"http2", "expired", "dialing", "http1", "unknown_connection"} {
		t.Run(name, func(t *testing.T) {
			transport, pools, now := adaptiveFixture(t)
			lane := pools.lanes[0]
			lane.lastStall = &uploadStall{bytes: 256 << 10, elapsed: 4 * time.Second, observed: now.Add(-time.Second)}
			if name == "expired" {
				lane.lastStall.observed = now.Add(-2 * uploadProbeLifetime)
			}
			// Prior burst has ended. A fresh upload creates concurrent demand again,
			// but has not itself had enough time to be labeled slow.
			clear(lane.current.attempts)
			upload := &poolUpload{ctx: t.Context(), bytes: 256 << 10, connected: now, connectionID: 2, protocol: "HTTP/2.0"}
			switch name {
			case "dialing":
				upload.connected, upload.connectionID, upload.protocol = time.Time{}, 0, ""
			case "http1":
				upload.protocol = "HTTP/1.1"
			case "unknown_connection":
				upload.connectionID = 0
			}
			lane.current.attempts[upload] = struct{}{}
			_, action := transport.chooseUploadPool(pools, now)
			if name != "http2" {
				require.Empty(t, action)
				require.Len(t, pools.lanes, 1)
			} else {
				require.Equal(t, "expanded", action)
				require.Len(t, pools.lanes, 2)
			}
		})
	}
}

func TestAdaptiveUploadReopensAfterBaselineExpiresDuringDrain(t *testing.T) {
	transport, pools, now := adaptiveFixture(t)
	baseline := pools.lanes[0]
	lane, _ := transport.chooseUploadPool(pools, now)
	lane.retiring = true
	lane.current.active = 1
	pools.probe = nil
	transport.removeIdleLocked("https://upstream.invalid", baseline)()
	require.Len(t, pools.lanes, 1)
	chosen, action := transport.chooseUploadPool(pools, now)
	require.NotSame(t, lane, chosen)
	require.Equal(t, "baseline_reopened", action)
	require.False(t, lane.current.closed, "draining work must not be interrupted")
	lane.current.active = 0
}

func TestAdaptiveUploadExpiredIdleProbeClosesOnNextRequest(t *testing.T) {
	for _, idleTimeout := range []time.Duration{0, 90 * time.Second} {
		t.Run(idleTimeout.String(), func(t *testing.T) {
			transport, pools, now := adaptiveFixture(t)
			transport.template.IdleConnTimeout = idleTimeout
			lane, _ := transport.chooseUploadPool(pools, now)
			pools.probe.deadline = now.Add(-time.Second)
			conn, peer := net.Pipe()
			defer peer.Close()
			require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
			owned := &identifiedConn{Conn: conn, owner: lane.current}
			lane.current.conns[owned] = struct{}{}
			pools.lanes[0].current.transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
				return nil, net.ErrClosed // Exercise real admission without an external dial.
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://upstream.invalid", nil)
			require.NoError(t, err)
			_, err = transport.RoundTrip(request)
			require.Error(t, err)
			require.Len(t, pools.lanes, 1, "a retired idle probe must not occupy the expansion budget")
			require.True(t, lane.current.closed)
			_, err = peer.Read(make([]byte, 1))
			require.ErrorIs(t, err, io.EOF, "retirement must close the actual owned connection")
		})
	}
}

func TestAdaptiveUploadRecoveryIsLocalToSelectedLane(t *testing.T) {
	transport, pools, now := adaptiveFixture(t)
	baseline := pools.lanes[0]
	lane, _ := transport.chooseUploadPool(pools, now)
	peerGeneration := lane.current
	status, cleanup := transport.recoverUpload("https://upstream.invalid", baseline, baseline.current, 256<<10)
	require.Equal(t, "pool_replaced", status)
	if cleanup != nil {
		cleanup()
	}
	require.Same(t, peerGeneration, lane.current)
	require.False(t, peerGeneration.closed)
}

func TestAdaptiveUploadSeparatesOriginsAndClientPolicies(t *testing.T) {
	transport, pools, now := adaptiveFixture(t)
	other := &originPools{lanes: []*originPool{{current: transport.newGeneration()}}}
	transport.origins["https://other.invalid"] = other
	transport.chooseUploadPool(pools, now)
	require.Len(t, pools.lanes, 2)
	_, action := transport.chooseUploadPool(other, now)
	require.Empty(t, action)
	require.Len(t, other.lanes, 1)
	independent := newRecoveringTransport(&http.Transport{})
	defer independent.CloseIdleConnections()
	require.Empty(t, independent.origins, "another HTTP client must not inherit learned capacity")
}

func TestAdaptiveUploadReclaimsIdleLaneWhileResponseStaysOpen(t *testing.T) {
	finish := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(finish) }) }
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "before")
		if r.URL.Path == "/hold" {
			w.(http.Flusher).Flush()
			select {
			case <-finish:
				_, _ = io.WriteString(w, "after")
			case <-r.Context().Done():
			}
		}
	})
	defer unblock()
	managed := client.client.Transport.(*recoveringTransport)
	managed.template.IdleConnTimeout = 30 * time.Millisecond
	response, err := client.GetNativeClient().Get(server.URL + "/hold")
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.ReadFull(response.Body, make([]byte, 6))
	require.NoError(t, err)
	managed.mu.Lock()
	pools := managed.origins[server.URL]
	old := pools.lanes[0].current
	lane := &originPool{current: managed.newGeneration()}
	pools.lanes = append(pools.lanes, lane)
	managed.mu.Unlock()
	_, err = client.Do(t.Context(), &Request{Method: http.MethodGet, URL: server.URL + "/quick"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		managed.mu.Lock()
		defer managed.mu.Unlock()
		return len(pools.lanes) == 1 && pools.lanes[0].current == old
	}, time.Second, 5*time.Millisecond)
	unblock()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "after", string(body), "shrinking must not truncate an active response")
}

func TestAdaptiveUploadResetDoesNotRetainOldConnectionEvidence(t *testing.T) {
	upload := &poolUpload{ctx: t.Context(), bytes: 256 << 10, connectionID: 1, protocol: "HTTP/2.0", connected: time.Now(), uploaded: time.Now(), headers: true, status: 200, failed: true, probeCounted: true}
	upload.resetConnection()
	require.Zero(t, upload.connectionID)
	require.Empty(t, upload.protocol)
	require.True(t, upload.connected.IsZero())
	require.True(t, upload.uploaded.IsZero())
	require.False(t, upload.headers)
	require.True(t, upload.pending())
	trace := new(TransportTrace)
	trace.beginPoolAttempt(2, nil, nil)
	trace.setPoolChoice(1, 8, "expansion_verified")
	require.Zero(t, trace.Snapshot().PoolCount, "a late callback must not relabel a redirect")
}

func TestAdaptiveUploadCreatesReusableHTTP2CapacityWithoutReplay(t *testing.T) {
	blocked := make(chan struct{}, 2)
	finish := make(chan struct{})
	var finishOnce sync.Once
	unblock := func() { finishOnce.Do(func() { close(finish) }) }
	var mu sync.Mutex
	calls := map[string]int{}
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/blocked" {
			blocked <- struct{}{}
			select {
			case <-finish:
			case <-r.Context().Done():
			}
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	})
	defer unblock()
	_, err := client.Do(t.Context(), &Request{Method: http.MethodGet, URL: server.URL + "/warm"})
	require.NoError(t, err)
	payload := bytes.Repeat([]byte("x"), 16<<20)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	requests := make([]*Request, 2)
	done := make([]<-chan error, 2)
	for i := range requests {
		request := &Request{Method: http.MethodPost, URL: server.URL + "/blocked", Body: payload, TransportTrace: new(TransportTrace)}
		requests[i] = request
		done[i] = poolTestAsync(func() error { _, err := client.Do(ctx, request); return err })
	}
	for range requests {
		poolTestWait(t, blocked)
	}
	first := requests[0].TransportTrace.Snapshot()
	require.Equal(t, first.ConnectionID, requests[1].TransportTrace.Snapshot().ConnectionID)
	require.Equal(t, "HTTP/2.0", first.Protocol)
	require.False(t, first.UploadCompleted)
	managed := client.client.Transport.(*recoveringTransport)
	managed.mu.Lock()
	pools := managed.origins[server.URL]
	// The server really stalls both uploads. Age only their private detector
	// timestamps instead of making the test wait 64 seconds for 16 MiB bodies.
	for upload := range pools.lanes[0].current.attempts {
		upload.connected = time.Now().Add(-2 * time.Minute)
	}
	managed.mu.Unlock()
	// Even a tiny real request can escape the blocked connection. It must not,
	// however, count as proof that comparable large uploads have improved.
	small := &Request{Method: http.MethodGet, URL: server.URL + "/small"}
	_, err = client.Do(t.Context(), small)
	require.NoError(t, err)
	require.Equal(t, "expanded", small.TransportTrace.Snapshot().PoolAction)
	require.NotEqual(t, first.ConnectionID, small.TransportTrace.Snapshot().ConnectionID)
	var newConnection uint64
	for i := range 2 {
		request := &Request{Method: http.MethodPost, URL: server.URL + "/upload", Body: payload}
		response, err := client.Do(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, "ok", string(response.Body))
		timing := request.TransportTrace.Snapshot()
		require.Equal(t, 2, timing.PoolCount)
		require.NotEqual(t, first.ConnectionID, timing.ConnectionID)
		if i == 0 {
			newConnection = timing.ConnectionID
			require.Empty(t, timing.PoolAction, "the tiny request did not certify large-upload improvement")
		} else {
			require.Equal(t, newConnection, timing.ConnectionID, "new capacity is reusable")
			require.Equal(t, "expansion_verified", timing.PoolAction)
		}
	}
	cancel()
	unblock()
	for _, result := range done {
		require.Error(t, poolTestWait(t, result))
	}
	mu.Lock()
	require.Equal(t, map[string]int{"/warm": 1, "/blocked": 2, "/small": 1, "/upload": 2}, calls, "capacity must not duplicate any request")
	mu.Unlock()
	client.CloseIdleConnections()
	managed.mu.Lock()
	require.Empty(t, managed.origins)
	managed.mu.Unlock()
}

func TestAdaptiveUploadRecordsCompletedBurstEvidence(t *testing.T) {
	started := make(chan struct{}, 2)
	allowRead := make(chan struct{}, 2)
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/burst" {
			started <- struct{}{}
			select {
			case <-allowRead:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := client.Do(ctx, &Request{Method: http.MethodGet, URL: server.URL})
	require.NoError(t, err)
	payload := bytes.Repeat([]byte("x"), 16<<20)
	done := make(chan error, 2)
	for range 2 {
		request := &Request{Method: http.MethodPost, URL: server.URL + "/burst", Body: payload}
		poolTestAsync(func() error { _, err := client.Do(ctx, request); done <- err; return nil })
	}
	for range 2 {
		poolTestWait(t, started)
	}
	managed := client.client.Transport.(*recoveringTransport)
	managed.mu.Lock()
	lane := managed.origins[server.URL].lanes[0]
	for upload := range lane.current.attempts {
		upload.connected = time.Now().Add(-2 * time.Minute)
	}
	managed.mu.Unlock()
	// Drain both request bodies: leaving either completely unread can exhaust
	// the HTTP/2 connection window and prevent its peer from finishing at all.
	allowRead <- struct{}{}
	allowRead <- struct{}{}
	require.NoError(t, poolTestWait(t, done))
	require.NoError(t, poolTestWait(t, done))
	managed.mu.Lock()
	evidence := lane.lastStall
	managed.mu.Unlock()
	require.NotNil(t, evidence, "native WroteRequest must retain the observed slow burst")
	require.Equal(t, int64(len(payload)), evidence.bytes)
}
