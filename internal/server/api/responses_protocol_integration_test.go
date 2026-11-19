package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestResponsesProtocolIntegrationHTTPUsesProductionHandlerAndUpstream(t *testing.T) {
	upstreamRequests := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := readRequestBody(r)
		require.NoError(t, err)
		upstreamRequests <- body
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(`{"id":"provider-response-1","object":"response","status":"completed","output":[{"type":"message","id":"provider-item-1","content":[{"type":"output_text","text":"synthetic-answer"}]}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)

	client, handlers, project := newResponsesIntegrationFixture(t, upstream.URL)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", stringsReader(`{"model":"synthetic-model","input":"synthetic-input"}`))
	requestContext := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	requestContext = contexts.WithAPIKey(requestContext, client.APIKey.Query().OnlyX(requestContext))
	request = request.WithContext(contexts.WithProjectID(requestContext, project.ID))
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/v1/responses", handlers.CreateResponse)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "provider-response-1", response["id"])
	require.Equal(t, "provider-item-1", response["output"].([]any)[0].(map[string]any)["id"])
	var upstreamBody map[string]any
	require.NoError(t, json.Unmarshal(<-upstreamRequests, &upstreamBody))
	require.Equal(t, "synthetic-model", upstreamBody["model"])
	require.Equal(t, "synthetic-input", upstreamBody["input"])
}

func TestFirstContentHTTPTrialLearnsOnlyFromDeliveredComparableSessions(t *testing.T) {
	var posts [2]atomic.Int32
	var reportCache atomic.Bool
	reportCache.Store(true)
	makeEndpoint := func(index int) *httptest.Server {
		return responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := posts[index].Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			details := ""
			if reportCache.Load() {
				details = `,"input_tokens_details":{"cached_tokens":0}`
			}
			fmt.Fprint(w, "data: "+`{"type":"response.output_text.delta","item_id":"i","output_index":0,"content_index":0,"delta":"hi"}`+"\n\n")
			fmt.Fprintf(w, "data: "+`{"type":"response.completed","response":{"id":"r%d-%d","object":"response","model":"synthetic-model","service_tier":"default","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6%s}}}`+"\n\n", index, n, details)
		}))
	}
	a, b := makeEndpoint(0), makeEndpoint(1)
	db, h, project := newResponsesIntegrationFixture(t, a.URL, func(c *ent.ChannelCreate) {
		c.SetSettings(&objects.ChannelSettings{AddressFailover: true, UpstreamAddresses: []objects.ChannelUpstreamAddress{
			{ID: "a", URL: a.URL, Priority: 1, Enabled: true}, {ID: "b", URL: b.URL, Priority: 1, Enabled: true},
		}})
	})
	o := h.ResponseCompletionHandlers.ChatCompletionOrchestrator
	o.RequestService.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), FirstContentTrialPercent: 10})
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), db))
	require.NoError(t, o.SystemService.SetRetryPolicy(ctx, &biz.RetryPolicy{TraceStickyMode: biz.TraceStickyPreferPreviousChannel, LoadBalancerStrategy: biz.LoadBalancerStrategyAdaptive}))
	ctx = contexts.WithProjectID(contexts.WithAPIKey(ctx, db.APIKey.Query().OnlyX(ctx)), project.ID)
	var traces []*ent.Trace
	for i := 0; len(traces) < 3; i++ {
		id := fmt.Sprintf("fixture-session-%d", i)
		slot := xxhash.Sum64String(id)
		if slot%100 < 10 && slot/100%2 == 1 {
			traces = append(traces, db.Trace.Create().SetProjectID(project.ID).SetTraceID(id).SaveX(ctx))
		}
	}
	router := gin.New()
	router.POST("/v1/responses", h.CreateResponse)
	send := func(trace *ent.Trace, want int, afterTerminal ...func()) {
		t.Helper()
		before := [2]int32{posts[0].Load(), posts[1].Load()}
		body := `{"model":"synthetic-model","stream":true,"input":[{"role":"user","content":"complete history"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		req.Header.Set("Session-Id", traces[0].TraceID)
		req.Header.Set("Content-Type", "application/json")
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if trace != nil {
			requestCtx = contexts.WithTrace(requestCtx, trace)
		}
		recorder := httptest.NewRecorder()
		var writer http.ResponseWriter = recorder
		if len(afterTerminal) != 0 {
			writer = &firstContentFlushRecorder{ResponseRecorder: recorder, onTerminal: afterTerminal[0]}
		}
		router.ServeHTTP(writer, req.WithContext(requestCtx))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), "response.completed")
		require.Equal(t, before[want]+1+int32(len(afterTerminal)), posts[want].Load(), "unexpected primary, trace=%v", trace)
		require.Equal(t, before[1-want], posts[1-want].Load())
	}
	send(nil, 0) // a client-supplied session header alone cannot enter the trial
	send(traces[0], 1, func() {
		// Deliver the next full-history request as soon as the client sees
		// the terminal frame, before the first ServeHTTP can finalize.
		send(traces[0], 1)
	})
	reportCache.Store(false)
	send(traces[1], 1) // explicitly reported zero on earlier rounds allowed exploration
	send(traces[2], 0) // missing evidence on the completed round stops further exploration
	require.Equal(t, 5, db.RequestExecution.Query().CountX(ctx))
}

type firstContentFlushRecorder struct {
	*httptest.ResponseRecorder
	onTerminal func()
}

func (r *firstContentFlushRecorder) Flush() {
	r.ResponseRecorder.Flush()
	if r.onTerminal != nil && strings.Contains(r.Body.String(), "response.completed") {
		run := r.onTerminal
		r.onTerminal = nil
		run()
	}
}

func TestResponsesProtocolIntegrationIsolation(t *testing.T) {
	upstreamRequests := make(chan []byte, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := readRequestBody(r)
		require.NoError(t, err)
		upstreamRequests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, err = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"provider-ws-1\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"provider-ws-1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"provider-item-ws-1\"}]}}\n\n")
		require.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)
	client, handlers, project := newResponsesIntegrationFixture(t, upstream.URL)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		requestContext := authz.WithTestBypass(ent.NewContext(c.Request.Context(), client))
		requestContext = contexts.WithAPIKey(requestContext, client.APIKey.Query().OnlyX(requestContext))
		c.Request = c.Request.WithContext(contexts.WithProjectID(requestContext, project.ID))
		c.Next()
	})
	router.GET("/v1/responses", handlers.CreateResponseWebSocket(5*time.Second))
	server := responsesIntegrationServer(t, router)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/responses"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"tenant-a","model":"synthetic-model","input":"synthetic-input"}`)))
	_, created, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "provider-ws-1", gjson.GetBytes(created, "response.id").String())
	_, completed, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "provider-item-ws-1", gjson.GetBytes(completed, "response.output.0.id").String())
	require.Equal(t, "tenant-a", gjson.GetBytes(completed, "stream_id").String())
	require.NotEmpty(t, <-upstreamRequests)
	require.NoError(t, conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
}

