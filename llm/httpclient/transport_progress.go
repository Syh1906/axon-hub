package httpclient

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

type TransportPhase string

const (
	TransportStarted    TransportPhase = "started"
	TransportConnected  TransportPhase = "connected"
	TransportUploaded   TransportPhase = "uploaded"
	TransportHeaders    TransportPhase = "headers"
	TransportFirstByte  TransportPhase = "first_byte"
	TransportFirstEvent TransportPhase = "first_event"
)

// TransportTrace is local to one attempt. net/http invokes some hooks concurrently.
type TransportTrace struct {
	mu                 sync.Mutex
	times              map[TransportPhase]time.Time
	poolGeneration     uint64
	connectionID       uint64
	protocol           string
	reused             *bool
	connectionAttempts int
	poolRecovery       string
	poolCount          int
	poolAction         string
	recoverUpload      func() (string, func())
	verifyUpload       func() bool
}

// TransportTimings uses nullable durations: absent evidence is not zero latency.
type TransportTimings struct {
	HTTPObserved       bool   `json:"http_observed"`
	ConnectMs          *int64 `json:"connect_ms,omitempty"`
	UploadMs           *int64 `json:"upload_ms,omitempty"`
	HeadersWaitMs      *int64 `json:"headers_wait_ms,omitempty"`
	FirstByteWaitMs    *int64 `json:"first_byte_wait_ms,omitempty"`
	FirstEventWaitMs   *int64 `json:"first_event_wait_ms,omitempty"`
	UploadCompleted    bool   `json:"upload_completed"`
	PoolGeneration     uint64 `json:"pool_generation,omitempty"`
	ConnectionID       uint64 `json:"connection_id,omitempty"`
	Protocol           string `json:"protocol,omitempty"`
	Reused             *bool  `json:"connection_reused,omitempty"`
	ConnectionAttempts int    `json:"connection_attempts,omitempty"`
	PoolRecovery       string `json:"pool_recovery,omitempty"`
	PoolCount          int    `json:"pool_count,omitempty"`
	PoolAction         string `json:"pool_action,omitempty"`
}

func (t *TransportTrace) beginPoolAttempt(generation uint64, recoverUpload func() (string, func()), verifyUpload func() bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.poolGeneration, t.recoverUpload = generation, recoverUpload
	t.verifyUpload = verifyUpload
	// Redirects use a different RoundTrip and may select a different origin.
	t.times = map[TransportPhase]time.Time{TransportStarted: time.Now()}
	t.connectionID, t.protocol, t.reused, t.connectionAttempts = 0, "", nil, 0
	t.poolRecovery = ""
	t.poolCount, t.poolAction = 0, ""
}

func (t *TransportTrace) setPoolChoice(generation uint64, count int, action string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.poolGeneration != generation {
		return
	}
	t.poolCount, t.poolAction = count, action
}

func (t *TransportTrace) recordConnection(info httptrace.GotConnInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connectionID, t.protocol = connectionIdentity(info.Conn)
	t.reused = new(info.Reused)
}

func (t *TransportTrace) startConnectionAttempt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	// A Transport may transparently retry on a fresh connection within one
	// RoundTrip. Its last connection/upload evidence must not label the new
	// dial as an upload or suppress recovery for the new upload.
	t.times = map[TransportPhase]time.Time{TransportStarted: time.Now()}
	t.connectionID, t.protocol, t.reused = 0, "", nil
	t.connectionAttempts++
}

func (t *TransportTrace) recordProtocol(protocol string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.protocol = protocol
}

// RecoverHTTPUpload changes only future connection selection. The pipeline
// calls this for its own upload timeout, never for cancellation or response
// timeouts. A missing WroteRequest callback alone is not permission to replay.
func (r *Request) RecoverHTTPUpload() {
	t := r.TransportTrace
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.recoverUpload == nil || t.poolRecovery != "" || t.times[TransportConnected].IsZero() ||
		!t.times[TransportUploaded].IsZero() || !t.times[TransportHeaders].IsZero() {
		t.mu.Unlock()
		return
	}
	status, cleanup := t.recoverUpload()
	t.poolRecovery = status
	t.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

func (t *TransportTrace) record(phase TransportPhase, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.times == nil {
		t.times = make(map[TransportPhase]time.Time)
	}
	if t.times[phase].IsZero() {
		t.times[phase] = at
	}
	if t.verifyUpload != nil && !t.times[TransportUploaded].IsZero() && !t.times[TransportHeaders].IsZero() {
		if t.verifyUpload() {
			t.poolRecovery = "upload_verified"
		}
		t.verifyUpload = nil
	}
}

func (t *TransportTrace) Snapshot() TransportTimings {
	if t == nil {
		return TransportTimings{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	duration := func(start, end TransportPhase) *int64 {
		a, b := t.times[start], t.times[end]
		if a.IsZero() || b.IsZero() || b.Before(a) {
			return nil
		}
		return new(b.Sub(a).Milliseconds())
	}
	return TransportTimings{
		HTTPObserved:     !t.times[TransportStarted].IsZero(),
		ConnectMs:        duration(TransportStarted, TransportConnected),
		UploadMs:         duration(TransportConnected, TransportUploaded),
		HeadersWaitMs:    duration(TransportUploaded, TransportHeaders),
		FirstByteWaitMs:  duration(TransportUploaded, TransportFirstByte),
		FirstEventWaitMs: duration(TransportUploaded, TransportFirstEvent),
		UploadCompleted:  !t.times[TransportUploaded].IsZero(),
		PoolGeneration:   t.poolGeneration, ConnectionID: t.connectionID,
		Protocol: t.protocol, Reused: t.reused, ConnectionAttempts: t.connectionAttempts,
		PoolRecovery: t.poolRecovery,
		PoolCount:    t.poolCount, PoolAction: t.poolAction,
	}
}

func (r *Request) ReportHTTPProgress(phase TransportPhase, at time.Time) {
	if r.TransportTrace != nil {
		r.TransportTrace.record(phase, at)
	}
	if r.OnHTTPProgress != nil {
		r.OnHTTPProgress(phase, at)
	}
}

func traceHTTPRequest(request *Request, raw *http.Request) *http.Request {
	if request.TransportTrace == nil {
		request.TransportTrace = new(TransportTrace)
	}
	request.ReportHTTPProgress(TransportStarted, time.Now())
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			request.TransportTrace.startConnectionAttempt()
			request.ReportHTTPProgress(TransportStarted, time.Now())
		},
		GotConn: func(info httptrace.GotConnInfo) {
			request.TransportTrace.recordConnection(info)
			request.ReportHTTPProgress(TransportConnected, time.Now())
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				request.ReportHTTPProgress(TransportUploaded, time.Now())
			}
		},
	}
	ctx := context.WithValue(raw.Context(), transportObserverKey{}, request.TransportTrace)
	return raw.WithContext(httptrace.WithClientTrace(ctx, trace))
}

func (r *Request) observeFirstByte(at time.Time) {
	r.ReportHTTPProgress(TransportFirstByte, at)
	if r.OnFirstResponseByte != nil {
		r.OnFirstResponseByte(at)
	}
}
