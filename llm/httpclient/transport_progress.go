package httpclient

import (
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
	mu    sync.Mutex
	times map[TransportPhase]time.Time
}

// TransportTimings uses nullable durations: absent evidence is not zero latency.
type TransportTimings struct {
	HTTPObserved     bool   `json:"http_observed"`
	ConnectMs        *int64 `json:"connect_ms,omitempty"`
	UploadMs         *int64 `json:"upload_ms,omitempty"`
	HeadersWaitMs    *int64 `json:"headers_wait_ms,omitempty"`
	FirstByteWaitMs  *int64 `json:"first_byte_wait_ms,omitempty"`
	FirstEventWaitMs *int64 `json:"first_event_wait_ms,omitempty"`
	UploadCompleted  bool   `json:"upload_completed"`
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
	request.ReportHTTPProgress(TransportStarted, time.Now())
	trace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { request.ReportHTTPProgress(TransportConnected, time.Now()) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				request.ReportHTTPProgress(TransportUploaded, time.Now())
			}
		},
	}
	return raw.WithContext(httptrace.WithClientTrace(raw.Context(), trace))
}

func (r *Request) observeFirstByte(at time.Time) {
	r.ReportHTTPProgress(TransportFirstByte, at)
	if r.OnFirstResponseByte != nil {
		r.OnFirstResponseByte(at)
	}
}
