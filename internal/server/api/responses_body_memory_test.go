package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/gemini"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestAnonymousGatewayDoesNotNegotiateDictionaries(t *testing.T) {
	p := biz.NewRequestPayloadService(biz.RequestPayloadConfig{
		Directory: t.TempDir(), AcceptPeerDictionary: true,
		PeerDictionaryEndpoints: []string{"https://peer.example/v1/responses"},
	})
	ctx := contexts.WithAPIKey(t.Context(), &ent.APIKey{Type: apikey.TypeNoauth})
	ctx = shared.WithSessionScope(ctx, "api_key:anonymous")
	require.Nil(t, p.PeerRequest(ctx))
	engine := gin.New()
	engine.Use(middleware.WithInferenceBody(p))
	plain := strings.Repeat("x", 20<<10)
	engine.POST("/", func(c *gin.Context) {
		raw, err := httpclient.ReadHTTPRequest(c.Request)
		require.NoError(t, err)
		require.Equal(t, plain, string(raw.Body))
		require.Empty(t, raw.Headers.Get(httpclient.PeerOffer))
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(plain))
	req.Header.Set(httpclient.PeerOffer, strings.Repeat("a", 64))
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Header().Get(httpclient.PeerAck))
}

func TestBodyBudgetPreservesGeminiReadErrors(t *testing.T) {
	check := func(limit int, offer bool) *httptest.ResponseRecorder {
		p := biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), MaxInflightBodyMiB: limit, AcceptPeerDictionary: offer})
		r := gin.New()
		r.Use(middleware.WithInferenceBody(p))
		h := &GeminiHandlers{ChatCompletionHandlers: NewChatCompletionHandlers(&orchestrator.ChatCompletionOrchestrator{Inbound: gemini.NewInboundTransformer()})}
		r.POST("/v1beta/models/*action", h.GenerateContent)
		req := httptest.NewRequest(http.MethodPost, "/v1beta/models/m:generateContent", strings.NewReader("invalid gzip"))
		req.Header.Set("Content-Encoding", "gzip")
		if offer {
			req = req.WithContext(shared.WithSessionScope(req.Context(), "api_key:fixture"))
			req.Header.Set(httpclient.PeerOffer, strings.Repeat("a", 64))
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	original := check(0, false)
	require.NotEmpty(t, gjson.Get(original.Body.String(), "error.status").String())
	for _, offer := range []bool{false, true} {
		budgeted := check(2, offer)
		require.Equal(t, original.Code, budgeted.Code)
		require.JSONEq(t, original.Body.String(), budgeted.Body.String())
	}
}

func TestResponsesWebSocketReleasesLargeRequestBodies(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	stop := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 2 {
			close(entered)
			select {
			case <-r.Context().Done():
			case <-stop:
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"gc-turn\",\"status\":\"completed\",\"output\":[]}}\n\n")
	}))
	t.Cleanup(provider.Close)
	defer close(stop)
	db, h, project := newResponsesIntegrationFixture(t, provider.URL)
	orch := h.ResponseCompletionHandlers.ChatCompletionOrchestrator
	router := responsesFixtureRouter(db, project)
	router.Use(middleware.WithInferenceBody(biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), MaxInflightBodyMiB: 16})))
	type refs struct{ body, frame weak.Pointer[byte] }
	seen := make(chan refs, 2)
	finished := make(chan struct{})
	router.GET("/v1/responses", func(c *gin.Context) {
		defer close(finished)
		serveResponsesWebSocket(c, 10*time.Second, func(ctx context.Context, r *httpclient.Request) (orchestrator.ChatCompletionResult, error) {
			seen <- refs{weak.Make(&r.Body[0]), weak.Make(&r.JSONBody[0])}
			return orch.Process(ctx, r)
		}, nil)
	})
	gateway := responsesIntegrationServer(t, router)
	conn := responsesIntegrationDial(t, gateway)
	frame := `{"type":"response.create","model":"synthetic-model","input":"` + strings.Repeat("x", 2<<20) + `"}`
	terminal := responsesIntegrationTurn(t, conn, frame)
	require.Equal(t, "response.completed", gjson.GetBytes(terminal, "type").String())
	first := <-seen
	require.EventuallyWithT(t, func(check *assert.CollectT) {
		runtime.GC()
		assert.True(check, first.body.Value() == nil, "completed turn POST body still live on open socket")
		assert.True(check, first.frame.Value() == nil, "completed turn input frame still live on open socket")
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(frame)))
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("second turn did not reach upstream")
	}
	require.NoError(t, conn.Close())
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("handler failed to release active turn")
	}
	second := <-seen
	require.EventuallyWithT(t, func(check *assert.CollectT) {
		runtime.GC()
		assert.True(check, second.body.Value() == nil, "canceled turn POST body still live")
		assert.True(check, second.frame.Value() == nil, "canceled turn input frame still live")
	}, 5*time.Second, 20*time.Millisecond)
	runtime.KeepAlive(h)
}

