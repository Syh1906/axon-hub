package gemini

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

const signedNativeParts = `[{"text":"FIRST","thought":true},{"text":"SECOND","thought":true},{"inlineData":{"mimeType":"image/png","data":"eA=="},"thoughtSignature":"opaque_image_1"},{"text":"between"},{"inlineData":{"mimeType":"image/png","data":"eQ=="},"thoughtSignature":"opaque_image_2"},{"functionCall":{"id":"call_1","name":"lookup","args":{}},"thoughtSignature":"opaque_call_1"},{"text":"after"}]`

const signedNativeFileParts = `[{"text":"before"},{"fileData":{"mimeType":"image/png","fileUri":"gs://test/image"},"thoughtSignature":"opaque_image"},{"text":"between"},{"fileData":{"mimeType":"video/quicktime","fileUri":"gs://test/video"},"thought":true,"thoughtSignature":"opaque_video"},{"fileData":{"mimeType":"audio/mpeg","fileUri":"gs://test/audio"},"thoughtSignature":"opaque_audio"},{"fileData":{"mimeType":"application/pdf","fileUri":"gs://test/document"},"thoughtSignature":"opaque_document"},{"text":"after","thoughtSignature":"opaque_text"}]`

func TestNativeGeminiRequestPartsRoundTrip(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]},{"role":"model","parts":` + signedNativeParts + `}]}`
	req, err := NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Path: "/v1beta/models/synthetic:generateContent"})
	require.NoError(t, err)
	require.Equal(t, "FIRSTSECOND", *req.Messages[1].ReasoningContent)
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var restored llm.Request
	require.NoError(t, json.Unmarshal(data, &restored))
	out, err := NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	wire, err := out.TransformRequest(t.Context(), &restored)
	require.NoError(t, err)
	require.JSONEq(t, signedNativeParts, gjson.GetBytes(wire.Body, "contents.1.parts").Raw)
	// Existing middleware edits stay authoritative; metadata contains no old text.
	restored.Messages[1].Content.MultipleContent[1].Text = new(string)
	*restored.Messages[1].Content.MultipleContent[1].Text = "updated"
	wire, err = out.TransformRequest(t.Context(), &restored)
	require.NoError(t, err)
	require.Equal(t, "updated", gjson.GetBytes(wire.Body, "contents.1.parts.3.text").String())
}

func TestNativeGeminiResponsePartsRoundTrip(t *testing.T) {
	testNativeGeminiResponsePartsRoundTrip(t, signedNativeParts)
}

func TestNativeGeminiUnsignedMixedPartsRoundTrip(t *testing.T) {
	const parts = `[{"text":"before"},{"functionCall":{"id":"call_1","name":"lookup","args":{}}},{"text":"after"}]`
	testNativeGeminiResponsePartsRoundTrip(t, parts)
}

func TestNativeGeminiFilePartsRoundTrip(t *testing.T) {
	testNativeGeminiResponsePartsRoundTrip(t, signedNativeFileParts)
	t.Run("request", func(t *testing.T) {
		body := `{"contents":[{"role":"model","parts":` + signedNativeFileParts + `}]}`
		req, err := NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(body), Path: "/v1beta/models/synthetic:generateContent"})
		require.NoError(t, err)
		out, err := NewOutboundTransformer("https://example.invalid", "synthetic")
		require.NoError(t, err)
		wire, err := out.TransformRequest(t.Context(), req)
		require.NoError(t, err)
		require.JSONEq(t, signedNativeFileParts, gjson.GetBytes(wire.Body, "contents.0.parts").Raw)
	})
}

func testNativeGeminiResponsePartsRoundTrip(t *testing.T, nativeParts string) {
	t.Helper()
	out, err := NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	body := `{"responseId":"r1","modelVersion":"synthetic","candidates":[{"index":0,"content":{"role":"model","parts":` + nativeParts + `},"finishReason":"STOP"}]}`
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "stream"}[streaming], func(t *testing.T) {
			var wireBody []byte
			if streaming {
				unified, err := out.TransformStream(t.Context(), nil, streams.SliceStream([]*httpclient.StreamEvent{{Data: []byte(body)}}))
				require.NoError(t, err)
				wire, err := NewInboundTransformer().TransformStream(t.Context(), unified)
				require.NoError(t, err)
				defer wire.Close()
				chunks, err := streams.All(wire)
				require.NoError(t, err)
				require.NotEmpty(t, chunks)
				wireBody = chunks[0].Data
			} else {
				unified, err := out.TransformResponse(t.Context(), &httpclient.Response{Body: []byte(body)})
				require.NoError(t, err)
				data, err := json.Marshal(unified)
				require.NoError(t, err)
				var restored llm.Response
				require.NoError(t, json.Unmarshal(data, &restored))
				wire, err := NewInboundTransformer().TransformResponse(t.Context(), &restored)
				require.NoError(t, err)
				wireBody = wire.Body
				// A persisted response can be used as model history without moving
				// or dropping a signed part on the outbound request path.
				replay, err := out.TransformRequest(t.Context(), &llm.Request{
					Model: "synthetic", Messages: []llm.Message{*restored.Choices[0].Message},
				})
				require.NoError(t, err)
				require.JSONEq(t, nativeParts, gjson.GetBytes(replay.Body, "contents.0.parts").Raw)
			}
			require.JSONEq(t, nativeParts, gjson.GetBytes(wireBody, "candidates.0.content.parts").Raw)
		})
	}
}

func TestNativeGeminiAggregatePartOrder(t *testing.T) {
	first := `{"responseId":"r1","candidates":[{"index":0,"content":{"role":"model","parts":` + signedNativeParts + `}}]}`
	last := `{"responseId":"r1","candidates":[{"index":0,"content":{"role":"model","parts":[{"thoughtSignature":"opaque_final"}]},"finishReason":"STOP"}]}`
	body, meta, err := AggregateStreamChunks(t.Context(), []*httpclient.StreamEvent{{Data: []byte(first)}, {Data: []byte(last)}})
	require.NoError(t, err)
	require.True(t, meta.Completed)
	var parts []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(body, "candidates.0.content.parts").Raw), &parts))
	require.Len(t, parts, 8)
	firstParts, err := json.Marshal(parts[:7])
	require.NoError(t, err)
	require.JSONEq(t, signedNativeParts, string(firstParts))
	require.JSONEq(t, `{"thoughtSignature":"opaque_final"}`, string(parts[7]))
}

func TestNativeGeminiStandaloneSignatureStream(t *testing.T) {
	out, err := NewOutboundTransformer("https://example.invalid", "synthetic")
	require.NoError(t, err)
	events := []*httpclient.StreamEvent{
		{Data: []byte(`{"responseId":"r1","candidates":[{"content":{"role":"model","parts":[{"text":"answer"}]}}]}`)},
		{Data: []byte(`{"responseId":"r1","candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"opaque_final"}]}}]}`)},
		{Data: []byte(`{"responseId":"r1","candidates":[{"finishReason":"STOP"}]}`)},
	}
	unified, err := out.TransformStream(t.Context(), nil, streams.SliceStream(events))
	require.NoError(t, err)
	wire, err := NewInboundTransformer().TransformStream(t.Context(), unified)
	require.NoError(t, err)
	defer wire.Close()
	chunks, err := streams.All(wire)
	require.NoError(t, err)
	require.Len(t, chunks, 3)
	require.JSONEq(t, `[{"text":"answer"}]`, gjson.GetBytes(chunks[0].Data, "candidates.0.content.parts").Raw)
	require.JSONEq(t, `[{"thoughtSignature":"opaque_final"}]`, gjson.GetBytes(chunks[1].Data, "candidates.0.content.parts").Raw)
	require.Equal(t, "STOP", gjson.GetBytes(chunks[2].Data, "candidates.0.finishReason").String())
}
