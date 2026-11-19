package httpclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/looplj/axonhub/llm/encodingbudget"
)

const minZstdRequestBytes = 1 << 20

type requestEncodingKey struct{}

type requestEncodingInput struct {
	body       []byte
	endpoints  []string
	disabled   []string
	learn      bool
	scope      int
	generation int64
	measure    bool
	progress   func(TransportPhase, time.Time)
}

func encodingKey(scope int, generation int64, url string, header http.Header) encodingResource {
	return encodingResource{scope: scope, generation: generation, url: sha256.Sum256([]byte(url)), account: encodingAccount(header)}
}

// UploadEstimate contains only evidence from the selected HTTP transport and account.
type UploadEstimate struct {
	Resource      [32]byte
	NSPerByte     float64
	PreferZstd    bool
	ZstdSavingsNS float64
}

// EstimateUpload is used only for large inline images, before optional PNG work.
// It never reserves an encoding trial or sends a request.
func (hc *HttpClient) EstimateUpload(request *Request) UploadEstimate {
	if hc == nil || len(request.Body) < minZstdRequestBytes || request.ResponsesWebSocket != nil {
		return UploadEstimate{}
	}
	t, ok := hc.client.Transport.(*recoveringTransport)
	if !ok {
		return UploadEstimate{}
	}
	copy := *request
	copy.Headers = request.Headers.Clone()
	raw, err := BuildHttpRequest(context.Background(), &copy)
	if err != nil {
		return UploadEstimate{}
	}
	url := raw.URL.String()
	key := encodingKey(request.EncodingScope, request.EncodingGeneration, url, raw.Header)
	now := time.Now()
	t.encodings.mu.Lock()
	e := t.encodings.entries[key]
	t.encodings.mu.Unlock()
	cost := e.costs[encodingBucket(len(request.Body))]
	resource := sha256.New()
	resource.Write(key.url[:])
	resource.Write(key.account[:])
	var estimate UploadEstimate
	resource.Sum(estimate.Resource[:0])
	if now.Sub(cost.plainAt) < encodingEvidenceTTL {
		estimate.NSPerByte = cost.plainPerByte
	}
	if !slices.Contains(request.NoZstdEndpoints, url) && !now.Before(e.blockedUntil) && raw.Header.Get("Content-Encoding") == "" {
		estimate.PreferZstd = slices.Contains(request.ZstdEndpoints, url) || request.LearnZstd && now.Before(e.until) &&
			(cost.cpuPerByte == 0 || cost.savedFraction*estimate.NSPerByte > cost.cpuPerByte || !now.Before(cost.nextTrial))
		estimate.ZstdSavingsNS = float64(len(request.Body)) * (cost.savedFraction*estimate.NSPerByte - cost.cpuPerByte)
	}
	return estimate
}

var requestEncoder = sync.OnceValue(func() *zstd.Encoder {
	// Base64 image data has a restricted alphabet even in blocks without matches.
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithAllLitEntropyCompression(true), zstd.WithEncoderConcurrency(encodingbudget.Capacity))
	if err != nil {
		panic(err) // The options and concurrency are fixed.
	}
	return encoder
})

// Encode only the native HTTP hop. The redirect policy and the pipeline see
// the original body, as do WebSocket, persistence and billing.
func (c *requestEncodingCache) prepare(req *http.Request) (*http.Request, *encodingAttempt) {
	source, ok := req.Context().Value(requestEncodingKey{}).(requestEncodingInput)
	if !ok || req.Context().Err() != nil || req.Response != nil || req.Method != http.MethodPost || req.ContentLength != int64(len(source.body)) ||
		req.Header.Get("Content-Encoding") != "" {
		return req, nil
	}
	mediaType, _, _ := strings.Cut(req.Header.Get("Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(mediaType), "application/json") {
		return req, nil
	}
	url := req.URL.String()
	enabled := !slices.Contains(source.disabled, url)
	explicit := enabled && slices.Contains(source.endpoints, url)
	learn := enabled && source.learn
	if !explicit && !learn && !source.measure {
		return req, nil
	}
	a := &encodingAttempt{
		start: time.Now(), size: len(source.body), explicit: explicit, measure: source.measure,
	}
	if a.size < minZstdRequestBytes {
		if !learn && !explicit {
			return req, nil
		}
		a.small, a.key.scope, a.key.generation = req, source.scope, source.generation
		return req, a
	}
	a.key = encodingKey(source.scope, source.generation, url, req.Header)
	if !explicit && !learn || !c.shouldEncode(a) {
		return req, a
	}
	if !encodingbudget.TryAcquire() {
		return req, a
	}
	defer encodingbudget.Release()
	if source.progress != nil {
		source.progress(TransportEncoding, time.Now())
		defer func() { source.progress(TransportStarted, time.Now()) }()
	}
	start := time.Now()
	encoded := requestEncoder().EncodeAll(source.body, nil)
	a.cpu, a.saved = time.Since(start), len(source.body)-len(encoded)
	if len(encoded) >= len(source.body) {
		return req, a
	}
	wire := req.Clone(req.Context())
	wire.Header.Set("Content-Encoding", "zstd")
	wire.ContentLength = int64(len(encoded))
	wire.Body = io.NopCloser(bytes.NewReader(encoded))
	wire.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(encoded)), nil }
	// The transport closes wire.Body; this RoundTrip owns the original body.
	_ = req.Body.Close()
	a.encoded = true
	return wire, a
}