func TestTwoGatewaysDictionaryKeepsSupplierBodyAndCount(t *testing.T) {
	delivered := make(chan string, 3)
	supplier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := httpclient.ReadHTTPRequest(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		for k := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-axonhub-dict-") {
				t.Errorf("peer header reached supplier: %s", k)
			}
		}
		delivered <- gjson.GetBytes(b.Body, "input.0.content.0.text").String()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"peer-fixture","object":"response","status":"completed","model":"synthetic-model","output":[{"type":"message","id":"msg","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9}}`)
	}))
	t.Cleanup(supplier.Close)
	db, h, project := newResponsesIntegrationFixture(t, supplier.URL)
	p := biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), AcceptPeerDictionary: true, MaxInflightBodyMiB: 8})
	h.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.PayloadService = p
	router := responsesFixtureRouter(db, project)
	type wire struct {
		coding string
		size   int64
	}
	hops := make(chan wire, 4)
	var forget atomic.Bool
	router.Use(func(c *gin.Context) {
		if forget.Swap(false) {
			p.ReceiveDictionaries = new(httpclient.PeerDictionaries)
		}
		hops <- wire{c.GetHeader("Content-Encoding"), c.Request.ContentLength}
		c.Next()
	}, middleware.WithInferenceBody(p))
	router.POST("/v1/responses", h.CreateResponse)
	inner := httptest.NewTLSServer(router)
	t.Cleanup(inner.Close)
	t.Run("outer", func(t *testing.T) {
		client := httpclient.NewHttpClientWithProxy(&httpclient.ProxyConfig{Type: httpclient.ProxyTypeDisabled}, httpclient.WithInsecureSkipVerify(true))
		defer client.CloseIdleConnections()
		db, h, project := newResponsesIntegrationFixtureWithClient(t, inner.URL, client, func(c *ent.ChannelCreate) { c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}}) })
		h.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), PeerDictionaryEndpoints: []string{inner.URL + "/v1/responses"}})
		outer := responsesIntegrationGateway(t, db, h, project)
		sample := make([]byte, 96<<10)
		rng := rand.New(rand.NewPCG(9, 4))
		for i := range sample {
			sample[i] = byte(rng.Uint32())
		}
		history := base64.StdEncoding.EncodeToString(sample)
		var sizes []int64
		for n := range 3 {
			if n == 2 {
				forget.Store(true)
			}
			want := fmt.Sprintf("%s turn %d", history, n)
			body := fmt.Sprintf(`{"model":"synthetic-model","input":[{"role":"user","type":"message","content":[{"type":"input_text","text":%q}]}]}`, want)
			code, response := responsesIntegrationPost(t, outer, "/v1/responses", body)
			require.Equal(t, 200, code, string(response))
			select {
			case got := <-delivered:
				require.True(t, want == got, "forwarded input changed")
			default:
				t.Fatal("supplier was not called")
			}
			hop := <-hops
			sizes = append(sizes, hop.size)
			if n == 0 {
				require.Empty(t, hop.coding)
			} else {
				require.Equal(t, httpclient.PeerEncoding, hop.coding)
			}
			if n == 2 {
				select {
				case retry := <-hops:
					require.Empty(t, retry.coding)
					require.Equal(t, sizes[0], retry.size)
				default:
					t.Fatal("missing full-body retry after dictionary loss")
				}
			}
		}
		require.Empty(t, delivered)
		require.Empty(t, hops)
		require.Less(t, sizes[1], sizes[0]/10)
		t.Logf("three turns, four gateway uploads, three supplier calls; first full %d bytes, second encoded %d bytes", sizes[0], sizes[1])
	})
}

