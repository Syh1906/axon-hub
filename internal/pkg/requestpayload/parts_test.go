package requestpayload

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInspectProtocolToolImagesAndOpaqueContent(t *testing.T) {
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]},{"type":"function_call_output","output":[{"type":"input_image","image_url":"data:image/png;base64,BBBBBBBB"},{"type":"input_text","text":"tool text"}]},{"type":"custom_tool_call_output","output":[{"type":"input_image","file_id":"private"}]},{"type":"reasoning","encrypted_content":"opaque"},{"type":"function_call","arguments":"{\"type\":\"input_image\"}"}]}`)
	images, content := Inspect(body)
	require.Equal(t, &ImageSummary{Count: 3, InlineCount: 2, InlineEncodedBytes: 12, LargestInlineEncodedBytes: 8, ToolCount: 2}, images)
	require.Greater(t, content.ToolTextBytes, 0)
	require.Equal(t, len(`"opaque"`), content.EncryptedBytes)
	require.Equal(t, len(body), content.ImageBytes+content.TextBytes+content.ToolTextBytes+content.FileBytes+content.EncryptedBytes+content.OtherBytes)
	for _, body := range []string{
		`{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}]}`,
		`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`,
	} {
		images, content := Inspect([]byte(body))
		require.Equal(t, 1, images.InlineCount)
		require.Equal(t, 4, images.InlineEncodedBytes)
		require.Positive(t, content.OtherBytes)
	}
	images, content = Inspect([]byte(`{"input":[{"type":"image_generation_call","result":"AAAA"}]}`))
	require.Equal(t, 4, images.InlineEncodedBytes)
	require.Equal(t, 1, images.ToolCount)
	require.Greater(t, content.ImageBytes, 4)
}

func TestInspectGeminiSystemTextAndEnvelope(t *testing.T) {
	for _, field := range []string{"systemInstruction", "system_instruction"} {
		payload := `{"` + field + `":{"parts":[{"text":"system text"}]},"contents":[{"role":"user","parts":[{"text":"user text"}]}]}`
		for _, body := range []string{payload, `{"project":"p","model":"m","requestType":"agent","userAgent":"antigravity","request":` + payload + `}`} {
			_, content := Inspect([]byte(body))
			require.Equal(t, len(`"system text"`)+len(`"user text"`), content.TextBytes)
			require.Equal(t, len(body), content.TextBytes+content.OtherBytes)
		}
	}
}
