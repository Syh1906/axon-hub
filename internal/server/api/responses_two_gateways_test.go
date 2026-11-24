package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/lifecycle"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/internal/tracing"
	"github.com/looplj/axonhub/llm/httpclient"
)

// Headers arrive immediately on both protocols. Only the first real Chat
// delta is delayed, and the POST is much smaller than the upload threshold.
func TestChatGatewayLearnsProtocolFromFirstOutput(t *testing.T) {
	observed := make(chan int, 7)
	encodings := make(chan string, 7)
	supplier := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.ProtoMajor
		encodings <- r.Header.Get("Content-Encoding")
		decoded, err := httpclient.ReadHTTPRequest(r)
		require.NoError(t, err)
		require.True(t, gjson.GetBytes(decoded.Body, "messages.0.content").Exists())
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Accept-Encoding", "zstd")
		const chunk = `{"id":"chat","object":"chat.completion.chunk","model":"synthetic-model","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`
		fmt.Fprintf(w, "data: "+chunk+"\n\n", `{"role":"assistant"}`, "null")
		w.(http.Flusher).Flush()
		if r.ProtoMajor == 2 {
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Fprintf(w, "data: "+chunk+"\n\n", `{"content":"hello"}`, "null")
		fmt.Fprintf(w, "data: "+chunk+"\n\ndata: [DONE]\n\n", "{}", `"stop"`)
	}))
	supplier.EnableHTTP2 = true
	supplier.StartTLS()
	t.Cleanup(supplier.Close)
	client := httpclient.NewHttpClientWithProxy(&httpclient.ProxyConfig{Type: httpclient.ProxyTypeDisabled}, httpclient.WithInsecureSkipVerify(true))
	t.Cleanup(client.CloseIdleConnections)
	db, handlers, project := newResponsesIntegrationFixtureWithClient(t, supplier.URL, client, func(c *ent.ChannelCreate) {
		c.SetType(channel.TypeOpenai)
	})
	site := responsesIntegrationGateway(t, db, handlers, project)
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), db))
	for i, want := range []int{2, 2, 1, 2, 1, 1} {
		code, body := responsesIntegrationPost(t, site, "/v1/chat/completions", `{"model":"synthetic-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
		require.Equal(t, http.StatusOK, code, string(body))
		require.Contains(t, string(body), "hello")
		protocol := <-observed
		require.Empty(t, <-encodings)
		// Final timing is persisted after protocol feedback. Do not start
		// the next comparison while the previous stream is still settling.
		require.Eventually(t, func() bool {
			row, err := db.Request.Get(ctx, i+1)
			return err == nil && row.StreamTiming != nil && row.StreamTiming.FirstEventMs != nil
		}, 2*time.Second, 5*time.Millisecond)
		timing := db.Request.GetX(ctx, i+1).StreamTiming
		t.Logf("POST %d used HTTP version %d and delivered first event %s at %d ms", i+1, protocol, timing.FirstEvent, *timing.FirstEventMs)
		require.Equalf(t, want, protocol, "upstream POST %d", i+1)
	}
	// The Chat peer has advertised its decoder. This new size/encoding
	// group starts its own protocol comparison with a single compressed POST.
	large := `{"model":"synthetic-model","stream":true,"messages":[{"role":"user","content":"` + strings.Repeat("abc", 400000) + `"}]}`
	code, body := responsesIntegrationPost(t, site, "/v1/chat/completions", large)
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, 2, <-observed)
	require.Equal(t, "zstd", <-encodings)
	settings := handlers.ChatCompletionHandlers.ChatCompletionOrchestrator.SystemService
	require.NoError(t, settings.SetUpstreamHTTPProtocol(ctx, httpclient.ProtocolHTTP1))
	require.NoError(t, settings.SetUpstreamAutomaticCompression(ctx, false))
	code, body = responsesIntegrationPost(t, site, "/v1/chat/completions", large)
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, 1, <-observed)
	require.Empty(t, <-encodings)
	require.Empty(t, observed)
}

func TestResponsesGatewayLearnsAfterEmptyMessageStart(t *testing.T) {
	protocols := make(chan int, 6)
	supplier := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocols <- r.ProtoMajor
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg","role":"assistant","content":[]}}`+"\n\n")
		w.(http.Flusher).Flush()
		if r.ProtoMajor == 2 {
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Fprint(w, "data: "+`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"hello"}`+"\n\n")
		fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"id":"resp","model":"synthetic-model","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}}`+"\n\n")
	}))
	supplier.EnableHTTP2 = true
	supplier.StartTLS()
	t.Cleanup(supplier.Close)
	client := httpclient.NewHttpClientWithProxy(&httpclient.ProxyConfig{Type: httpclient.ProxyTypeDisabled}, httpclient.WithInsecureSkipVerify(true))
	t.Cleanup(client.CloseIdleConnections)
	db, handlers, project := newResponsesIntegrationFixtureWithClient(t, supplier.URL, client)
	site := responsesIntegrationGateway(t, db, handlers, project)
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), db))
	for i, want := range []int{2, 2, 1, 2, 1, 1} {
		code, body := responsesIntegrationPost(t, site, "/v1/responses", `{"model":"synthetic-model","input":"hello","stream":true}`)
		require.Equal(t, http.StatusOK, code, string(body))
		require.Contains(t, string(body), "response.completed")
		require.Equal(t, want, <-protocols)
		require.Eventually(t, func() bool {
			row, err := db.Request.Get(ctx, i+1)
			return err == nil && row.StreamTiming != nil && row.StreamTiming.FirstEvent == "response.output_item.added.message"
		}, 2*time.Second, 5*time.Millisecond)
	}
}

