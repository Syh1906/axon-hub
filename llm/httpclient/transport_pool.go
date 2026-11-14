package httpclient

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var nextPoolGeneration atomic.Uint64
var nextConnectionID atomic.Uint64

// recoveringTransport keeps proxy/TLS policy local to its owning HttpClient.
// Each origin starts with one pool and may add independent pools when concurrent
// HTTP/2 uploads stall. Recovery changes only future requests; an active pool
// may have at most one old generation draining. Neither mechanism replays work.
type recoveringTransport struct {
	template  *http.Transport
	tlsConfig *tls.Config // Immutable policy, separate from Transport's lazy HTTP/2 setup.
	mu        sync.Mutex
	origins   map[string]*originPools
}

type originPool struct {
	current       *poolGeneration
	draining      *poolGeneration
	recoveryBytes int64 // A comparable upload must succeed before another recovery.
	timer         *time.Timer
	idleEpoch     uint64
	closeWhenIdle bool
	retiring      bool // Unhelpful expansion: stop admission, drain existing work.
	lastStall     *uploadStall
}

type poolGeneration struct {
	id        uint64
	transport *http.Transport
	active    int                      // Includes response bodies, not just RoundTrip calls.
	attempts  map[*poolUpload]struct{} // Protected by recoveringTransport.mu.
	connMu    sync.Mutex
	conns     map[*identifiedConn]struct{}
	closed    bool
}

func newRecoveringTransport(template *http.Transport) *recoveringTransport {
	template = template.Clone()
	return &recoveringTransport{template: template, tlsConfig: template.TLSClientConfig.Clone(), origins: make(map[string]*originPools)}
}

func (t *recoveringTransport) newGeneration() *poolGeneration {
	generation := &poolGeneration{
		id: nextPoolGeneration.Add(1), transport: t.template.Clone(),
		conns:    make(map[*identifiedConn]struct{}),
		attempts: make(map[*poolUpload]struct{}),
	}
	dial := generation.transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	generation.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		identified := &identifiedConn{Conn: conn, id: nextConnectionID.Add(1), owner: generation}
		generation.connMu.Lock()
		if generation.closed {
			generation.connMu.Unlock()
			conn.Close()
			return nil, net.ErrClosed
		}
		generation.conns[identified] = struct{}{}
		generation.connMu.Unlock()
		return identified, nil
	}
	return generation
}

// prepareClose prevents late dials from joining the generation. The returned
// action must run outside pool and trace locks: net/http invokes trace hooks
// while holding its own pool locks, and TLS shutdown can perform network I/O.
// After all admitted responses end, close our dialer's own sockets even when
// HTTP/2 stream cleanup has not yet caught up with application EOF/cancellation.
func (g *poolGeneration) prepareClose() func() {
	g.connMu.Lock()
	if g.closed {
		g.connMu.Unlock()
		return nil
	}
	g.closed = true
	connections := make([]*identifiedConn, 0, len(g.conns))
	for conn := range g.conns {
		connections = append(connections, conn)
	}
	g.connMu.Unlock()
	return func() {
		// Close the sockets first; a retiring pool has no remaining users and
		// must not wait for a TLS close-notify write on a broken connection.
		for _, conn := range connections {
			conn.Close()
		}
		g.transport.CloseIdleConnections()
	}
}

type transportObserverKey struct{}

