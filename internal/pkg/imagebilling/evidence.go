// Package imagebilling extracts completed image outputs without depending on
// provider token usage or changing the response sent to the client.
package imagebilling

import (
	"context"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	_ "golang.org/x/image/webp"

	"github.com/looplj/axonhub/internal/objects"
)

type FetchImage func(context.Context, string) (image.Config, error)

// Extract visits only protocol-defined final output fields, never prompt images
// or streaming previews. Unknown outputs retain their place in the billing log.
func Extract(ctx context.Context, request, response []byte, model string, fetch FetchImage) []objects.ImageOutput {
	root := gjson.ParseBytes(response)
	requestedTier := firstString(gjson.ParseBytes(request), "resolution", "size", "generationConfig.imageConfig.imageSize", "generation_config.image_config.image_size")
	var outputs []objects.ImageOutput
	add := func(encoded, url, size, tier string, width, height int) {
		output := objects.ImageOutput{Source: "unknown"}
		if width > 0 && height > 0 {
			output.Width, output.Height, output.Source = width, height, "response"
		}
		if w, h := dimensions(size); w > 0 && h > 0 {
			output.Width, output.Height, output.Source = w, h, "response"
		}
		var config image.Config
		var err error
		if encoded != "" {
			if strings.HasPrefix(encoded, "data:") {
				_, encoded, _ = strings.Cut(encoded, ",")
			}
			config, _, err = image.DecodeConfig(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)), 1<<20))
		} else if strings.HasPrefix(url, "data:image/") {
			_, data, _ := strings.Cut(url, ",")
			config, _, err = image.DecodeConfig(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)), 1<<20))
		} else if url != "" && fetch != nil {
			config, err = fetch(ctx, url)
		}
		if err == nil && config.Width > 0 && config.Height > 0 {
			output.Width, output.Height, output.Source = config.Width, config.Height, "file"
		}
		// Explicit specification labels describe the provider's generation tier;
		// non-square 1K output need not fit inside a 1024-square pixel budget.
		if t := normalizeTier(tier); t != "" {
			output.Tier, output.TierSource = t, "response_tier"
		} else if t := normalizeTier(requestedTier); t != "" {
			output.Tier, output.TierSource = t, "request_tier"
		} else if output.Width > 0 && output.Height > 0 {
			output.Tier, output.TierSource = TierForDimensions(model, output.Width, output.Height), output.Source
		}
		if output.Source == "unknown" && output.TierSource != "" {
			output.Source = output.TierSource
		}
		outputs = append(outputs, output)
	}
	// OpenAI Images and compatible proxies (including per-image Seedream size).
	for _, item := range root.Get("data").Array() {
		encoded, url := item.Get("b64_json").String(), item.Get("url").String()
		if encoded == "" && url == "" {
			continue
		}
		size := item.Get("size").String()
		if size == "" {
			size = root.Get("size").String()
		}
		tier := firstString(item, "resolution", "image_size", "size")
		if normalizeTier(tier) == "" {
			tier = firstString(root, "resolution", "image_size", "size")
		}
		add(encoded, url, size, tier, int(item.Get("width").Int()), int(item.Get("height").Int()))
	}
	// Gemini native generateContent, including aggregated streams.
	for _, candidate := range root.Get("candidates").Array() {
		for _, part := range candidate.Get("content.parts").Array() {
			if part.Get("thought").Bool() {
				continue
			}
			inline := part.Get("inlineData")
			if !inline.Exists() {
				inline = part.Get("inline_data")
			}
			mime := firstString(inline, "mimeType", "mime_type")
			if strings.HasPrefix(mime, "image/") && inline.Get("data").String() != "" {
				add(inline.Get("data").String(), "", "", "", 0, 0)
			}
		}
	}
	// Responses image_generation_call results. Failed/in-progress calls do not bill.
	for _, item := range root.Get("output").Array() {
		if item.Get("type").String() != "image_generation_call" || item.Get("result").String() == "" {
			continue
		}
		status := item.Get("status").String()
		if status != "" && status != "completed" && !(status == "generating" && root.Get("status").String() == "completed") {
			continue
		}
		add(item.Get("result").String(), "", item.Get("size").String(), "", 0, 0)
	}
	// Some compatible chat endpoints return final image_url content parts.
	for _, choice := range root.Get("choices").Array() {
		for _, part := range choice.Get("message.content").Array() {
			if part.Get("type").String() == "image_url" && part.Get("image_url.url").String() != "" {
				add("", part.Get("image_url.url").String(), "", "", 0, 0)
			}
		}
	}
	return outputs
}

func firstString(root gjson.Result, paths ...string) string {
	for _, path := range paths {
		if value := root.Get(path).String(); value != "" {
			return value
		}
	}
	return ""
}

func normalizeTier(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "1K":
		return "1K"
	case "2K":
		return "2K"
	case "4K":
		return "4K"
	}
	return ""
}

func dimensions(size string) (int, int) {
	w, h, ok := strings.Cut(strings.ToLower(strings.TrimSpace(size)), "x")
	if !ok {
		return 0, 0
	}
	width, e1 := strconv.Atoi(w)
	height, e2 := strconv.Atoi(h)
	if e1 != nil || e2 != nil || width <= 0 || height <= 0 || width > 1000000 || height > 1000000 {
		return 0, 0
	}
	return width, height
}

// TierForDimensions defines AxonHub's automatic billing bands, not an upstream
// tariff. Known Gemini aspect-ratio sizes are matched before pixel-area bands.
func TierForDimensions(model string, width, height int) string {
	if width <= 0 || height <= 0 || width > 1000000 || height > 1000000 {
		return ""
	}
	if strings.Contains(strings.ToLower(model), "gemini") {
		// https://ai.google.dev/gemini-api/docs/generate-content/image-generation
		bases := [][2]int{{1024, 1024}, {512, 2048}, {384, 3072}, {848, 1264}, {896, 1200}, {928, 1152}, {768, 1376}, {672, 1584}}
		for _, base := range bases {
			for _, scale := range []int{1, 2, 4} {
				w, h := base[0]*scale, base[1]*scale
				if (width == w && height == h) || (width == h && height == w) {
					return strconv.Itoa(scale) + "K"
				}
			}
		}
	}
	area := int64(width) * int64(height)
	for _, edge := range []int64{1024, 2048, 4096} {
		if area <= edge*edge {
			return strconv.FormatInt(edge/1024, 10) + "K"
		}
	}
	return ""
}
