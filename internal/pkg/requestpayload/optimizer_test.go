package requestpayload

import (
	"bytes"
	"container/list"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 256, 128))
	for y := range 128 {
		for x := range 256 {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 123, uint8(x)})
		}
	}
	var data bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&data, img))
	// Preserve an ancillary text chunk byte-for-byte along with alpha and color.
	chunk := binary.BigEndian.AppendUint32(nil, 11)
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, "note\x00retain"...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	p := data.Bytes()
	result := append([]byte(nil), p[:33]...)
	result = append(result, chunk...)
	return append(result, p[33:]...)
}

func TestOptimizerPreservesPixelsMetadataHistoryAndProjectIsolation(t *testing.T) {
	original := testPNG(t)
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(original)
	encoded, _ := json.Marshal(imageURL)
	body := []byte(`{ "model":"模型", "input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":` + string(encoded) + `}]},{"type":"function_call_output","call_id":"keep","output":[{"type":"input_image","image_url":` + string(encoded) + `}]},{"type":"reasoning","encrypted_content":"opaque:do-not-edit"}]}`)
	var optimizer Optimizer
	result, stats := optimizer.Optimize(context.Background(), 1, body)
	require.Less(t, len(result), len(body))
	require.Equal(t, 2, stats.OptimizedImages)
	require.Equal(t, 1, stats.CacheHits)
	require.Equal(t, "keep", gjson.GetBytes(result, "input.1.call_id").String())
	require.Equal(t, "opaque:do-not-edit", gjson.GetBytes(result, "input.2.encrypted_content").String())
	require.True(t, bytes.HasPrefix(result, []byte(`{ "model":"模型", "input":`)))
	url := gjson.GetBytes(result, "input.0.content.0.image_url").String()
	decoded, err := base64.StdEncoding.DecodeString(url[len("data:image/png;base64,"):])
	require.NoError(t, err)
	require.Contains(t, string(decoded), "note\x00retain")
	want, err := png.Decode(bytes.NewReader(original))
	require.NoError(t, err)
	got, err := png.Decode(bytes.NewReader(decoded))
	require.NoError(t, err)
	require.Equal(t, want.Bounds(), got.Bounds())
	for y := range 128 {
		for x := range 256 {
			require.Equal(t, color.NRGBAModel.Convert(want.At(x, y)), color.NRGBAModel.Convert(got.At(x, y)))
		}
	}
	again, stats := optimizer.Optimize(context.Background(), 1, body)
	require.Equal(t, result, again)
	require.Equal(t, 2, stats.CacheHits)
	_, stats = optimizer.Optimize(context.Background(), 2, body)
	require.Equal(t, 1, stats.CacheHits, "different project must recompute the first occurrence")
}

func TestOptimizerLeavesUnsupportedInvalidAndCanceledInputs(t *testing.T) {
	var optimizer Optimizer
	for _, body := range [][]byte{[]byte(`{"input":"hello"}`), []byte(`invalid`), []byte(`{"input":[{"type":"image_generation_call","result":"original"}]}`)} {
		got, _ := optimizer.Optimize(context.Background(), 1, body)
		require.Equal(t, body, got)
	}
	image := testPNG(t)
	badCRC := bytes.Clone(image)
	badCRC[40] ^= 1
	require.Nil(t, recompressPNG(context.Background(), badCRC))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.Nil(t, recompressPNG(canceled, image))
}

func TestOptimizerPreservesSignedGeminiHistory(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(testPNG(t))
	for _, signature := range []string{"thoughtSignature", "thought_signature"} {
		t.Run(signature, func(t *testing.T) {
			body := []byte(`{"contents":[{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"` + data + `"},"` + signature + `":"opaque-image"}]}]}`)
			var optimizer Optimizer
			result, stats := optimizer.Optimize(t.Context(), 1, body)
			require.Equal(t, body, result, "signed generated history must preserve its original encoding")
			require.Zero(t, stats.OptimizedImages)
			images, content := Inspect(result)
			require.Equal(t, len(data), images.InlineEncodedBytes)
			require.Positive(t, content.ImageBytes)
		})
	}
}