func (t *recoveringTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// The enclosing http.Client validates the URL. Paths and credentials do
	// not participate in the key or in transport diagnostics.
	origin := strings.ToLower(req.URL.Scheme + "://" + req.URL.Host)
	t.mu.Lock()
	pools := t.origins[origin]
	if pools == nil {
		pools = &originPools{lanes: []*originPool{{current: t.newGeneration()}}}
		t.origins[origin] = pools
	}
	pool, action := t.chooseUploadPool(pools, time.Now())
	var cleanups []func()
	for _, lane := range slices.Clone(pools.lanes) {
		if lane.retiring {
			// An expired trial may already be idle, with no future release to
			// retire it. Reclaim it now, even when IdleConnTimeout is disabled.
			if cleanup := t.onIdleLocked(origin, lane); cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}
		}
	}
	poolCount := len(pools.lanes)
	pool.idleEpoch++
	if pool.timer != nil {
		pool.timer.Stop()
		pool.timer = nil
	}
	pool.closeWhenIdle = false
	generation := pool.current
	generation.active++
	upload := &poolUpload{ctx: req.Context(), bytes: req.ContentLength}
	generation.attempts[upload] = struct{}{}
	t.mu.Unlock()
	for _, cleanup := range cleanups {
		cleanup()
	}

	observer, _ := req.Context().Value(transportObserverKey{}).(*TransportTrace)
	if observer != nil {
		bodyBytes := req.ContentLength
		observer.beginPoolAttempt(generation.id, func() (string, func()) {
			return t.recoverUpload(origin, pool, generation, bodyBytes)
		}, func() bool {
			t.mu.Lock()
			defer t.mu.Unlock()
			verified := pool.current == generation && pool.recoveryBytes > 0 && bodyBytes >= pool.recoveryBytes
			if verified {
				pool.recoveryBytes = 0
			}
			return verified
		})
		observer.setPoolChoice(generation.id, poolCount, action)
	}
	// Observe native callers too. Do not depend on application payload decoding or
	// acquire the diagnostic trace lock while holding the pool lock.
	progress := &httptrace.ClientTrace{
		GetConn: func(string) {
			t.mu.Lock()
			upload.resetConnection()
			t.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			upload.connectionID, upload.protocol = connectionIdentity(info.Conn)
			upload.connected = time.Now()
			t.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.mu.Lock()
			now := time.Now()
			if info.Err == nil {
				if pool.current == generation {
					if stalled := generation.stalledSharedUpload(now); stalled != nil {
						pool.lastStall = &uploadStall{bytes: stalled.bytes, elapsed: now.Sub(stalled.connected), observed: now}
					}
				}
				upload.uploaded = now
			} else {
				upload.failed = true
			}
			result := t.observeUploadProbe(pools, pool, generation, upload, now)
			t.mu.Unlock()
			if observer != nil && result != "" {
				observer.setPoolChoice(generation.id, poolCount, result)
			}
		},
	}
	response, err := generation.transport.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), progress)))
	if err != nil {
		t.release(origin, pool, generation, upload)
		return response, err
	}
	t.mu.Lock()
	upload.headers = true
	upload.status = response.StatusCode
	probeResult := t.observeUploadProbe(pools, pool, generation, upload, time.Now())
	t.mu.Unlock()
	if observer != nil {
		if probeResult != "" {
			observer.setPoolChoice(generation.id, poolCount, probeResult)
		}
		observer.recordProtocol(response.Proto)
		// HTTP/2 can deliver headers before finishing the upload. Verification
		// observes both events in either order rather than sampling only here.
		observer.record(TransportHeaders, time.Now())
	}
	response.Body = &poolResponseBody{ReadCloser: response.Body, release: func() {
		t.release(origin, pool, generation, upload)
	}}
	return response, nil
}

func (t *recoveringTransport) recoverUpload(origin string, pool *originPool, generation *poolGeneration, bodyBytes int64) (string, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasPool(origin, pool) || pool.current != generation {
		return "already_replaced", nil
	}
	if t.template.DisableKeepAlives {
		return "reuse_disabled", nil
	}
	if bodyBytes <= 0 {
		return "unmeasured_upload", nil
	}
	if pool.recoveryBytes != 0 {
		return "awaiting_comparable_upload", nil
	}
	if pool.draining != nil {
		return "previous_pool_draining", nil
	}
	pool.current = t.newGeneration()
	pool.draining = generation
	pool.recoveryBytes = bodyBytes
	pool.lastStall = nil
	if generation.active == 0 {
		return "pool_replaced", t.retireLocked(origin, pool, generation)
	}
	return "pool_replaced", nil
}