// The outer gateway uses WS to the inner gateway. Only the inner gateway
// can observe its final HTTP supplier and restore the second turn for it.
func TestResponsesTwoGatewaysWebSocketToHTTP(t *testing.T) {
	var posts atomic.Int32
	type received struct {
		body      []byte
		encoding  string
		wireBytes int64
	}
	receivedRequests := make(chan received, 3)
	supplier := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		encoding, size := r.Header.Get("Content-Encoding"), r.ContentLength
		b, err := httpclient.ReadHTTPRequest(r)
		require.NoError(t, err)
		receivedRequests <- received{b.Body, encoding, size}
		n := posts.Add(1)
		item := `{"type":"message","id":"msg","role":"assistant","content":[{"type":"output_text","text":"OK"}]}`
		if n == 2 {
			item = `{"type":"function_call","id":"fc","name":"lookup","call_id":"tool-1","arguments":"{}"}`
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r-%d\",\"model\":\"synthetic-model\",\"object\":\"response\",\"status\":\"completed\",\"output\":[%s],\"usage\":{\"input_tokens\":100,\"output_tokens\":5,\"total_tokens\":105}}}\n\n", n, item)
	}))
	innerDB, inner, innerProject := newResponsesIntegrationFixture(t, supplier.URL)
	// Global compression off does not revoke this explicitly verified endpoint.
	require.NoError(t, inner.ResponseCompletionHandlers.ChatCompletionOrchestrator.SystemService.SetUpstreamAutomaticCompression(
		authz.WithTestBypass(ent.NewContext(t.Context(), innerDB)), false))
	inner.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{
		Directory: t.TempDir(), ResponsesZstdEndpoints: []string{supplier.URL + "/v1/responses"},
	})
	innerSite := responsesIntegrationGateway(t, innerDB, inner, innerProject)
	t.Run("outer", func(t *testing.T) {
		db, h, project := newResponsesIntegrationFixture(t, innerSite.URL, func(c *ent.ChannelCreate) {
			c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", SupportsWebSockets: new(true)}})
		})
		ctx, id, tester := fixtureChannelTester(t.Context(), db, h, project.ID)
		proof, err := tester.VerifyChannelWebSocket(ctx, id, "synthetic-model", false)
		require.NoError(t, err)
		require.Equal(t, "verified", proof.State)
		require.EqualValues(t, 1, posts.Load())
		<-receivedRequests
		site := responsesIntegrationGateway(t, db, h, project)
		conn := responsesIntegrationDial(t, site)
		img := image.NewGray(image.Rect(0, 0, 1536, 1024))
		rng := rand.New(rand.NewPCG(11, 19))
		for i := range img.Pix {
			img.Pix[i] = uint8(rng.Uint32())
		}
		var encoded bytes.Buffer
		require.NoError(t, jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 90}))
		imageURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
		first := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","input":[{"role":"user","content":[{"type":"input_text","text":"first-question"},{"type":"input_image","image_url":"`+imageURL+`"}]}]}`)
		require.Equal(t, "r-2", gjson.GetBytes(first, "response.id").String(), string(first))
		firstRequest := <-receivedRequests
		require.True(t, bytes.Contains(firstRequest.body, []byte(imageURL)), "first HTTP turn retains the complete image")
		require.Equalf(t, "zstd", firstRequest.encoding, "first logical body %d bytes, wire %d bytes", len(firstRequest.body), firstRequest.wireBytes)
		require.Less(t, firstRequest.wireBytes, int64(len(firstRequest.body)))
		second := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","previous_response_id":"r-2","input":[{"type":"function_call_output","call_id":"tool-1","output":"lookup-result"}]}`)
		require.Equal(t, "r-3", gjson.GetBytes(second, "response.id").String(), string(second))
		secondRequest := <-receivedRequests
		body, wire := secondRequest.body, secondRequest.wireBytes
		require.Equal(t, "zstd", secondRequest.encoding)
		require.Less(t, wire, int64(len(body)))
		require.True(t, bytes.Contains(body, []byte(imageURL)), "the HTTP continuation restores the complete image")
		require.Contains(t, string(body), "first-question")
		require.Contains(t, string(body), "lookup-result")
		require.Contains(t, string(body), `"type":"function_call"`)
		require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
		require.EqualValues(t, 3, posts.Load())
		outerRows := db.RequestExecution.Query().AllX(ctx)
		require.Len(t, outerRows, 3)
		for _, row := range outerRows {
			require.Equal(t, "websocket", row.Selection.UpstreamTransport)
		}
		innerRows := innerDB.RequestExecution.Query().AllX(ctx)
		require.Len(t, innerRows, 3)
		for _, row := range innerRows {
			require.Equal(t, "websocket", row.Selection.ClientTransport)
			require.Equal(t, "http", row.Selection.UpstreamTransport)
		}
		innerTurns := innerDB.Request.Query().Order(request.ByID()).AllX(ctx)
		require.Len(t, innerTurns, 3)
		secondBytes := gjson.GetBytes(innerTurns[2].RequestBody, "_axon_payload.size").Int()
		require.True(t, secondBytes > 0 && secondBytes < 4096, "middle hop should receive only the new tool output")
		for _, siteDB := range []*ent.Client{innerDB, db} {
			turns := siteDB.Request.Query().Order(request.ByID()).AllX(ctx)
			require.Len(t, turns, 3)
			// The WS terminal can reach the client before stream.Close settles usage.
			ids := []int{turns[1].ID, turns[2].ID}
			require.Eventually(t, func() bool {
				settled, err := siteDB.Request.Query().Where(request.IDIn(ids...), request.StatusEQ(request.StatusCompleted)).Count(ctx)
				if err != nil || settled != len(ids) {
					return false
				}
				rows, err := siteDB.UsageLog.Query().Where(usagelog.RequestIDIn(ids...)).Count(ctx)
				return err == nil && rows == len(ids)
			}, 5*time.Second, 10*time.Millisecond)
			for _, turn := range turns[1:] {
				usage := siteDB.UsageLog.Query().Where(usagelog.RequestIDEQ(turn.ID)).OnlyX(ctx)
				require.EqualValues(t, 100, usage.PromptTokens)
				require.EqualValues(t, 5, usage.CompletionTokens)
			}
		}
		t.Logf("second turn: inner ingress %d bytes, final HTTP JSON %d bytes, zstd body %d bytes, business posts %d", secondBytes, len(body), wire, posts.Load()-1)
	})
}

