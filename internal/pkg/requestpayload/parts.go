// Package requestpayload inspects only protocol-defined content positions.
// It never interprets JSON-looking tool text or opaque encrypted state.
package requestpayload

import (
	"strings"

	"github.com/tidwall/gjson"
)

type ImageSummary struct {
	Count                     int `json:"count"`
	InlineCount               int `json:"inline_count"`
	InlineEncodedBytes        int `json:"inline_encoded_bytes"`
	LargestInlineEncodedBytes int `json:"largest_inline_encoded_bytes"`
	ToolCount                 int `json:"tool_count,omitempty"`
}

// ContentSummary partitions raw JSON bytes; image metadata is included in ImageBytes.
// Counts do not estimate tokens or provider-side image processing cost.
type ContentSummary struct {
	ImageBytes     int `json:"image_bytes,omitempty"`
	TextBytes      int `json:"text_bytes,omitempty"`
	ToolTextBytes  int `json:"tool_text_bytes,omitempty"`
	FileBytes      int `json:"file_bytes,omitempty"`
	EncryptedBytes int `json:"encrypted_bytes,omitempty"`
	OtherBytes     int `json:"other_bytes,omitempty"`
}

type imagePart struct {
	value  gjson.Result
	mime   string
	prefix string
	data   string
}

func Inspect(body []byte) (*ImageSummary, *ContentSummary) {
	images, content := walk(body, nil)
	if content == nil {
		return nil, nil
	}
	if images.Count == 0 {
		return nil, content
	}
	return &images, content
}

func walk(body []byte, visit func(imagePart)) (ImageSummary, *ContentSummary) {
	var images ImageSummary
	content := &ContentSummary{}
	if !gjson.ValidBytes(body) {
		return images, nil
	}
	root := gjson.ParseBytes(body)
	countImage := func(part, value gjson.Result, mime string, tool bool, dataOnly bool) {
		images.Count++
		if tool {
			images.ToolCount++
		}
		content.ImageBytes += len(part.Raw)
		if value.Type != gjson.String {
			return
		}
		data, prefix := value.String(), ""
		if !dataOnly {
			if !strings.HasPrefix(data, "data:") {
				return
			}
			comma := strings.IndexByte(data, ',')
			if comma < 0 || !strings.HasSuffix(data[:comma], ";base64") {
				return
			}
			prefix, data = data[:comma+1], data[comma+1:]
			mime = strings.TrimSuffix(prefix[5:len(prefix)-1], ";base64")
		}
		images.InlineCount++
		images.InlineEncodedBytes += len(data)
		images.LargestInlineEncodedBytes = max(images.LargestInlineEncodedBytes, len(data))
		// Signed native parts are opaque history; preserve the original
		// encoding even when the decoded pixels would remain identical.
		preserve := part.Get("thoughtSignature").Exists() || part.Get("thought_signature").Exists()
		if visit != nil && !preserve {
			visit(imagePart{value: value, mime: mime, prefix: prefix, data: data})
		}
	}
	var parts func(gjson.Result, bool)
	var item func(gjson.Result, bool)
	text := func(value gjson.Result, tool bool) {
		if !value.Exists() {
			return
		}
		if tool {
			content.ToolTextBytes += len(value.Raw)
		} else {
			content.TextBytes += len(value.Raw)
		}
	}
	parts = func(value gjson.Result, tool bool) {
		if value.IsArray() {
			value.ForEach(func(_, part gjson.Result) bool { item(part, tool); return true })
		} else if value.Type == gjson.String {
			text(value, tool)
		}
	}
	item = func(part gjson.Result, tool bool) {
		switch part.Get("type").String() {
		case "input_image":
			countImage(part, part.Get("image_url"), "", tool, false)
			return
		case "image_url":
			countImage(part, part.Get("image_url.url"), "", tool, false)
			return
		case "image":
			source := part.Get("source")
			if source.Get("type").String() == "base64" {
				countImage(part, source.Get("data"), source.Get("media_type").String(), tool, true)
			} else {
				countImage(part, gjson.Result{}, "", tool, false)
			}
			return
		case "input_file", "file", "document":
			content.FileBytes += len(part.Raw)
			return
		case "input_text", "output_text", "text":
			text(part.Get("text"), tool)
			return
		case "function_call_output", "custom_tool_call_output":
			parts(part.Get("output"), true)
			return
		case "tool_result":
			parts(part.Get("content"), true)
			return
		case "reasoning", "compaction", "compaction_summary":
			content.EncryptedBytes += len(part.Get("encrypted_content").Raw)
			return
		case "image_generation_call":
			// Generated results may carry signatures/identity; count without rewriting.
			content.ImageBytes += len(part.Raw)
			if result := part.Get("result"); result.Type == gjson.String && result.String() != "" {
				images.Count++
				images.ToolCount++
				images.InlineCount++
				images.InlineEncodedBytes += len(result.String())
				images.LargestInlineEncodedBytes = max(images.LargestInlineEncodedBytes, len(result.String()))
			}
			return
		}
		for _, field := range []string{"inlineData", "inline_data"} {
			if inline := part.Get(field); inline.Exists() {
				mime := inline.Get("mimeType").String()
				if mime == "" {
					mime = inline.Get("mime_type").String()
				}
				if strings.HasPrefix(mime, "image/") {
					countImage(part, inline.Get("data"), mime, tool, true)
				} else {
					content.FileBytes += len(part.Raw)
				}
				return
			}
		}
		if part.Get("fileData").Exists() || part.Get("file_data").Exists() {
			content.FileBytes += len(part.Raw)
			return
		}
		if value := part.Get("text"); value.Exists() {
			text(value, tool)
		}
		parts(part.Get("content"), tool || part.Get("role").String() == "tool")
		parts(part.Get("parts"), tool)
	}
	for _, field := range []string{"input", "messages"} {
		parts(root.Get(field), false)
	}
	geminiParts := func(payload gjson.Result) {
		parts(payload.Get("contents"), false)
		item(payload.Get("systemInstruction"), false)
		item(payload.Get("system_instruction"), false)
	}
	geminiParts(root)
	// Antigravity wraps the Gemini payload in a protocol-defined envelope.
	// Do not recurse into arbitrary request objects in tool arguments or text.
	if root.Get("requestType").String() == "agent" && root.Get("userAgent").String() == "antigravity" {
		geminiParts(root.Get("request"))
	}
	parts(root.Get("system"), false)
	text(root.Get("instructions"), false)
	content.OtherBytes = len(body) - content.ImageBytes - content.TextBytes - content.ToolTextBytes - content.FileBytes - content.EncryptedBytes
	return images, content
}