func (t *recoveringTransport) release(origin string, pool *originPool, generation *poolGeneration, upload *poolUpload) {
	t.mu.Lock()
	generation.active--
	delete(generation.attempts, upload)
	upload.released = true
	var cleanup func()
	if generation != pool.current && generation.active == 0 {
		cleanup = t.retireLocked(origin, pool, generation)
	} else {
		cleanup = t.onIdleLocked(origin, pool)
	}
	t.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

func (t *recoveringTransport) retireLocked(origin string, pool *originPool, generation *poolGeneration) func() {
	closeConnections := generation.prepareClose()
	return func() {
		if closeConnections != nil {
			closeConnections()
		}
		t.mu.Lock()
		// Keep the old generation visible until its sockets actually close:
		// a concurrent successful upload must not enable a third pool early.
		pool.draining = nil
		cleanup := t.onIdleLocked(origin, pool)
		t.mu.Unlock()
		if cleanup != nil {
			cleanup()
		}
	}
}

func (t *recoveringTransport) onIdleLocked(origin string, pool *originPool) func() {
	if pool.current.active != 0 || pool.draining != nil {
		return nil
	}
	if pool.closeWhenIdle || pool.retiring {
		return t.removeIdleLocked(origin, pool)
	}
	if timeout := t.template.IdleConnTimeout; timeout > 0 {
		pool.idleEpoch++
		epoch := pool.idleEpoch
		pool.timer = time.AfterFunc(timeout, func() {
			defer func() {
				if value := recover(); value != nil {
					slog.Error("HTTP pool cleanup panicked", "panic", value)
				}
			}()
			t.mu.Lock()
			var cleanup func()
			if t.hasPool(origin, pool) && pool.idleEpoch == epoch {
				cleanup = t.removeIdleLocked(origin, pool)
			}
			t.mu.Unlock()
			if cleanup != nil {
				cleanup()
			}
		})
	}
	return nil
}

// removeIdleLocked is called only after every admitted request has ended.
func (t *recoveringTransport) removeIdleLocked(origin string, pool *originPool) func() {
	pool.idleEpoch++
	if pool.timer != nil {
		pool.timer.Stop()
		pool.timer = nil
	}
	pools := t.origins[origin]
	pools.lanes = slices.DeleteFunc(pools.lanes, func(lane *originPool) bool { return lane == pool })
	if pools.probe != nil && pools.probe.lane == pool {
		pools.probe = nil
	}
	if len(pools.lanes) == 0 {
		delete(t.origins, origin)
	}
	return pool.current.prepareClose()
}

func (t *recoveringTransport) CloseIdleConnections() {
	t.mu.Lock()
	var cleanups []func()
	for origin, pools := range t.origins {
		for _, pool := range slices.Clone(pools.lanes) {
			pool.closeWhenIdle = true
			if pool.current.active == 0 && pool.draining == nil {
				if cleanup := t.removeIdleLocked(origin, pool); cleanup != nil {
					cleanups = append(cleanups, cleanup)
				}
			} else {
				cleanups = append(cleanups, pool.current.transport.CloseIdleConnections)
				if pool.draining != nil {
					cleanups = append(cleanups, pool.draining.transport.CloseIdleConnections)
				}
			}
		}
	}
	t.mu.Unlock()
	for _, cleanup := range cleanups {
		cleanup()
	}
}

// hasPool is called with t.mu held; a lane can expire independently of its peers.
func (t *recoveringTransport) hasPool(origin string, pool *originPool) bool {
	pools := t.origins[origin]
	return pools != nil && slices.Contains(pools.lanes, pool)
}

type poolResponseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *poolResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(b.release)
	}
	return n, err
}

func (b *poolResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

type identifiedConn struct {
	net.Conn
	id    uint64
	owner *poolGeneration
}

func (c *identifiedConn) Close() error {
	err := c.Conn.Close()
	c.owner.connMu.Lock()
	delete(c.owner.conns, c)
	c.owner.connMu.Unlock()
	return err
}

func connectionIdentity(conn net.Conn) (uint64, string) {
	protocol := "HTTP/1.1"
	if secure, ok := conn.(*tls.Conn); ok {
		if secure.ConnectionState().NegotiatedProtocol == "h2" {
			protocol = "HTTP/2.0"
		}
	}
	// HTTPS proxies add their own TLS layer below the upstream TLS session.
	// Protocol belongs to the outermost session; identity to the owned socket.
	for {
		secure, ok := conn.(*tls.Conn)
		if !ok {
			break
		}
		conn = secure.NetConn()
	}
	if identified, ok := conn.(*identifiedConn); ok {
		return identified.id, protocol
	}
	return 0, protocol
}