func TestHTTPAndWebSocketShareInflightBodyBudget(t *testing.T) {
	const m = 1 << 20
	payload := biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), MaxInflightBodyMiB: 2})
	router := gin.New()
	router.Use(middleware.WithInferenceBody(payload))
	router.GET("/upgrade-check", func(c *gin.Context) {
		require.True(t, websocket.IsWebSocketUpgrade(c.Request))
		require.Same(t, payload.BodyBudget, httpclient.BodyBudgetFromContext(c.Request.Context()))
		c.Status(http.StatusNoContent)
	})
	upgrade := httptest.NewRequest(http.MethodGet, "/upgrade-check", nil)
	upgrade.Header.Set("Connection", "keep-alive, Upgrade")
	upgrade.Header.Set("Upgrade", "another, WebSocket")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, upgrade)
	require.Equal(t, http.StatusNoContent, w.Code)
	var active atomic.Int32
	router.GET("/v1/responses", func(c *gin.Context) {
		serveResponsesWebSocket(c, 0, func(ctx context.Context, r *httpclient.Request) (orchestrator.ChatCompletionResult, error) {
			active.Add(1)
			return orchestrator.ChatCompletionResult{}, nil
		}, nil)
	})
	router.POST("/v1/responses", func(c *gin.Context) { c.Status(204) })
	srv := httptest.NewServer(router)
	defer srv.Close()
	conn := dialResponsesWebSocket(t, srv.URL, nil)
	defer conn.Close()
	warm := `{"type":"response.create","generate":false,"model":"m","input":"` + strings.Repeat("w", 2*m) + `"}`
	reply := responsesIntegrationTurn(t, conn, warm)
	require.Equal(t, "response.completed", gjson.GetBytes(reply, "type").String())
	code, _ := responsesIntegrationPost(t, srv, "/v1/responses", `{}`)
	require.Equal(t, 204, code)
	// The retained warmup occupies the budget. The next unknown-length large frame
	// returns a local error and still leaves the socket available for small messages.
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(warm)))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "body_capacity_exhausted", gjson.GetBytes(data, "error.code").String(), string(data))
	bigDone := make(chan int, 1)
	go func() {
		defer func() {
			if cause := recover(); cause != nil {
				t.Errorf("waiting POST panicked: %v", cause)
				bigDone <- 0
			}
		}()
		req, _ := http.NewRequestWithContext(t.Context(), "POST", srv.URL+"/v1/responses", strings.NewReader(strings.Repeat("x", 2*m)))
		resp, err := srv.Client().Do(req)
		if err != nil {
			bigDone <- 0
			return
		}
		defer resp.Body.Close()
		bigDone <- resp.StatusCode
	}()
	select {
	case <-bigDone:
		t.Fatal("large POST bypassed held WS warmup")
	case <-time.After(40 * time.Millisecond):
	}
	require.NoError(t, conn.Close())
	select {
	case code := <-bigDone:
		require.Equal(t, 204, code)
	case <-time.After(time.Second):
		t.Fatal("large POST did not resume after WS close")
	}
	require.Zero(t, active.Load())
}
