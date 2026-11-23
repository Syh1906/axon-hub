package httpclient

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/looplj/axonhub/llm/encodingbudget"
)

// A private, negotiated request coding between explicitly paired gateways.
const (
	PeerEncoding = "axon-zstd-dict-v1"
	PeerOffer    = "X-Axonhub-Dict-Scope"
	PeerBase     = "X-Axonhub-Dict-Base"
	PeerDigest   = "X-Axonhub-Dict-Digest"
	PeerAck      = "X-Axonhub-Dict-Ack"
	PeerMiss     = "X-Axonhub-Dict-Miss"
	peerMin      = 16 << 10
	peerSample   = 1 << 20
	peerSlots    = 32
	peerTTL      = 10 * time.Minute
)

var ErrPeerDictionaryMiss = errors.New("previous gateway dictionary unavailable")

type dictionaryEntry struct {
	id      string
	data    []byte
	expires time.Time
}
type PeerDictionaries struct {
	salt     [32]byte
	initSalt sync.Once
	mu       sync.Mutex
	entries  map[[32]byte]dictionaryEntry
	timer    *time.Timer
}

func peerHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (p *PeerDictionaries) get(key [32]byte) dictionaryEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[key]
	if time.Now().Before(e.expires) {
		return e
	}
	return dictionaryEntry{}
}
func (p *PeerDictionaries) remember(key [32]byte, body []byte, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = make(map[[32]byte]dictionaryEntry)
	}
	if _, ok := p.entries[key]; !ok && len(p.entries) >= peerSlots {
		var oldest [32]byte
		var at time.Time
		for k, e := range p.entries {
			if at.IsZero() || e.expires.Before(at) {
				oldest, at = k, e.expires
			}
		}
		delete(p.entries, oldest)
	}
	e := p.entries[key]
	if e.id != id {
		e.data = bytes.Clone(body[:min(len(body), peerSample)])
	}
	e.id, e.expires = id, time.Now().Add(peerTTL)
	p.entries[key] = e
	if p.timer == nil {
		p.timer = time.AfterFunc(peerTTL, p.expire)
	}
}
func (p *PeerDictionaries) expire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var next time.Time
	for k, e := range p.entries {
		if !now.Before(e.expires) {
			delete(p.entries, k)
		} else if next.IsZero() || e.expires.Before(next) {
			next = e.expires
		}
	}
	p.timer = nil
	if !next.IsZero() {
		p.timer = time.AfterFunc(time.Until(next), p.expire)
	}
}

type peerRequestKey struct{}
type peerEncodingTimeKey struct{}
type PeerRequest struct {
	Dictionaries *PeerDictionaries
	Endpoints    []string
	Scope        string
}
type peerUpload struct {
	progress func(TransportPhase, time.Time)
	cache    *PeerDictionaries
	scope    string
	body     []byte
	key      [32]byte
}

func (p *PeerRequest) bind(ctx context.Context, raw *http.Request, r *Request) context.Context {
	if p == nil || p.Dictionaries == nil || p.Scope == "" || raw.Method != "POST" || raw.URL.Scheme != "https" ||
		len(r.Body) < peerMin || len(r.Body) > maxRequestBodySize || !slices.Contains(p.Endpoints, raw.URL.String()) ||
		slices.Contains(r.NoZstdEndpoints, raw.URL.String()) || raw.Header.Get("Content-Encoding") != "" {
		return ctx
	}
	media, _, _ := strings.Cut(raw.Header.Get("Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(media), "application/json") {
		return ctx
	}
	p.Dictionaries.initSalt.Do(func() { rand.Read(p.Dictionaries.salt[:]) })
	h := hmac.New(sha256.New, p.Dictionaries.salt[:])
	io.WriteString(h, p.Scope)
	scope := hex.EncodeToString(h.Sum(nil))
	key := sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%x/%s/%s", r.EncodingScope, r.EncodingGeneration, encodingAccount(raw.Header), raw.URL.String(), scope)))
	return context.WithValue(ctx, peerRequestKey{}, &peerUpload{cache: p.Dictionaries, scope: scope, body: r.Body, key: key, progress: r.OnHTTPProgress})
}

func (p *peerUpload) roundTrip(req *http.Request, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	offer := req.Clone(req.Context())
	offer.Header.Set(PeerOffer, p.scope)
	observer, _ := req.Context().Value(transportObserverKey{}).(*TransportTrace)
	wire := offer
	base := p.cache.get(p.key)
	if base.id != "" && req.Context().Err() == nil && encodingbudget.TryAcquire() {
		started := time.Now()
		if p.progress != nil {
			p.progress(TransportEncoding, started)
		}
		encoded, err := encodePeerBody(base.data, p.body)
		if p.progress != nil {
			p.progress(TransportStarted, time.Now())
		}
		encodingbudget.Release()
		if err == nil && len(encoded) < len(p.body) {
			wire = offer.Clone(context.WithValue(offer.Context(), peerEncodingTimeKey{}, time.Since(started)))
			wire.Body = io.NopCloser(bytes.NewReader(encoded))
			wire.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(encoded)), nil }
			wire.ContentLength = int64(len(encoded))
			wire.Header.Set("Content-Encoding", PeerEncoding)
			wire.Header.Set(PeerBase, base.id)
			wire.Header.Set(PeerDigest, peerHash(p.body))
			observer.trackExpectBody(wire)
		}
	}
	res, err := send(wire)
	if err == nil && wire != offer && res.StatusCode == http.StatusConflict && res.Header.Get(PeerMiss) == base.id && req.Context().Err() == nil {
		// This acknowledgement is generated before inference, only by a configured peer.
		res.Body.Close()
		p.cache.mu.Lock()
		if p.cache.entries[p.key].id == base.id {
			delete(p.cache.entries, p.key)
		}
		p.cache.mu.Unlock()
		observer.trackExpectBody(offer)
		res, err = send(offer)
	} else if wire != offer {
		req.Body.Close()
	}
	if res != nil {
		if ack := res.Header.Get(PeerAck); ack != "" && ack == peerHash(p.body[:min(len(p.body), peerSample)]) {
			p.cache.remember(p.key, p.body, ack)
		}
		res.Header.Del(PeerAck)
		res.Header.Del(PeerMiss)
	}
	return res, err
}

