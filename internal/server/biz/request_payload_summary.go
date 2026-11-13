package biz

import (
	"context"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/requestpayload"
)

type requestImageSummary = requestpayload.ImageSummary

func (s *RequestPayloadService) saveRequestDebug(ctx context.Context, body []byte) objects.JSONRawMessage {
	stored := s.saveDebug(ctx, body)
	ref, _ := parsePayloadMarker(stored)
	if ref.State != "invalid" {
		ref.Images, ref.Content = requestpayload.Inspect(body)
	}
	return payloadMarker(ref)
}

func summarizeRequestImages(body []byte) *requestImageSummary {
	images, _ := requestpayload.Inspect(body)
	return images
}
