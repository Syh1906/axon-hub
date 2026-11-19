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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
)

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