func encodePeerBody(dict, body []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderDictRaw(1, dict), zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	return enc.EncodeAll(body, nil), nil
}

// Receive validates and removes hop-local metadata before calling any inference handler.
// scope is the authenticated inbound identity; the sender's namespace alone cannot select a dictionary.
func (p *PeerDictionaries) Receive(r *http.Request, h http.Header, scope string, lease *BodyLease) error {
	offer, base, digest := r.Header.Get(PeerOffer), r.Header.Get(PeerBase), r.Header.Get(PeerDigest)
	for _, k := range []string{PeerOffer, PeerBase, PeerDigest, PeerAck, PeerMiss} {
		r.Header.Del(k)
	}
	encoded := strings.EqualFold(r.Header.Get("Content-Encoding"), PeerEncoding)
	if len(offer) != 64 {
		offer = ""
	}
	rawScope, err := hex.DecodeString(offer)
	if scope == "" || err != nil || len(rawScope) != 32 {
		if encoded {
			return errors.New("gateway dictionary coding is unavailable")
		}
		return nil
	}
	if p == nil {
		if encoded && len(base) == 64 {
			h.Set(PeerMiss, base)
			return ErrPeerDictionaryMiss
		}
		if encoded {
			return errors.New("gateway dictionary coding is unavailable")
		}
		return nil
	}
	key := sha256.Sum256([]byte(scope + "\x00" + r.URL.EscapedPath() + "\x00" + offer))
	var body []byte
	if encoded {
		entry := p.get(key)
		if base == "" || len(base) != 64 {
			return errors.New("invalid gateway dictionary ID")
		}
		if entry.id != base {
			h.Set(PeerMiss, base)
			return ErrPeerDictionaryMiss
		}
		decoder, err := zstd.NewReader(io.LimitReader(r.Body, int64(maxRequestBodySize)+1), zstd.WithDecoderDictRaw(1, entry.data), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(maxRequestBodySize)))
		if err != nil {
			return err
		}
		body, err = readLimited(lease.Reader(decoder, 0), maxRequestBodySize)
		decoder.Close()
		if err != nil {
			return err
		}
		if peerHash(body) != digest {
			return errors.New("gateway request digest mismatch")
		}
		r.Header.Del("Content-Encoding")
		r.Header.Del("Content-Length")
		r.ContentLength = int64(len(body))
		r.Body = &inspectedRequestBody{Reader: bytes.NewReader(body), Closer: r.Body, decoded: body, readAt: time.Now()}
	} else {
		body, err = InspectBudgetedBody(r, lease)
		if err != nil {
			// An offer is optional on an ordinary body. Its cached read error
			// will be rendered by the normal protocol handler.
			if errors.Is(err, ErrBodyBudget) {
				return err
			}
			return nil
		}
	}
	if len(body) >= peerMin {
		id := peerHash(body[:min(len(body), peerSample)])
		p.remember(key, body, id)
		h.Set(PeerAck, id)
	}
	return nil
}
