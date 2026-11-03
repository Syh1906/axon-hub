package biz

import (
	"context"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/objects"
)

// These fixed-size numbers survive expiry/oversize rejection of debug copies.
// They never retain image data, URLs, file IDs, prompt text, or per-image lists.
type requestImageSummary struct {
	Count                     int `json:"count"`
	InlineCount               int `json:"inline_count"`
	InlineEncodedBytes        int `json:"inline_encoded_bytes"`
	LargestInlineEncodedBytes int `json:"largest_inline_encoded_bytes"`
}

func (s *RequestPayloadService) saveRequestDebug(ctx context.Context, body []byte) objects.JSONRawMessage {
	stored := s.saveDebug(ctx, body)
	ref, _ := parsePayloadMarker(stored)
	if ref.State != "invalid" {
		ref.Images = summarizeRequestImages(body)
	}
	return payloadMarker(ref)
}

// Count only protocol image parts, never JSON-looking text or tool arguments.
// Encoded bytes describe the supplied inline data, not the provider's relay size.
func summarizeRequestImages(body []byte) *requestImageSummary {
	if !gjson.ValidBytes(body) {
		return nil
	}
	root := gjson.ParseBytes(body)
	var summary requestImageSummary
	countPart := func(part gjson.Result) {
		var url string
		switch part.Get("type").String() {
		case "input_image":
			url = part.Get("image_url").String()
		case "image_url":
			url = part.Get("image_url.url").String()
		default:
			return
		}
		summary.Count++
		if strings.HasPrefix(url, "data:") {
			if comma := strings.IndexByte(url, ','); comma >= 0 {
				size := len(url) - comma - 1
				summary.InlineCount++
				summary.InlineEncodedBytes += size
				summary.LargestInlineEncodedBytes = max(summary.LargestInlineEncodedBytes, size)
			}
		}
	}
	for _, field := range []string{"input", "messages"} {
		items := root.Get(field)
		if !items.IsArray() {
			continue
		}
		items.ForEach(func(_, item gjson.Result) bool {
			countPart(item)
			content := item.Get("content")
			if content.IsArray() {
				content.ForEach(func(_, part gjson.Result) bool {
					countPart(part)
					return true
				})
			}
			return true
		})
	}
	if summary.Count == 0 {
		return nil
	}
	return &summary
}
