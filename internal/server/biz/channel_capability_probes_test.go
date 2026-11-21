package biz

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDraftCapabilityProbeRequiresFunctionalEvidence(t *testing.T) {
	svc, db := setupTestChannelService(t)
	defer db.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), db))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer probe-key" {
			http.Error(w, "auth required", 401)
			return
		}
		if websocket.IsWebSocketUpgrade(r) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			require.NoError(t, err)
			defer conn.Close()
			_, body, err := conn.ReadMessage()
			require.NoError(t, err)
			require.Equal(t, "response.create", gjson.GetBytes(body, "type").String())
			require.GreaterOrEqual(t, gjson.GetBytes(body, "max_output_tokens").Int(), int64(16))
			require.Equal(t, "/v1/custom", r.URL.Path)
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_test","object":"response","model":"fixture","status":"completed","output":[{"id":"msg_test","role":"assistant","type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`)))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/alpha/search"):
			t.Error("dedicated search endpoint URL was ignored")
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, "/custom"):
			if !gjson.GetBytes(body, "tools.0").Exists() {
				require.GreaterOrEqual(t, gjson.GetBytes(body, "max_output_tokens").Int(), int64(16))
			}
			if gjson.GetBytes(body, "tools.0.type").Str == "web_search" {
				w.WriteHeader(429)
				return
			}
			fmt.Fprint(w, `{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":"model_not_found"}}`)
		}
	}))
	defer upstream.Close()
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/alpha/search", r.URL.Path)
		id := r.Header.Get("Session_id")
		require.NotEmpty(t, id)
		body, _ := io.ReadAll(r.Body)
		require.Equal(t, id, gjson.GetBytes(body, "id").Str)
		require.Equal(t, id, r.Header.Get("Session-Id"))
		require.NotEmpty(t, r.Header.Get("Originator"))
		require.NotEmpty(t, gjson.GetBytes(body, "commands.search_query.0.q").Str)
		fmt.Fprint(w, `{"results":[{"title":"at endpoint override"}]}`)
	}))
	defer override.Close()
	before := db.Channel.Query().CountX(ctx)
	inactive := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("disabled upstream received a probe") }))
	defer inactive.Close()
	input := DetectChannelEndpointsInput{Model: lo.ToPtr("fixture"), Draft: &ChannelProbeDraftInput{
		Type: channel.TypeOpenai, BaseURL: inactive.URL, Credentials: objects.ChannelCredentials{APIKey: "probe-key"},
		Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/responses", Path: "/custom"}, {APIFormat: "openai/alpha_search", BaseURL: override.URL}},
		UpstreamAddresses: []objects.ChannelUpstreamAddress{
			{ID: "main", Name: "main", URL: inactive.URL, Priority: 1, Enabled: false},
			{ID: "backup", Name: "backup", URL: upstream.URL + "/v1", Priority: 2, Enabled: true},
		},
	}}
	_, err := svc.DetectChannelEndpoints(t.Context(), input)
	require.Error(t, err)
	payload, err := svc.DetectChannelEndpoints(ctx, input)
	require.NoError(t, err)
	require.Equal(t, before, db.Channel.Query().CountX(ctx))
	rows := lo.KeyBy(payload.Endpoints, func(r DetectedChannelEndpoint) string { return r.APIFormat })
	require.True(t, rows["openai/alpha_search"].Supported)
	require.True(t, rows["openai/responses-ws"].Supported, "%+v", rows["openai/responses-ws"])
	require.False(t, rows[probeImageTool].Supported)
	require.Equal(t, endpointDetectReasonInvalidResponse, rows[probeImageTool].Reason)
	require.Equal(t, endpointDetectReasonRateLimited, rows[probeWebSearch].Reason)
	require.Equal(t, "model_unavailable", rows["openai/image_generation"].Reason)
}

func TestResponsesProbeAllowsNullErrorAndChecksImageEvidence(t *testing.T) {
	for _, tc := range []struct {
		format, body string
		supported    bool
	}{
		{"openai/responses", `{"object":"response","status":"completed","error":null,"output":[{"type":"message"}]}`, true},
		{probeWebSearch, `{"object":"response","status":"completed","error":null,"output":[{"type":"web_search_call","status":"completed","action":{"type":"search"}}]}`, true},
		{probeImageTool, `{"object":"response","status":"completed","error":null,"output":[{"type":"image_generation_call","status":"completed","result":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYPjPAAACAwEAKOj8bQAAAABJRU5ErkJggg=="}]}`, true},
		{probeImageTool, `{"object":"response","status":"completed","output":[{"type":"image_generation_call","status":"completed","result":"iVBORw0KGgoAAAAN"}]}`, false},
		{"openai/responses", `{"object":"response","status":"completed","error":{"code":"failed"},"output":[]}`, false},
	} {
		require.Equal(t, tc.supported, validCapabilityResponse(tc.format, &httpclient.Response{Body: []byte(tc.body)}), tc.format)
	}
}

func TestImageProbeUploadsReadablePNG(t *testing.T) {
	for _, format := range []string{"openai/image_edit", "openai/image_variation"} {
		req := buildCapabilityProbe(format, "fixture")
		require.Len(t, req.Image.Images, 1)
		_, err := png.Decode(bytes.NewReader(req.Image.Images[0]))
		require.NoError(t, err)
	}
}

func TestConfiguredNativeProbeFormats(t *testing.T) {
	for _, tc := range []struct {
		typ      channel.Type
		format   string
		response string
	}{
		{channel.TypeOpenai, "openai/video", `{"object":"video","id":"video_1","status":"queued"}`},
		{channel.TypeDeepseek, "openai/completions", `{"object":"text_completion","choices":[{"text":"OK"}]}`},
		{channel.TypeOllama, "ollama/chat", `{"model":"local","done":true,"message":{"role":"assistant","content":"OK"}}`},
		{channel.TypeDoubao, "seedance/video", `{"id":"cgt-test"}`},
	} {
		require.Contains(t, endpointProbeFormats(tc.typ), tc.format)
		require.True(t, validCapabilityResponse(tc.format, &httpclient.Response{Body: []byte(tc.response)}), tc.format)
		require.False(t, validCapabilityResponse(tc.format, &httpclient.Response{Body: []byte(`{"object":"response","output":[]}`)}), tc.format)
	}
	require.Contains(t, endpointProbeFormats(channel.TypeModelscope), "modelscope/image_generation")
}
