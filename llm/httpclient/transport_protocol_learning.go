package httpclient

import (
	"math/bits"
	"net/http"
	"time"
)

const (
	minProtocolUpload = 1 << 20
	protocolWindow    = 10 * time.Minute
	maxProtocolKeys   = 2048
)

type protocolScopeKey struct{}
type protocolScope struct {
	channel    int
	generation int64
}

type protocolKey struct {
	resource encodingResource
	size     int
	encoding string
}

func uploadProtocolKey(req *http.Request, tr *http.Transport, pinned bool) (protocolKey, bool) {
	scope, ok := req.Context().Value(protocolScopeKey{}).(protocolScope)
	if !ok || pinned || req.Response != nil || req.URL.Scheme != "https" || req.Method != http.MethodPost ||
		req.ContentLength < minProtocolUpload || tr.MaxConnsPerHost > 0 || tr.DisableKeepAlives ||
		tr.DialTLSContext != nil || tr.DialTLS != nil || tr.Protocols != nil && (!tr.Protocols.HTTP1() || !tr.Protocols.HTTP2()) {
		return protocolKey{}, false
	}
	return protocolKey{
		resource: encodingKey(scope.channel, scope.generation, req.URL.String(), req.Header),
		size:     bits.Len64(uint64(req.ContentLength)), encoding: req.Header.Get("Content-Encoding"),
	}, true
}

type protocolHistory map[protocolKey]*protocolEvidence

type protocolEvidence struct {
	until              time.Time
	baseline           float64 // Fastest qualifying HTTP/2 upload, nanoseconds per wire byte.
	slow, wins         int
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
	if s.slow >= 2 {
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
	if !valid || resp.ProtoMajor != 2 || s.slow >= 2 {
		return
	}
	elapsed := upload.uploaded.Sub(upload.connected)
	if elapsed < minSlowUploadTime || float64(upload.bytes)/elapsed.Seconds() > 320<<10 {
		s.slow, s.baseline = 0, 0
		return
	}
	cost := float64(elapsed) / float64(upload.bytes)
	if s.slow == 0 || cost < s.baseline {
		s.baseline = cost
	}
	s.slow++
}
