package biz

import (
	"bytes"
	"context"
	"sync"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/requestpayload"
)

type requestImageSummary = requestpayload.ImageSummary

type bodyInspectionKey struct{}
type bodyInspection struct {
	mu      sync.Mutex
	body    []byte
	images  *requestpayload.ImageSummary
	content *requestpayload.ContentSummary
	marker  objects.JSONRawMessage
}

// WithRequestBodyInspection shares a single immutable body version across audit and admission.
func WithRequestBodyInspection(ctx context.Context) context.Context {
	return context.WithValue(ctx, bodyInspectionKey{}, new(bodyInspection))
}

func inspectRequestBody(ctx context.Context, body []byte) (*requestpayload.ImageSummary, *requestpayload.ContentSummary) {
	cache, _ := ctx.Value(bodyInspectionKey{}).(*bodyInspection)
	if cache == nil || len(body) == 0 {
		return requestpayload.Inspect(body)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.inspect(body)
	return cache.images, cache.content
}

func (cache *bodyInspection) inspect(body []byte) {
	if !bytes.Equal(cache.body, body) {
		cache.images, cache.content = requestpayload.Inspect(body)
		cache.body, cache.marker = body, nil
	}
}

func (s *RequestPayloadService) saveRequestDebug(ctx context.Context, body []byte) objects.JSONRawMessage {
	cache, _ := ctx.Value(bodyInspectionKey{}).(*bodyInspection)
	if cache != nil && len(body) > 0 {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		cache.inspect(body)
		if cache.marker != nil {
			return cache.marker
		}
		ref, _ := parsePayloadMarker(s.saveDebug(ctx, body))
		ref.Images, ref.Content = cache.images, cache.content
		marker := payloadMarker(ref)
		if ref.State == "cached" {
			cache.marker = marker
		}
		return marker
	}
	ref, _ := parsePayloadMarker(s.saveDebug(ctx, body))
	if ref.State != "invalid" {
		ref.Images, ref.Content = requestpayload.Inspect(body)
	}
	return payloadMarker(ref)
}

func summarizeRequestImages(body []byte) *requestImageSummary {
	images, _ := requestpayload.Inspect(body)
	return images
}
