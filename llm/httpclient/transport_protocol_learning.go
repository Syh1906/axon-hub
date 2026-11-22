package httpclient

import (
	"crypto/sha256"
	"errors"
	"math/bits"
	"net/http"
	"strings"
	"time"
)

const (
	minProtocolUpload = 1 << 20
	protocolWindow    = 10 * time.Minute
	maxProtocolKeys   = 2048
)

type protocolScopeKey struct{}

type ProtocolPreference string

const (
	ProtocolAuto        ProtocolPreference = "AUTO"
	ProtocolPreferHTTP2 ProtocolPreference = "PREFER_HTTP2"
	ProtocolHTTP1       ProtocolPreference = "HTTP1"
)

func (p ProtocolPreference) Valid() bool {
	return p == ProtocolAuto || p == ProtocolPreferHTTP2 || p == ProtocolHTTP1
}

type protocolScope struct {
	channel     int
	generation  int64
	work        ProtocolWorkload
	varyHeaders []string
}

// ProtocolWorkload separates first-output observations for comparable streams.
// The zero value selects upload timing for requests without a stream clock.
type ProtocolWorkload struct {
	Model, Effort, Tier, Ingress, Format string
}

type protocolKey struct {
	resource encodingResource
	size     int
	encoding string
	work     ProtocolWorkload
}

func uploadProtocolKey(req *http.Request, tr *http.Transport, pinned bool) (protocolKey, bool) {
	scope, ok := req.Context().Value(protocolScopeKey{}).(protocolScope)
	if !ok || pinned || req.Response != nil || req.URL.Scheme != "https" || req.Method != http.MethodPost ||
		req.ContentLength <= 0 ||
		tr.MaxConnsPerHost > 0 || tr.DisableKeepAlives ||
		tr.DialTLSContext != nil || tr.DialTLS != nil || tr.Protocols != nil && (!tr.Protocols.HTTP1() || !tr.Protocols.HTTP2()) {
		return protocolKey{}, false
	}
	resource := encodingKey(scope.channel, scope.generation, req.URL.String(), req.Header)
	if len(scope.varyHeaders) > 0 {
		// Custom headers may select an account or backend outside the known credential headers.
		varied := make(http.Header, len(scope.varyHeaders))
		for _, name := range scope.varyHeaders {
			varied[name] = req.Header.Values(name)
		}
		h := sha256.New()
		_, _ = h.Write(resource.account[:])
		_ = varied.Write(h)
		h.Sum(resource.account[:0])
	}
	return protocolKey{
		resource: resource, work: scope.work,
		size: bits.Len64(uint64(req.ContentLength)), encoding: req.Header.Get("Content-Encoding"),
	}, true
}

// Go's bundled HTTP/2 transport uses private error types. Match its specific
// wire errors only after the negotiated connection has been observed as h2.
func isHTTP2ProtocolFailure(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		message := err.Error()
		if strings.HasPrefix(message, "http2: server sent GOAWAY and closed the connection") || message == "http2: frame too large" ||
			strings.HasPrefix(message, "http2: failed reading the frame payload: ") && strings.HasSuffix(message, "the frame header looked like an HTTP/1.1 header") {
			return true
		}
		if strings.HasPrefix(message, "stream error: stream ID ") || strings.HasPrefix(message, "connection error: ") ||
			strings.HasPrefix(message, "http2: connection error: ") {
			return strings.Contains(message, "PROTOCOL_ERROR") || strings.Contains(message, "HTTP_1_1_REQUIRED") || strings.Contains(message, "FRAME_SIZE_ERROR") ||
				strings.Contains(message, "REFUSED_STREAM") || strings.Contains(message, "INTERNAL_ERROR")
		}
	}
	return false
}

// Remember incompatibility for future dispatches. Never replay a model POST
// here: the endpoint may have accepted the body before its stream failed.
func (t *recoveringTransport) recordHTTP2Failure(key encodingResource, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.http2Failures == nil {
		t.http2Failures = make(map[encodingResource]time.Time)
	}
	if len(t.http2Failures) >= maxProtocolKeys {
		for k, expiry := range t.http2Failures {
			if !now.Before(expiry) {
				delete(t.http2Failures, k)
			}
		}
		if _, exists := t.http2Failures[key]; !exists && len(t.http2Failures) >= maxProtocolKeys {
			return
		}
	}
	t.http2Failures[key] = now.Add(protocolWindow)
}

type protocolHistory map[protocolKey]*protocolEvidence