func TestOptimizerHandlesAntigravityEnvelope(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(testPNG(t))
	body := []byte(`{ "project":"test-project", "model":"test-model", "requestType":"agent", "userAgent":"antigravity", "request":{"contents":[{"role":"user","parts":[{"text":"keep"},{"inlineData":{"mimeType":"image/png","data":"` + data + `"}}]}]}}`)
	var optimizer Optimizer
	result, stats := optimizer.Optimize(t.Context(), 1, body)
	require.Equal(t, 1, stats.OptimizedImages)
	require.Less(t, len(result), len(body))
	before := gjson.GetBytes(body, "request.contents.0.parts.1.inlineData.data")
	after := gjson.GetBytes(result, "request.contents.0.parts.1.inlineData.data")
	require.Equal(t, bytes.Replace(body, []byte(before.Raw), []byte(after.Raw), 1), result)
	images, content := Inspect(result)
	require.Equal(t, 1, images.Count)
	require.Equal(t, len(after.String()), images.InlineEncodedBytes)
	require.Equal(t, len(result), content.ImageBytes+content.TextBytes+content.ToolTextBytes+content.FileBytes+content.EncryptedBytes+content.OtherBytes)
	// An arbitrary object named request is not a known provider envelope.
	unknown := bytes.Replace(body, []byte(`"antigravity"`), []byte(`"another-client"`), 1)
	unchanged, _ := optimizer.Optimize(t.Context(), 1, unknown)
	require.Equal(t, unknown, unchanged)
}

func TestOptimizerCacheBoundAndExpiry(t *testing.T) {
	var optimizer Optimizer
	optimizer.once.Do(func() {
		optimizer.entries = make(map[cacheKey]*list.Element)
		optimizer.workers = make(chan struct{}, 2)
	})
	now := time.Now()
	for i := 0; i <= maxCacheEntries; i++ {
		optimizer.put(cacheKey{project: i}, "", now)
	}
	require.Len(t, optimizer.entries, maxCacheEntries)
	_, ok := optimizer.get(cacheKey{project: 0}, now)
	require.False(t, ok)
	_, ok = optimizer.get(cacheKey{project: maxCacheEntries}, now.Add(cacheTTL))
	require.False(t, ok)
}

func TestOptimizerBusyPreservesOriginalAndDoesNotFailRequest(t *testing.T) {
	image := testPNG(t)
	body := []byte(`{"input":[{"type":"input_image","image_url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(image) + `"}]}`)
	var optimizer Optimizer
	optimizer.once.Do(func() {
		optimizer.entries = make(map[cacheKey]*list.Element)
		optimizer.workers = make(chan struct{}, 2)
	})
	optimizer.workers <- struct{}{}
	optimizer.workers <- struct{}{}
	result, stats := optimizer.Optimize(context.Background(), 1, body)
	require.Equal(t, body, result)
	require.Equal(t, 1, stats.BusyImages)
	require.Zero(t, stats.OptimizedImages)
}

func BenchmarkOptimizerColdAndCached(b *testing.B) {
	var pngBytes bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 1024, 1024))); err != nil {
		b.Fatal(err)
	}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"` + url + `"}]}]}`)
	for _, cached := range []bool{false, true} {
		name := "cold"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			var optimizer Optimizer
			result, _ := optimizer.Optimize(context.Background(), 1, body)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				if cached {
					optimizer.Optimize(context.Background(), 1, body)
				} else {
					var cold Optimizer
					cold.Optimize(context.Background(), 1, body)
				}
			}
			b.ReportMetric(float64(len(result))/float64(len(body))*100, "output-%")
		})
	}
}

func TestOptimizerLargeSyntheticRequest(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2048, 1536))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+1] = 128
		img.Pix[i+3] = 255
	}
	var original bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&original, img))
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(original.Bytes())
	parts := make([]map[string]string, 4)
	for i := range parts {
		parts[i] = map[string]string{"type": "input_image", "image_url": url}
	}
	body, err := json.Marshal(map[string]any{"input": []any{map[string]any{"role": "user", "content": parts}}})
	require.NoError(t, err)
	var optimizer Optimizer
	result, stats := optimizer.Optimize(context.Background(), 42, body)
	require.Greater(t, len(body), 50_000_000)
	if stats.OptimizedImages == 0 {
		require.True(t, stats.BudgetExhausted, "slow hosts may preserve the original within the work budget")
		require.Equal(t, body, result)
	} else {
		require.Less(t, len(result), len(body))
	}
	t.Logf("synthetic request: before=%d after=%d optimized_images=%d cache_hits=%d processing_ms=%d budget_exhausted=%v", len(body), len(result), stats.OptimizedImages, stats.CacheHits, stats.DurationMs, stats.BudgetExhausted)
}