func newResponsesIntegrationFixture(t *testing.T, upstreamURL string, configure ...func(*ent.ChannelCreate)) (*ent.Client, *OpenAIHandlers, *ent.Project) {
	t.Helper()
	client := enttest.NewEntClient(t, "sqlite3", "file:"+t.Name()+"?mode=memory&_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	project, err := client.Project.Create().SetName("responses-integration-project").SetDescription("synthetic").SetStatus(project.StatusActive).Save(ctx)
	require.NoError(t, err)
	owner := client.User.Create().SetEmail("fixture@example.invalid").SetPassword("test").SaveX(ctx)
	client.APIKey.Create().SetName("fixture").SetKey("fixture-key").SetProjectID(project.ID).SetUserID(owner.ID).SetType("user").SaveX(ctx)
	create := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("responses-integration-channel").SetStatus(channel.StatusEnabled).SetTags([]string{"synthetic"}).SetBaseURL(upstreamURL).SetCredentials(objects.ChannelCredentials{APIKey: "synthetic-key"}).SetSupportedModels([]string{"synthetic-model"}).SetDefaultTestModel("synthetic-model")
	for _, apply := range configure {
		apply(create)
	}
	fixtureChannel, err := create.Save(ctx)
	require.NoError(t, err)

	cacheConfig := xcache.Config{Mode: xcache.ModeMemory}
	systemService := biz.NewSystemService(biz.SystemServiceParams{CacheConfig: cacheConfig, Ent: client})
	client.Model.Create().SetDeveloper("test").SetName("synthetic-model").SetModelID("synthetic-model").SetIcon("test").SetGroup("test").SetStatus("enabled").SetModelCard(&objects.ModelCard{}).SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: fixtureChannel.ID, ModelID: "synthetic-model"}}}}).SaveX(ctx)
	channelService := biz.NewChannelService(biz.ChannelServiceParams{CacheConfig: cacheConfig, Ent: client, SystemService: systemService, HttpClient: httpclient.NewHttpClient()})
	usageLogService := biz.NewUsageLogService(client, systemService, channelService)
	storage := &biz.DataStorageService{AbstractService: &biz.AbstractService{}, SystemService: systemService, Cache: xcache.NewFromConfig[ent.DataStorage](cacheConfig)}
	requestService := biz.NewRequestService(client, cacheConfig, systemService, usageLogService, storage, biz.NewLiveStreamRegistry(), biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir()}))
	modelService := biz.NewModelService(biz.ModelServiceParams{ChannelService: channelService, SystemService: systemService, Ent: client})
	selector := orchestrator.NewDefaultSelector(channelService, modelService, systemService)
	candidates, err := selector.Select(ctx, &llm.Request{Model: "synthetic-model", RequestType: llm.RequestTypeChat})
	require.NoError(t, err)
	require.NotEmpty(t, candidates)
	protection := biz.NewPromptProtectionRuleService(biz.PromptProtectionRuleServiceParams{CacheConfig: cacheConfig, Ent: client})
	t.Cleanup(protection.Stop)
	params := OpenAIHandlersParams{
		ChannelService: channelService, ModelService: modelService, DefaultSelector: selector, RequestService: requestService,
		SystemService: systemService, UsageLogService: usageLogService, PromptService: biz.NewPromptService(biz.PromptServiceParams{Ent: client}),
		PromptProtectionRuleService: protection, QuotaService: biz.NewQuotaService(client, systemService), HttpClient: httpclient.NewHttpClient(),
		LiveStreamRegistry: biz.NewLiveStreamRegistry(), ChannelLimiterManager: orchestrator.NewChannelLimiterManager(), Client: client,
	}
	return client, NewOpenAIHandlers(params), project
}

func readRequestBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func stringsReader(value string) *strings.Reader { return strings.NewReader(value) }
