package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/gql"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
)

// Exercise the public catalog and the management queries used by CodeGateway
// against the same request, recorded by a real generation through a local upstream.
func TestCodeGatewayReadOnlyIntegration(t *testing.T) {
	h := newCodexRouteHarness(t)
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"modelVersion":"model-b","usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	}))
	defer upstream.Close()

	source := h.db.Channel.Create().SetType(channel.TypeGemini).SetName("integration channel").
		SetBaseURL(upstream.URL).SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).
		SetDefaultTestModel("model-b").SetSupportedModels([]string{"publisher/model-a", "model-b"}).SaveX(h.ctx)
	ch, err := h.channels.GetChannel(h.ctx, source.ID)
	require.NoError(t, err)
	h.channels.SetEnabledChannelsForTest([]*biz.Channel{ch})
	h.registerModels(t, ch, "chat")
	require.NoError(t, h.system.SetSecretKey(h.ctx, "integration-test-secret"))
	models := h.openAI.ModelService
	gemini := api.NewGeminiHandlers(api.GeminiHandlersParams{
		ModelService: models, ChannelService: h.channels, SystemService: h.system, RequestService: h.requests,
		DefaultSelector: orchestrator.NewDefaultSelector(h.channels, models, h.system), UsageLogService: h.usage,
		PromptService: h.prompts, PromptProtectionRuleService: h.protect, HttpClient: httpclient.NewHttpClient(),
		LiveStreamRegistry: biz.NewLiveStreamRegistry(), ChannelLimiterManager: orchestrator.NewChannelLimiterManager(),
	})
	auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
	server := &Server{Engine: gin.New(), Config: Config{RequestTimeout: 5 * time.Second, LLMRequestTimeout: 5 * time.Second}}
	ip, err := middleware.NewIPAccessControlConfig(false, nil, "")
	require.NoError(t, err)
	SetupRoutes(server, Handlers{
		OpenAI: h.openAI, Gemini: gemini, CodexCompatibility: api.NewCodexCompatibilityHandlers(h.system),
		Graphql: gql.NewGraphqlHandlers(gql.Dependencies{Ent: h.db, ChannelService: h.channels, RequestService: h.requests}),
	}, h.db, Services{SystemService: h.system, AuthService: auth, RequestService: h.requests}, ip)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	makeKey := func(name string, permissions ...string) string {
		return h.db.APIKey.Create().SetProjectID(h.key.ProjectID).SetUserID(h.key.UserID).
			SetType(apikey.TypeServiceAccount).SetKey("ah-" + name).SetName(name).SetScopes(permissions).
			SetProfiles(&objects.APIKeyProfiles{ActiveProfile: "limited", Profiles: []objects.APIKeyProfile{{Name: "limited", ModelIDs: []string{"model-b"}}}}).SaveX(h.ctx).Key
	}
	reader := makeKey("requests", "read_requests")
	operator := makeKey("operations", "read_requests", "read_channels")
	catalog := makeKey("catalog", "read_channels")
	writer := makeKey("inference", "write_requests", "read_channels")

	call := func(method, path, key, body string, status int, headers ...http.Header) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, httpServer.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		if len(headers) > 0 {
			req.Header = headers[0]
		}
		if strings.HasPrefix(path, "/v1beta/") || strings.HasPrefix(path, "/gemini/") {
			req.Header.Set("X-Goog-Api-Key", key)
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpServer.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, status, resp.StatusCode, "%s %s: %s", method, path, data)
		return string(data)
	}
	for _, prefix := range []string{"/v1beta", "/gemini/v1beta"} {
		redirect := httptest.NewRecorder()
		server.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, prefix+"/models/?pageSize=2", nil))
		require.Equal(t, http.StatusMovedPermanently, redirect.Code)
		require.Equal(t, prefix+"/models?pageSize=2", redirect.Header().Get("Location"))

		for key, count := range map[string]int{h.key.Key: 2, operator: 1} {
			list := gjson.Get(call("GET", prefix+"/models", key, "", 200), "models").Array()
			require.Len(t, list, count)
			for _, item := range list {
				detail := call("GET", prefix+"/"+item.Get("name").String(), key, "", 200)
				require.JSONEq(t, item.Raw, detail)
			}
		}
		hidden := call("GET", prefix+"/models/publisher/model-a", operator, "", 404)
		require.Equal(t, "NOT_FOUND", gjson.Get(hidden, "error.status").String())
		require.JSONEq(t, hidden, call("GET", prefix+"/models/missing", operator, "", 404))
	}
	call("GET", "/v1beta/models/model-b", reader, "", 403)
	body := `{"contents":[{"parts":[{"text":"ping"}]}]}`
	call("POST", "/v1beta/models/model-b:generateContent", reader, body, 403)
	call("POST", "/v1/chat/completions", operator, `{"model":"model-b","messages":[{"role":"user","content":"ping"}]}`, 403)
	call("GET", "/v1/responses", reader, "", 403)
	require.Zero(t, upstreamCalls.Load())
	require.Zero(t, h.db.Request.Query().CountX(h.ctx))
	result := call("POST", "/v1beta/models/model-b:generateContent", writer, body, 200)
	require.Equal(t, "ok", gjson.Get(result, "candidates.0.content.parts.0.text").String())
	require.EqualValues(t, 1, upstreamCalls.Load())

	own := h.db.Request.Query().OnlyX(h.ctx)
	attempt := h.db.RequestExecution.Query().OnlyX(h.ctx)
	otherProject := h.db.Project.Create().SetName("second project").SaveX(h.ctx)
	other := h.db.Request.Create().SetProjectID(otherProject.ID).SetModelID("other").SetStatus("completed").SetRequestBody([]byte("{}")).SaveX(h.ctx)
	otherAttempt := h.db.RequestExecution.Create().SetProjectID(otherProject.ID).SetRequestID(other.ID).
		SetModelID("other").SetStatus("completed").SetRequestBody([]byte("{}")).SaveX(h.ctx)

	query := func(key, operation string) string {
		t.Helper()
		data, err := json.Marshal(map[string]string{"query": operation})
		require.NoError(t, err)
		return call("POST", "/admin/graphql", key, string(data), 200)
	}
	const fields = "id createdAt modelID status format stream externalID clientStatusCode metricsLatencyMs metricsFirstTokenLatencyMs"
	const channelFields = " channel { id name }"
	const page = "totalCount pageInfo { endCursor hasNextPage }"
	const listQuery = "{ requests(first:20, orderBy:{field:CREATED_AT,direction:DESC}) { " + page + " edges { node { " + fields + "%s } } } }"
	checkCompleted := func(record gjson.Result, id string, latencyMs *int64) {
		t.Helper()
		require.Equal(t, id, record.Get("id").String())
		require.Equal(t, "completed", record.Get("status").String())
		require.NotNil(t, latencyMs)
		latency := record.Get("metricsLatencyMs")
		require.Equal(t, gjson.Number, latency.Type)
		require.Equal(t, *latencyMs, latency.Int())
	}
	for _, key := range []string{reader, operator} {
		channelSelection := ""
		if key == operator {
			channelSelection = channelFields
		}
		list := query(key, fmt.Sprintf(listQuery, channelSelection))
		require.False(t, gjson.Get(list, "errors").Exists(), list)
		require.EqualValues(t, 1, gjson.Get(list, "data.requests.totalCount").Int())
		id := fmt.Sprintf("gid://axonhub/Request/%d", own.ID)
		checkCompleted(gjson.Get(list, "data.requests.edges.0.node"), id, own.MetricsLatencyMs)
		detail := query(key, fmt.Sprintf(`{ node(id:%q) { ... on Request { %s executions(first:20,orderBy:{field:CREATED_AT,direction:DESC}) { %s edges { node { id createdAt modelID status format responseStatusCode metricsLatencyMs metricsFirstTokenLatencyMs %s } } } } } }`, id, fields+channelSelection, page, channelSelection))
		require.False(t, gjson.Get(detail, "errors").Exists(), detail)
		checkCompleted(gjson.Get(detail, "data.node"), id, own.MetricsLatencyMs)
		require.EqualValues(t, 1, gjson.Get(detail, "data.node.executions.totalCount").Int())
		entry := gjson.Get(detail, "data.node.executions.edges.0.node")
		checkCompleted(entry, fmt.Sprintf("gid://axonhub/RequestExecution/%d", attempt.ID), attempt.MetricsLatencyMs)
		if key == operator {
			require.Equal(t, source.Name, entry.Get("channel.name").String())
		}
	}
	empty := query(reader, `{ requests(first:20, where:{modelID:"other"}) { totalCount edges { node { id } } } }`)
	require.JSONEq(t, `{"data":{"requests":{"totalCount":0,"edges":[]}}}`, empty)
	for _, id := range []string{fmt.Sprintf("gid://axonhub/Request/%d", other.ID), fmt.Sprintf("gid://axonhub/RequestExecution/%d", otherAttempt.ID)} {
		result := query(reader, fmt.Sprintf(`{ node(id:%q) { id } }`, id))
		require.False(t, gjson.Get(result, "data.node.id").Exists(), result)
	}
	require.Equal(t, "FORBIDDEN", gjson.Get(query(reader, fmt.Sprintf(listQuery, channelFields)), "errors.0.extensions.code").String())
	require.Equal(t, "FORBIDDEN", gjson.Get(query(catalog, fmt.Sprintf(listQuery, "")), "errors.0.extensions.code").String())
	call("POST", "/admin/graphql", reader, `{"query":"{ requests(first:1) { totalCount } }"}`, 403, http.Header{"X-Project-Id": {fmt.Sprintf("gid://axonhub/Project/%d", otherProject.ID)}})
	mutation := query(reader, fmt.Sprintf(`mutation { deleteChannel(id:"gid://axonhub/Channel/%d") }`, source.ID))
	require.Contains(t, gjson.Get(mutation, "errors.0.message").String(), "read-only")
	require.Equal(t, source.Name, h.db.Channel.GetX(h.ctx, source.ID).Name)
}
