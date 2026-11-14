package httpclient

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var nextPoolGeneration atomic.Uint64
var nextConnectionID atomic.Uint64

// recoveringTransport keeps proxy/TLS policy local to its owning HttpClient.
// Each origin has an independent standard-library pool. Recovery changes only
// the pool used by future requests; it never closes a traced connection or
// retries a request. At most one old pool per origin may drain at a time.
type recoveringTransport struct {
	template  *http.Transport
	tlsConfig *tls.Config // Immutable policy, separate from Transport's lazy HTTP/2 setup.
	mu        sync.Mutex
	origins   map[string]*originPool
}

type originPool struct {
	current       *poolGeneration
	draining      *poolGeneration
	recoveryBytes int64 // A comparable upload must succeed before another recovery.
	timer         *time.Timer
	idleEpoch     uint64
	closeWhenIdle bool
}

type poolGeneration struct {
	id        uint64
	transport *http.Transport
	active    int // Includes response bodies, not just RoundTrip calls.
	connMu    sync.Mutex
	conns     map[*identifiedConn]struct{}
	closed    bool
}

func newRecoveringTransport(template *http.Transport) *recoveringTransport {
	template = template.Clone()
	return &recoveringTransport{template: template, tlsConfig: template.TLSClientConfig.Clone(), origins: make(map[string]*originPool)}
}

func (t *recoveringTransport) newGeneration() *poolGeneration {
	generation := &poolGeneration{
		id: nextPoolGeneration.Add(1), transport: t.template.Clone(),
		conns: make(map[*identifiedConn]struct{}),
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
	pool := t.origins[origin]
	if pool == nil {
		pool = &originPool{current: t.newGeneration()}
		t.origins[origin] = pool
	}
	pool.idleEpoch++
	if pool.timer != nil {
		pool.timer.Stop()
		pool.timer = nil
	}
	pool.closeWhenIdle = false
	generation := pool.current
	generation.active++
	t.mu.Unlock()

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
	}
	response, err := generation.transport.RoundTrip(req)
	if err != nil {
		t.release(origin, pool, generation)
		return response, err
	}
	if observer != nil {
		observer.recordProtocol(response.Proto)
		// HTTP/2 can deliver headers before finishing the upload. Verification
		// observes both events in either order rather than sampling only here.
		observer.record(TransportHeaders, time.Now())
	}
	response.Body = &poolResponseBody{ReadCloser: response.Body, release: func() {
		t.release(origin, pool, generation)
	}}
	return response, nil
}

func (t *recoveringTransport) recoverUpload(origin string, pool *originPool, generation *poolGeneration, bodyBytes int64) (string, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.origins[origin] != pool || pool.current != generation {
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
	if generation.active == 0 {
		return "pool_replaced", t.retireLocked(origin, pool, generation)
	}
	return "pool_replaced", nil
}

func (t *recoveringTransport) release(origin string, pool *originPool, generation *poolGeneration) {
	t.mu.Lock()
	generation.active--
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
	if pool.closeWhenIdle {
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
			if t.origins[origin] == pool && pool.idleEpoch == epoch {
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
	delete(t.origins, origin)
	return pool.current.prepareClose()
}

func (t *recoveringTransport) CloseIdleConnections() {
	t.mu.Lock()
	var cleanups []func()
	for origin, pool := range t.origins {
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
	t.mu.Unlock()
	for _, cleanup := range cleanups {
		cleanup()
	}
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
