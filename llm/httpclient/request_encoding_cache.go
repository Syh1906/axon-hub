package httpclient

import (
	"crypto/sha256"
	"hash"
	"math/bits"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const encodingEvidenceTTL = 10 * time.Minute

type encodingResource struct {
	scope      int
	generation int64
	url        [32]byte
	account    [32]byte
}

func encodingAccount(h http.Header) [32]byte {
	var digest [32]byte
	var hasher hash.Hash
	for i, key := range [...]string{"Authorization", "X-Api-Key", "Api-Key", "Chatgpt-Account-Id", "OpenAI-Organization", "OpenAI-Project", "X-Api-Secret", "X-Api-Token", "X-Goog-Api-Key", "X-Google-Api-Key", "Cookie", "Proxy-Authorization"} {
		for _, value := range h.Values(key) {
			if hasher == nil {
				hasher = sha256.New()
			}
			hasher.Write([]byte{0, byte(i), 0})
			hasher.Write([]byte(value))
		}
	}
	if hasher != nil {
		hasher.Sum(digest[:0])
	}
	return digest
}

type encodingCost struct {
	savedFraction float64
	cpuPerByte    float64
	nextTrial     time.Time
	plainPerByte  float64
	plainAt       time.Time
}

type encodingEvidence struct {
	until, declaration, rejectedAt, blockedUntil time.Time
	costs                                        [4]encodingCost
}

type encodingAttempt struct {
	key      encodingResource
	start    time.Time
	explicit bool
	encoded  bool
	size     int
	saved    int
	cpu      time.Duration
	measure  bool
	small    *http.Request
}

type requestEncodingCache struct {
	mu      sync.Mutex
	entries map[encodingResource]encodingEvidence
}

func encodingBucket(n int) int {
	return min(3, max(0, bits.Len(uint(n/minZstdRequestBytes))-1))
}

func (c *requestEncodingCache) shouldEncode(a *encodingAttempt) bool {
	if a.size < minZstdRequestBytes {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[a.key]
	if a.start.Before(e.blockedUntil) {
		return false
	}
	if a.explicit {
		return true
	}
	if !a.start.Before(e.until) {
		return false
	}
	bucket := encodingBucket(a.size)
	cost := e.costs[bucket]
	if cost.cpuPerByte > 0 && a.start.Sub(cost.plainAt) < time.Minute && cost.savedFraction*cost.plainPerByte > cost.cpuPerByte {
		return true
	}
	if cost.cpuPerByte > 0 && a.start.Sub(cost.plainAt) >= time.Minute {
		return false // refresh the uncompressed baseline with one real request
	}
	if a.start.Before(cost.nextTrial) {
		return false
	}
	// Permit one bounded trial in this size range using the actual request.
	e.costs[bucket].nextTrial = a.start.Add(time.Minute)
	c.entries[a.key] = e
	return true
}

func (c *requestEncodingCache) observe(a *encodingAttempt, response *http.Response, trace *TransportTrace) {
	if response == nil {
		return
	}
	success := response.StatusCode/100 == 2
	rejected := a.encoded && response.StatusCode == http.StatusUnsupportedMediaType
	if !success && !rejected {
		return
	}
	values := response.Header.Values("Accept-Encoding")
	if a.small != nil {
		if len(values) == 0 {
			return
		}
		a.key = encodingKey(a.key.scope, a.key.generation, a.small.URL.String(), a.small.Header)
	}
	advertised := acceptsZstd(values)
	var duration time.Duration
	var wire int64
	if trace != nil && a.size >= minZstdRequestBytes {
		trace.mu.Lock()
		start, end := trace.times[TransportConnected], trace.times[TransportUploaded]
		if !start.IsZero() && end.After(start) {
			duration, wire = end.Sub(start), trace.wireBytes
		}
		trace.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	e, exists := c.entries[a.key]
	if !exists && !(advertised || rejected || a.explicit && a.cpu > 0 || a.measure && duration > 0) {
		return
	}
	if !exists {
		if len(c.entries) >= 512 {
			// Eviction only discards optional evidence. The next request uses plain JSON.
			for key := range c.entries {
				delete(c.entries, key)
				break
			}
		}
		if c.entries == nil {
			c.entries = make(map[encodingResource]encodingEvidence)
		}
	}
	if rejected {
		e = encodingEvidence{rejectedAt: now, blockedUntil: now.Add(encodingEvidenceTTL)}
	} else {
		if a.start.Before(e.rejectedAt) || now.Before(e.blockedUntil) {
			return
		}
		if len(values) > 0 && !a.start.Before(e.declaration) {
			e.declaration = a.start
			e.until = time.Time{}
			if advertised {
				e.until = now.Add(encodingEvidenceTTL)
			}
		}
		cost := &e.costs[encodingBucket(a.size)]
		if duration > 0 && wire == int64(a.size) && !a.encoded {
			cost.plainPerByte, cost.plainAt = float64(duration)/float64(wire), now
		}
		if a.cpu > 0 {
			cost.savedFraction = float64(max(0, a.saved)) / float64(a.size)
			cost.cpuPerByte, cost.nextTrial = float64(a.cpu)/float64(a.size), now.Add(time.Minute)
		}
	}
	c.entries[a.key] = e
}

// Only an explicit positive zstd preference in a response is evidence.
func acceptsZstd(values []string) bool {
	accepted := false
	for _, line := range values {
		for item := range strings.SplitSeq(line, ",") {
			coding, param, hasParam := strings.Cut(item, ";")
			if !strings.EqualFold(strings.TrimSpace(coding), "zstd") {
				continue
			}
			if hasParam {
				key, raw, ok := strings.Cut(strings.TrimSpace(param), "=")
				raw = strings.TrimSpace(raw)
				value, err := strconv.ParseFloat(raw, 64)
				// RFC 9110 qvalue is 0 or 1 with up to three decimal digits.
				if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") || len(raw) == 0 || len(raw) > 5 ||
					(raw[0] != '0' && raw[0] != '1') || len(raw) > 1 && (raw[1] != '.' || strings.Trim(raw[2:], "0123456789") != "") ||
					err != nil || !(value > 0 && value <= 1) {
					return false
				}
			}
			accepted = true
		}
	}
	return accepted
}
