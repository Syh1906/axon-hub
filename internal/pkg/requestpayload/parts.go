// Package requestpayload inspects only protocol-defined content positions.
// It never interprets JSON-looking tool text or opaque encrypted state.
package requestpayload

import (
	"bytes"
	"encoding/base64"
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

// Analysis indexes one immutable body for admission, persistence and optional PNG rewriting.
type Analysis struct {
	Images   *ImageSummary
	Content  *ContentSummary
	body     []byte
	pngs     []pngLocation
	eligible int
}

type pngLocation struct {
	start, end int
	prefix     string
}

// HasPNG reports whether there is a rewritable inline PNG at a supported protocol position.
func (a *Analysis) HasPNG() bool { return len(a.pngs) != 0 }

// Analyze keeps offsets into body, without retaining decoded copies of image strings.
func Analyze(body []byte) *Analysis {
	a := &Analysis{body: body}
	var collect func(imagePart)
	if len(body) >= MinImageBytes {
		collect = func(p imagePart) {
			a.eligible++
			if pos, ok := p.pngLocation(body); ok {
				a.pngs = append(a.pngs, pos)
			}
		}
	}
	images, content := walk(body, collect)
	if images.Count != 0 {
		a.Images = &images
	}
	a.Content = content
	return a
}

func (p imagePart) pngLocation(body []byte) (pngLocation, bool) {
	if p.mime != "image/png" || len(p.data) < MinImageBytes || len(p.data) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return pngLocation{}, false
	}
	start, end := p.value.Index, p.value.Index+len(p.value.Raw)
	if start < 0 || end > len(body) || string(body[start:end]) != p.value.Raw {
		return pngLocation{}, false
	}
	// A protocol prefix can alias gjson's entire multi-megabyte source string.
	// Keep only the short prefix in the per-request index.
	return pngLocation{start: start, end: end, prefix: strings.Clone(p.prefix)}, true
}

func (p pngLocation) data(body []byte) []byte {
	raw := body[p.start+1 : p.end-1]
	if bytes.IndexByte(raw, '\\') >= 0 {
		return []byte(gjson.ParseBytes(body[p.start:p.end]).String()[len(p.prefix):])
	}
	return raw[len(p.prefix):]
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
	countImage := func(part, value gjson.Result, mime string, tool, dataOnly, signed bool) {
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
		if visit != nil && !signed {
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
		// Index each object's fields once. Repeated Get calls for absent fields
		// otherwise rescan the same multi-megabyte inline image or tool output.
		fields := part.Map()
		signed := visit != nil && (fields["thoughtSignature"].Exists() || fields["thought_signature"].Exists())
		switch fields["type"].String() {
		case "input_image":
			countImage(part, fields["image_url"], "", tool, false, signed)
			return
		case "image_url":
			countImage(part, fields["image_url"].Get("url"), "", tool, false, signed)
			return
		case "image":
			source := fields["source"].Map()
			if source["type"].String() == "base64" {
				countImage(part, source["data"], source["media_type"].String(), tool, true, signed)
			} else {
				countImage(part, gjson.Result{}, "", tool, false, signed)
			}
			return
		case "input_file", "file", "document":
			content.FileBytes += len(part.Raw)
			return
		case "input_text", "output_text", "text":
			text(fields["text"], tool)
			return
		case "function_call_output", "custom_tool_call_output":
			parts(fields["output"], true)
			return
		case "tool_result":
			parts(fields["content"], true)
			return
		case "reasoning", "compaction", "compaction_summary":
			content.EncryptedBytes += len(fields["encrypted_content"].Raw)
			return
		case "image_generation_call":
			// Generated results may carry signatures/identity; count without rewriting.
			content.ImageBytes += len(part.Raw)
			if result := fields["result"]; result.Type == gjson.String && result.String() != "" {
				images.Count++
				images.ToolCount++
				images.InlineCount++
				images.InlineEncodedBytes += len(result.String())
				images.LargestInlineEncodedBytes = max(images.LargestInlineEncodedBytes, len(result.String()))
			}
			return
		}
		for _, field := range []string{"inlineData", "inline_data"} {
			if inline := fields[field]; inline.Exists() {
				data := inline.Map()
				mime := data["mimeType"].String()
				if mime == "" {
					mime = data["mime_type"].String()
				}
				if strings.HasPrefix(mime, "image/") {
					countImage(part, data["data"], mime, tool, true, signed)
				} else {
					content.FileBytes += len(part.Raw)
				}
				return
			}
		}
		if fields["fileData"].Exists() || fields["file_data"].Exists() {
			content.FileBytes += len(part.Raw)
			return
		}
		if value := fields["text"]; value.Exists() {
			text(value, tool)
		}
		parts(fields["content"], tool || fields["role"].String() == "tool")
		parts(fields["parts"], tool)
	}
	fields := root.Map()
	for _, field := range []string{"input", "messages"} {
		parts(fields[field], false)
	}
	geminiParts := func(payload map[string]gjson.Result) {
		parts(payload["contents"], false)
		item(payload["systemInstruction"], false)
		item(payload["system_instruction"], false)
	}
	geminiParts(fields)
	// Antigravity wraps the Gemini payload in a protocol-defined envelope.
	// Do not recurse into arbitrary request objects in tool arguments or text.
	if fields["requestType"].String() == "agent" && fields["userAgent"].String() == "antigravity" {
		geminiParts(fields["request"].Map())
	}
	parts(fields["system"], false)
	text(fields["instructions"], false)
	content.OtherBytes = len(body) - content.ImageBytes - content.TextBytes - content.ToolTextBytes - content.FileBytes - content.EncryptedBytes
	return images, content
}
