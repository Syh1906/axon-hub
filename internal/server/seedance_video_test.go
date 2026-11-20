package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

func TestSeedanceStandardVideoTasks(t *testing.T) {
	var creates, createStatus atomic.Int32
	var state atomic.Value
	state.Store("queued")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			n := creates.Add(1)
			if code := createStatus.Load(); code != 0 {
				if code == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/redirected-create")
				}
				w.WriteHeader(int(code))
				fmt.Fprint(w, `{"error":{"code":"fixture","message":"fixture response"}}`)
				return
			}
			fmt.Fprintf(w, `{"id":"task-%d"}`, n)
		case http.MethodGet:
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			switch status := state.Load().(string); status {
			case "deleted":
				w.WriteHeader(http.StatusNotFound)
			case "succeeded":
				fmt.Fprintf(w, `{"id":%q,"status":"succeeded","content":{"video_url":"https://cdn.example.invalid/result.mp4"},"usage":{"completion_tokens":12345,"total_tokens":12345}}`, id)
			case "failed":
				fmt.Fprintf(w, `{"id":%q,"status":"failed","error":{"code":"InvalidInput","message":"Invalid video input","debug":"private"}}`, id)
			default:
				fmt.Fprintf(w, `{"id":%q,"status":%q}`, id, status)
			}
		case http.MethodDelete:
			switch state.Load().(string) {
			case "running":
				w.WriteHeader(http.StatusConflict)
				return
			case "queued":
				state.Store("cancelled")
			default:
				state.Store("deleted")
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(upstream.Close)
	h := newCodexRouteHarness(t)
	var channels []*biz.Channel
	var associations []*objects.ModelAssociation
	const model = "doubao-seedance-2-0-260128"
	for _, name := range []string{"a", "b"} {
		row := h.db.Channel.Create().SetType(channel.TypeDoubao).SetName(name).SetBaseURL(upstream.URL + "/" + name).
			SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetDefaultTestModel(model).SetSupportedModels([]string{model}).SaveX(h.ctx)
		ch, err := h.channels.GetChannel(h.ctx, row.ID)
		require.NoError(t, err)
		channels = append(channels, ch)
		associations = append(associations, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: row.ID, ModelID: model}})
	}
	h.channels.SetEnabledChannelsForTest(channels)
	h.registerModels(t, channels[0], "video_generation")
	h.db.Model.Query().OnlyX(h.ctx).Update().SetSettings(&objects.ModelSettings{Associations: associations}).SaveX(h.ctx)
	require.NoError(t, h.system.SetRetryPolicy(h.ctx, &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 1, MaxSingleChannelRetries: 1}))
	in := openai.NewVideoInboundTransformer()
	h.openAI.VideoService = biz.NewVideoService(h.channels, h.requests)
	h.openAI.VideoInboundTransformer = in
	h.openAI.VideoHandlers = api.NewChatCompletionHandlers(orchestrator.NewChatCompletionOrchestrator(h.channels, orchestrator.NewDefaultSelector(h.channels, h.openAI.ModelService, h.system), h.requests, httpclient.NewHttpClient(), in, h.system, h.usage, h.prompts, nil, h.protect, biz.NewLiveStreamRegistry(), orchestrator.NewChannelLimiterManager(), nil, orchestrator.PluginIntegration{}))
	auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
	v1 := h.router.Group("/v1", middleware.WithAPIKeyConfig(auth, nil))
	v1.POST("/videos", h.openAI.CreateVideo)
	v1.GET("/videos/:id", h.openAI.GetVideo)
	v1.DELETE("/videos/:id", h.openAI.DeleteVideo)
	call := func(method, path string, want int) gjson.Result {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/videos"+path, strings.NewReader(`{"model":"doubao-seedance-2-0-260128","prompt":"test"}`))
		r.Header.Set("Authorization", "Bearer "+h.key.Key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.router.ServeHTTP(w, r)
		require.Equal(t, want, w.Code, w.Body.String())
		return gjson.Parse(w.Body.String())
	}
	create := func() string { return "/" + call("POST", "", 200).Get("id").String() }
	saved := func(path string) request.Status {
		return h.db.Request.Query().Where(request.ExternalID(strings.TrimPrefix(path, "/"))).OnlyX(h.ctx).Status
	}
	path := create()
	state.Store("running")
	call("DELETE", path, 409)
	require.Equal(t, request.StatusProcessing, saved(path))
	state.Store("queued")
	call("DELETE", path, 204)
	require.Equal(t, request.StatusCanceled, saved(path))
	require.Equal(t, "cancelled", call("GET", path, 200).Get("status").String())
	path = create()
	state.Store("expired")
	require.Equal(t, "expired", call("GET", path, 200).Get("status").String())
	require.Equal(t, request.StatusFailed, saved(path))
	path = create()
	state.Store("failed")
	failure := call("GET", path, 200).Get("error")
	require.JSONEq(t, `{"code":"InvalidInput","message":"Invalid video input"}`, failure.Raw)
	policy := h.system.RetryPolicyOrDefault(h.ctx)
	policy.UpstreamErrorPolicy = biz.UpstreamErrorPolicy{Mode: biz.UpstreamErrorModeCustom, CustomMessage: "Contact support"}
	require.NoError(t, h.system.SetRetryPolicy(h.ctx, policy))
	require.Equal(t, "Contact support", call("GET", path, 200).Get("error.message").String())
	path = create()
	state.Store("succeeded")
	video := call("GET", path, 200)
	require.EqualValues(t, 12345, video.Get("usage.completion_tokens").Int())
	require.Equal(t, "https://cdn.example.invalid/result.mp4", video.Get("video_url").String())
	call("DELETE", path, 204)
	require.Equal(t, request.StatusCompleted, saved(path))
	// Completion between a queued GET and the DELETE removes the upstream
	// record. A successful deletion alone must never certify cancellation.
	state.Store("queued")
	path = create()
	require.Equal(t, "queued", call("GET", path, 200).Get("status").String())
	state.Store("succeeded")
	call("DELETE", path, 204)
	require.Equal(t, request.StatusProcessing, saved(path))
	before := creates.Load()
	createStatus.Store(503)
	require.Equal(t, "video_create_outcome_unknown", call("POST", "", 502).Get("error.code").String())
	require.Equal(t, before+1, creates.Load())
	createStatus.Store(422)
	call("POST", "", 422)
	require.Equal(t, before+2, creates.Load())
	for _, code := range []int32{http.StatusOK, http.StatusTemporaryRedirect} {
		before = creates.Load()
		createStatus.Store(code)
		require.Equal(t, "video_create_outcome_unknown", call("POST", "", 502).Get("error.code").String())
		require.Equal(t, before+1, creates.Load())
	}
}
