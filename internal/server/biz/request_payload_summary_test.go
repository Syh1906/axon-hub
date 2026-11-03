package biz

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
)

func BenchmarkRequestImageSummaryLargeBody(b *testing.B) {
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", 50<<20) + `"}]}]}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		summary := summarizeRequestImages(body)
		if summary == nil || summary.InlineEncodedBytes != 50<<20 {
			b.Fatal("图片摘要不完整")
		}
	}
}

func TestRequestImageSummaryProtocolParts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       *requestImageSummary
	}{
		{"responses", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"},{"type":"input_image","image_url":"https://private/image"},{"type":"input_image","file_id":"private-file"}]}]}`, &requestImageSummary{Count: 4, InlineCount: 2, InlineEncodedBytes: 8, LargestInlineEncodedBytes: 4}},
		{"chat", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAAAAAA"}},{"type":"image_url","image_url":{"url":"https://private/image"}}]}]}`, &requestImageSummary{Count: 2, InlineCount: 1, InlineEncodedBytes: 8, LargestInlineEncodedBytes: 8}},
		{"text and tool arguments", `{"input":[{"type":"function_call","arguments":"{\"type\":\"input_image\"}"},{"role":"user","content":[{"type":"input_text","text":"{\"type\":\"input_image\"}"}]}]}`, nil},
		{"plain", `{"input":"hello"}`, nil},
		{"invalid", `{"input":[`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, summarizeRequestImages([]byte(tc.body))) })
	}
}

func TestRequestImageSummarySurvivesMissingDebugBody(t *testing.T) {
	svc := NewRequestPayloadService(RequestPayloadConfig{Directory: t.TempDir()})
	ctx := context.Background()
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`)
	marker := svc.saveRequestDebug(ctx, body)
	ref, ok := parsePayloadMarker(marker)
	require.True(t, ok)
	require.Equal(t, &requestImageSummary{Count: 1, InlineCount: 1, InlineEncodedBytes: 4, LargestInlineEncodedBytes: 4}, ref.Images)
	loaded, ok := svc.loadDebug(ctx, marker)
	require.True(t, ok)
	require.Equal(t, string(body), string(loaded), "仍可用时返回原始正文，不修改调用内容")
	for _, state := range []string{"expired", "evicted"} {
		t.Run(state, func(t *testing.T) {
			missing := ref
			if state == "expired" {
				missing.ExpiresAt = time.Now().Add(-time.Second)
			} else {
				missing.Key = strings.Repeat("0", 64)
			}
			loaded, ok := svc.loadDebug(ctx, payloadMarker(missing))
			require.True(t, ok)
			got, ok := parsePayloadMarker(loaded)
			require.True(t, ok)
			require.Equal(t, state, got.State)
			require.Equal(t, ref.Images, got.Images)
			require.Empty(t, got.Key)
		})
	}
	large := []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", debugPayloadMaxBytes) + `"}]}]}`)
	ref, ok = parsePayloadMarker(svc.saveRequestDebug(ctx, large))
	require.True(t, ok)
	require.Equal(t, "too_large", ref.State)
	require.Equal(t, debugPayloadMaxBytes, ref.Images.InlineEncodedBytes)
	require.Less(t, len(payloadMarker(ref)), 1024)
	require.Empty(t, ref.Key)
}

func TestRequestImageSummaryUsesEachSavedBodyAndStorageSwitch(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:image_summary?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newResponsesSessionRequestService(t, client)
	ch := &Channel{Channel: &ent.Channel{ID: 1, Settings: &objects.ChannelSettings{}}}
	input := []byte(`{"model":"example","input":[{"role":"user","content":[{"type":"input_image","file_id":"private"}]}]}`)
	outbound := []byte(`{"model":"example","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAAAAAA"}}]}]}`)
	for _, enabled := range []bool{true, false} {
		require.NoError(t, svc.SystemService.SetStoragePolicy(ctx, &StoragePolicy{StoreRequestBody: enabled}))
		req, err := svc.CreateRequest(ctx, &llm.Request{Model: "example"}, &httpclient.Request{Body: input}, llm.APIFormatOpenAIResponse)
		require.NoError(t, err)
		execution, err := svc.CreateRequestExecution(ctx, ch, "example", req, httpclient.Request{JSONBody: outbound}, llm.APIFormatOpenAIChatCompletion, false, nil, nil, nil)
		require.NoError(t, err)
		clientRef, ok := parsePayloadMarker(req.RequestBody)
		require.True(t, ok)
		channelRef, ok := parsePayloadMarker(execution.RequestBody)
		require.True(t, ok)
		if enabled {
			require.Equal(t, len(input), clientRef.Size)
			require.Equal(t, 1, clientRef.Images.Count)
			require.Equal(t, len(outbound), channelRef.Size)
			require.Equal(t, 2, channelRef.Images.Count)
			require.Equal(t, 12, channelRef.Images.InlineEncodedBytes)
		} else {
			require.Equal(t, "disabled", clientRef.State)
			require.Equal(t, "disabled", channelRef.State)
			require.Nil(t, clientRef.Images)
			require.Nil(t, channelRef.Images)
		}
		for _, body := range [][]byte{req.RequestBody, execution.RequestBody} {
			require.NotContains(t, string(body), "private")
			require.NotContains(t, string(body), "AAAA")
		}
	}
}
