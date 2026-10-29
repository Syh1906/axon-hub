package imagebilling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/netip"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/gemini"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/stretchr/testify/require"
)

func TestExtractFinalImages(t *testing.T) {
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 16, 32))))
	data := base64.StdEncoding.EncodeToString(buffer.Bytes())
	tests := []struct {
		name, request, response, tier, source string
		width, count                          int
	}{
		{"image bytes override response size", `{}`, fmt.Sprintf(`{"size":"4096x4096","data":[{"b64_json":%q}]}`, data), "1K", "file", 16, 1},
		{"seedream per image metadata", `{}`, `{"data":[{"url":"unused","size":"2048x2048"},{"url":"unused","size":"4096x4096"}]}`, "2K", "response", 2048, 2},
		{"grok request resolution", `{"resolution":"2k"}`, `{"data":[{"url":"unused"}]}`, "2K", "request_tier", 0, 1},
		{"response resolution", `{}`, `{"data":[{"url":"unused","size":"4K"}]}`, "4K", "response_tier", 0, 1},
		{"auto is unknown", `{"size":"auto","n":5}`, `{"data":[{"url":"unused"}]}`, "", "unknown", 0, 1},
		{"gemini", `{"generationConfig":{"imageConfig":{"imageSize":"2K"}}}`, fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, data), "2K", "file", 16, 1},
		{"gemini thought ignored", `{}`, fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"thought":true,"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, data), "", "", 0, 0},
		{"responses completed", `{}`, fmt.Sprintf(`{"output":[{"type":"image_generation_call","status":"completed","result":%q}]}`, data), "1K", "file", 16, 1},
		{"responses failed not counted", `{}`, fmt.Sprintf(`{"output":[{"type":"image_generation_call","status":"failed","result":%q}]}`, data), "", "", 0, 0},
		{"completed response does not revive failed image", `{}`, fmt.Sprintf(`{"status":"completed","output":[{"type":"image_generation_call","status":"failed","result":%q}]}`, data), "", "", 0, 0},
		{"generating without completed response ignored", `{}`, fmt.Sprintf(`{"output":[{"type":"image_generation_call","status":"generating","result":%q}]}`, data), "", "", 0, 0},
		{"prompt and preview ignored", fmt.Sprintf(`{"image":%q}`, data), fmt.Sprintf(`{"type":"response.image_generation_call.partial_image","partial_image_b64":%q}`, data), "", "", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outputs := Extract(context.Background(), []byte(tc.request), []byte(tc.response), "proxy-model", nil)
			require.Len(t, outputs, tc.count)
			if tc.count > 0 {
				require.Equal(t, tc.width, outputs[0].Width)
				require.Equal(t, tc.tier, outputs[0].Tier)
				require.Equal(t, tc.source, outputs[0].Source)
			}
		})
	}
}

func TestExtractAggregatedImageStreams(t *testing.T) {
	ctx := context.Background()
	encode := func(edge int) string {
		var buffer bytes.Buffer
		require.NoError(t, png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, edge, edge))))
		return base64.StdEncoding.EncodeToString(buffer.Bytes())
	}
	small, large := encode(16), encode(2048)
	chunks := func(events ...string) []*httpclient.StreamEvent {
		result := make([]*httpclient.StreamEvent, len(events))
		for i, event := range events {
			result[i] = &httpclient.StreamEvent{Data: []byte(event)}
		}
		return result
	}
	t.Run("chat preserves final images and mixed text", func(t *testing.T) {
		body, meta, err := openai.AggregateStreamChunks(ctx, chunks(
			`{"choices":[{"index":0,"delta":{"content":"before"}}]}`,
			fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}}]}`, small),
			fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":[{"type":"text","text":"between"},{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}}]}`, large),
			`{"choices":[{"index":0,"delta":{"content":"after"},"finish_reason":"stop"}]}`,
		), openai.DefaultTransformChunk)
		require.NoError(t, err)
		require.Nil(t, meta.Usage)
		outputs := Extract(ctx, nil, body, "proxy", nil)
		require.Len(t, outputs, 2)
		require.Equal(t, "1K", outputs[0].Tier)
		require.Equal(t, "2K", outputs[1].Tier)
		var response openai.Response
		require.NoError(t, json.Unmarshal(body, &response))
		require.Len(t, response.Choices[0].Message.Content.MultipleContent, 5)
	})
	t.Run("gemini preserves thought marker before extraction", func(t *testing.T) {
		body, _, err := gemini.AggregateStreamChunks(ctx, chunks(
			fmt.Sprintf(`{"candidates":[{"index":0,"content":{"parts":[{"thought":true,"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, small),
			fmt.Sprintf(`{"candidates":[{"index":0,"content":{"parts":[{"thoughtSignature":"signature","inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, large),
		))
		require.NoError(t, err)
		outputs := Extract(ctx, nil, body, "gemini", nil)
		require.Len(t, outputs, 1)
		require.Equal(t, 2048, outputs[0].Width)
		require.Contains(t, string(body), `"thoughtSignature":"signature"`)
	})
	t.Run("codex done result followed by completed response", func(t *testing.T) {
		body, meta, err := responses.AggregateStreamChunks(ctx, chunks(
			`{"type":"response.output_item.added","item":{"id":"ig_1","type":"image_generation_call","status":"in_progress"},"output_index":0}`,
			`{"type":"response.image_generation_call.partial_image","item_id":"ig_1","output_index":0,"partial_image_b64":"preview","partial_image_index":0}`,
			`{"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","status":"generating","result":"iVBORw0KGgo=","size":"1254x1254"},"output_index":0}`,
			`{"type":"response.completed","response":{"id":"resp_x","status":"completed","output":[]}}`,
		))
		require.NoError(t, err)
		require.Nil(t, meta.Usage)
		outputs := Extract(ctx, nil, body, "codex", nil)
		require.Len(t, outputs, 1)
		require.Equal(t, 1254, outputs[0].Width)
		require.Equal(t, "2K", outputs[0].Tier)
	})
}

func TestAutomaticDimensionsAreNotLongestEdge(t *testing.T) {
	require.Equal(t, "1K", TierForDimensions("gemini-3-pro-image-preview", 1376, 768))
	require.Equal(t, "2K", TierForDimensions("gemini-3-pro-image-preview", 2752, 1536))
	require.Equal(t, "1K", TierForDimensions("proxy", 2048, 512))
	require.Equal(t, "2K", TierForDimensions("proxy", 1536, 1024))
	require.Equal(t, "", TierForDimensions("proxy", 8192, 8192))
	require.Equal(t, "", TierForDimensions("proxy", -1, 1024))
}

func TestImageFetchRejectsPrivateDestinations(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "::ffff:127.0.0.1", "192.0.2.1"} {
		require.False(t, publicAddress(netip.MustParseAddr(address).Unmap()), address)
	}
	require.True(t, publicAddress(netip.MustParseAddr("8.8.8.8")))
	_, err := Fetch(context.Background(), "http://127.0.0.1:8090/private")
	require.Error(t, err)
	_, err = Fetch(context.Background(), "file:///etc/passwd")
	require.Error(t, err)
}