type protocolEvidence struct {
	until              time.Time
	controlAfter       time.Time // Stream trials get a fresh HTTP/2 comparison between wins.
	baseline           float64   // Fastest control: ns to first output, or upload ns per byte.
	controls, wins     int
	inFlight, cooldown bool
}

type protocolSample struct {
	key          protocolKey
	state        *protocolEvidence
	start        time.Time
	http1, trial bool
}

// Each decision applies to one naturally arriving request. Expiry starts a new
// measurement window even for a successful preference. In-flight completions
// from an old window cannot validate its replacement.
func (h *protocolHistory) selectProtocol(key protocolKey, now time.Time) protocolSample {
	if *h == nil {
		*h = make(protocolHistory)
	}
	s := (*h)[key]
	if s == nil || !now.Before(s.until) {
		if s == nil && len(*h) >= maxProtocolKeys {
			for k, entry := range *h {
				if !now.Before(entry.until) {
					delete(*h, k)
				}
			}
			if len(*h) >= maxProtocolKeys {
				return protocolSample{}
			}
		}
		s = &protocolEvidence{until: now.Add(protocolWindow)}
		(*h)[key] = s
	}
	if s.cooldown || s.inFlight {
		return protocolSample{}
	}
	sample := protocolSample{key: key, state: s, start: now}
	if s.controls >= 2 {
		sample.http1 = true
		sample.trial = s.wins < 2
		s.inFlight = sample.trial
	}
	return sample
}

func (h protocolHistory) reject(sample protocolSample, now time.Time) {
	if s := sample.state; h[sample.key] == s && !s.cooldown && now.Before(s.until) {
		s.cooldown, s.until = true, now.Add(protocolWindow)
	}
}

func (h protocolHistory) observe(sample protocolSample, upload *poolUpload, resp *http.Response, err error, now time.Time) {
	s := sample.state
	if h[sample.key] != s || s.cooldown || !now.Before(s.until) {
		return
	}
	if sample.trial {
		s.inFlight = false
	}
	if upload.ctx.Err() != nil {
		return
	}
	valid := err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 &&
		!upload.failed && !upload.connected.IsZero() && !upload.uploaded.Before(upload.connected) &&
		upload.bytes >= minProtocolUpload
	if sample.http1 {
		// Charge the candidate for connection setup and pool waiting as well.
		// Headers and model processing time never enter the comparison.
		cost := float64(upload.uploaded.Sub(sample.start)) / float64(upload.bytes)
		if !valid || resp.ProtoMajor != 1 || cost > .75*s.baseline {
			h.reject(sample, now)
		} else if sample.trial {
			s.wins++
		}
		return
	}
	if !valid || resp.ProtoMajor != 2 || s.controls >= 2 {
		return
	}
	elapsed := upload.uploaded.Sub(upload.connected)
	if elapsed < minSlowUploadTime {
		s.controls, s.baseline = 0, 0
		return
	}
	s.addControl(float64(elapsed) / float64(upload.bytes))
}

func (s *protocolEvidence) addControl(cost float64) {
	if s.controls == 0 || cost < s.baseline {
		s.baseline = cost
	}
	s.controls++
}

func (t *recoveringTransport) trackProtocolOutput(sample protocolSample, trace *TransportTrace, response *http.Response, err error) {
	finish := func(first time.Time) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.protocols.observeOutput(sample, first, time.Now())
	}
	version := 2
	if sample.http1 {
		version = 1
	}
	if err != nil || response.StatusCode/100 != 2 || response.ProtoMajor != version || trace == nil {
		finish(time.Time{})
		return
	}
	trace.mu.Lock()
	trace.finishProtocol = finish
	trace.mu.Unlock()
}

// A successful HTTP response only establishes the transport version. A stream
// contributes a timing once the application confirms delivery and identifies
// its first model or tool event. Abandoned or failed streams cannot be wins.
func (h protocolHistory) observeOutput(sample protocolSample, first time.Time, now time.Time) {
	s := sample.state
	if h[sample.key] != s || s.cooldown || !now.Before(s.until) {
		return
	}
	cost := float64(first.Sub(sample.start))
	observed := !first.IsZero() && !first.Before(sample.start)
	if sample.http1 {
		if sample.trial {
			s.inFlight = false
		}
		if !observed || cost > .75*s.baseline {
			h.reject(sample, now)
		} else if sample.trial {
			s.wins++
			if s.wins == 1 {
				s.controls = 1
				s.controlAfter = now
			}
		}
	} else if observed && s.controls < 2 && !sample.start.Before(s.controlAfter) {
		if cost == 0 {
			// No measurable improvement is possible over this control.
			h.reject(sample, now)
		} else {
			s.addControl(cost)
		}
	}
}
