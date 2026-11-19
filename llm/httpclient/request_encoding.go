package httpclient

import (
	"bytes"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const minZstdRequestBytes = 1 << 20

type requestEncodingKey struct{}

type requestEncodingInput struct {
	body      []byte
	endpoints []string
}

// When both encoders are busy, send the original JSON instead of queuing a
// latency-sensitive request behind another upload's optional compression.
var requestEncodingSlots = make(chan struct{}, 2)

var requestEncoder = sync.OnceValue(func() *zstd.Encoder {
	// Base64 image data has a restricted alphabet even in blocks without matches.
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithAllLitEntropyCompression(true), zstd.WithEncoderConcurrency(cap(requestEncodingSlots)))
	if err != nil {
		panic(err) // The options and concurrency are fixed.
	}
	return encoder
})

// Encode only the native HTTP hop. The redirect policy and the pipeline see
// the original body, as do WebSocket, persistence and billing.
func encodeConfiguredRequest(req *http.Request) *http.Request {
	source, ok := req.Context().Value(requestEncodingKey{}).(requestEncodingInput)
	if !ok || req.Response != nil || req.Method != http.MethodPost || req.ContentLength != int64(len(source.body)) ||
		req.Header.Get("Content-Encoding") != "" || !slices.Contains(source.endpoints, req.URL.String()) {
		return req
	}
	mediaType, _, _ := strings.Cut(req.Header.Get("Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(mediaType), "application/json") {
		return req
	}
	select {
	case requestEncodingSlots <- struct{}{}:
		defer func() { <-requestEncodingSlots }()
	default:
		return req
	}
	encoded := requestEncoder().EncodeAll(source.body, nil)
	if len(encoded) >= len(source.body) {
		return req
	}
	wire := req.Clone(req.Context())
	wire.Header.Set("Content-Encoding", "zstd")
	wire.ContentLength = int64(len(encoded))
	wire.Body = io.NopCloser(bytes.NewReader(encoded))
	wire.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(encoded)), nil }
	// The transport closes wire.Body; this RoundTrip owns the original body.
	_ = req.Body.Close()
	return wire
}