func TestResponsesHTTPGatewaysNegotiatePerHop(t *testing.T) {
	var posts atomic.Int32
	var wireBytes atomic.Int64
	supplier := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		wireBytes.Store(r.ContentLength)
		_, err := httpclient.ReadHTTPRequest(r)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Accept-Encoding", "zstd")
		_, _ = io.WriteString(w, `{"id":"r","model":"synthetic-model","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}`)
	}))
	innerDB, inner, project := newResponsesIntegrationFixture(t, supplier.URL)
	inner.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir()})
	innerSite := responsesIntegrationGateway(t, innerDB, inner, project)
	codings := make(chan string, 2)
	observed := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codings <- r.Header.Get("Content-Encoding")
		innerSite.Config.Handler.ServeHTTP(w, r)
	}))
	t.Run("outer", func(t *testing.T) {
		db, h, p := newResponsesIntegrationFixture(t, observed.URL)
		h.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir()})
		site := responsesIntegrationGateway(t, db, h, p)
		longInput := strings.Repeat("plain input ", 110000)
		for _, input := range []string{"small", longInput} {
			code, b := responsesIntegrationPost(t, site, "/v1/responses", `{"model":"synthetic-model","input":[{"role":"user","content":"`+input+`"}]}`)
			require.Equal(t, 200, code, string(b))
		}
		require.Equal(t, "", <-codings)
		require.Equal(t, "zstd", <-codings)
		require.EqualValues(t, 2, posts.Load())
		require.Less(t, wireBytes.Load(), int64(len(longInput)))
		t.Logf("two HTTP gateways: final wire body %d bytes for %d bytes of input text", wireBytes.Load(), len(longInput))
		inner.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), DisableZstdAdvertisement: true})
		res, err := innerSite.Client().Post(innerSite.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"synthetic-model","input":[{"role":"user","content":"last"}]}`))
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, 200, res.StatusCode)
		require.Empty(t, res.Header.Get("Accept-Encoding"), "the supplier's own declaration must never leak across the disabled gateway")
	})
}

func TestHTTPClientsRewriteHistoryAcrossTwoGateways(t *testing.T) {
	const answer = `{"type":"message","id":"reply","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}`
	inputs := make(chan string, 3)
	var calls, upgrades atomic.Int32
	supplier := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		req, err := httpclient.ReadHTTPRequest(r)
		require.NoError(t, err)
		inputs <- gjson.GetBytes(req.Body, "input").Raw
		require.False(t, gjson.GetBytes(req.Body, "previous_response_id").Exists())
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"id":"reply","type":"message","role":"assistant","status":"in_progress","content":[]}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.content_part.added","item_id":"reply","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","item_id":"reply","output_index":0,"content_index":0,"delta":"ok"}`+"\n\n")
		fmt.Fprintf(w, `data: {"type":"response.output_item.done","output_index":0,"item":%s}`+"\n\n", answer)
		fmt.Fprintf(w, `data: {"type":"response.completed","response":{"id":"turn-%d","model":"synthetic-model","object":"response","status":"completed","output":[%s]}}`+"\n\n", calls.Add(1), answer)
	}))
	innerDB, innerHandlers, innerProject := newResponsesIntegrationFixture(t, supplier.URL)
	shutdown := lifecycle.New()
	inner := responsesIntegrationGateway(t, innerDB, innerHandlers, innerProject, shutdown)
	counted := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, websocket.IsWebSocketUpgrade(r))
		upgrades.Add(1)
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	defer shutdown.Shutdown(nil)

	t.Run("outer", func(t *testing.T) {
		db, h, project := newResponsesIntegrationFixture(t, counted.URL, func(c *ent.ChannelCreate) {
			c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}, {APIFormat: "openai/responses-ws"}})
			c.SetSettings(&objects.ChannelSettings{ResponsesTransportMode: objects.ResponsesTransportWSOnly})
		})
		router := responsesFixtureRouter(db, project)
		rs := h.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService
		router.Use(middleware.WithInferenceBody(rs.PayloadService), middleware.WithTrace(tracing.Config{CodexTraceEnabled: true}, biz.NewTraceService(biz.TraceServiceParams{Ent: db, RequestService: rs})))
		router.POST("/v1/responses", h.CreateResponse)
		site := responsesIntegrationServer(t, router)
		ctx := authz.WithTestBypass(ent.NewContext(t.Context(), innerDB))
		var previousItem, previousAnswer string
		for i, input := range []string{"old-question", "fresh-question", "follow-up"} {
			item := `{"type":"message","role":"user","content":[{"type":"input_text","text":"` + input + `"}]}`
			itemsJSON := item
			if i == 2 {
				itemsJSON = previousItem + "," + previousAnswer + "," + item
			}
			body := `{"model":"synthetic-model","input":[` + itemsJSON + `]}`
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, site.URL+"/v1/responses", strings.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Session_id", "rewrite-session")
			req.Header.Set("Content-Type", "application/json")
			res, err := site.Client().Do(req)
			require.NoError(t, err)
			data, err := io.ReadAll(res.Body)
			require.NoError(t, res.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, res.StatusCode, string(data))
			require.Equal(t, fmt.Sprintf("turn-%d", i+1), gjson.GetBytes(data, "id").String(), string(data))
			items := gjson.Parse(<-inputs).Array()
			if i < 2 {
				require.Len(t, items, 1)
			} else {
				require.Len(t, items, 3)
				require.Equal(t, "fresh-question", items[0].Get("content.0.text").String())
				require.Equal(t, "reply", items[1].Get("id").String())
				last := innerDB.Request.Query().Order(ent.Desc(request.FieldID)).FirstX(ctx)
				received, err := innerHandlers.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.LoadRequestBody(ctx, last)
				require.NoError(t, err)
				require.Equal(t, "turn-2", gjson.GetBytes(received, "previous_response_id").String(), "inner gateway must receive a delta")
				require.Len(t, gjson.GetBytes(received, "input").Array(), 1)
			}
			require.Equal(t, input, items[len(items)-1].Get("content.0.text").String())
			previousItem, previousAnswer = item, gjson.GetBytes(data, "output.0").Raw
			require.NotEmpty(t, previousAnswer, string(data))
		}
		require.EqualValues(t, 1, upgrades.Load())
		require.EqualValues(t, 3, calls.Load())
	})
}
