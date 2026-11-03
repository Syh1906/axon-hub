package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestResponsesWebSocketInterruptKeepsHTTPContinuation(t *testing.T) {
	release := make(chan struct{})
	finishItem := make(chan struct{})
	t.Cleanup(func() { close(release) })
	canceled := make(chan struct{})
	retained := make(chan []byte, 1)
	process := func(ctx context.Context, request *httpclient.Request) (orchestrator.ChatCompletionResult, error) {
		if gjson.GetBytes(request.Body, "previous_response_id").String() == "resp_active" {
			return orchestrator.ChatCompletionResult{ChatCompletion: &httpclient.Response{Body: []byte(`{"id":"resp_next","status":"completed","output":[]}`)}}, nil
		}
		return orchestrator.ChatCompletionResult{
			RetainResponsesContinuation: func(body []byte) error { retained <- append([]byte(nil), body...); return nil },
			ChatCompletionStream: &interruptTestStream{
				ctx: ctx, release: release, finishItem: finishItem, canceled: canceled,
				events: []*httpclient.StreamEvent{
					{Type: "response.created", Data: []byte(`{"type":"response.created","sequence_number":0,"response":{"id":"resp_active","status":"in_progress","output":[]}}`)},
					{Type: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"rs_1","type":"reasoning"}}`)},
					{Type: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"retained-reasoning"}}`)},
				},
			},
		}, nil
	}
	server := newResponsesWebSocketTestServer(t, process, nil)
	conn := dialResponsesWebSocket(t, server.URL, nil)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"test","input":"first"}`)))
	for range 2 {
		_, _, err := conn.ReadMessage()
		require.NoError(t, err)
	}
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"resp_active"}`)))
	_, ack, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "response.interrupt.accepted", gjson.GetBytes(ack, "type").String(), string(ack))
	close(finishItem)
	// 当前 reasoning 项完成后才结束，不能把半个工具调用或推理项放进续聊。
	_, item, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "response.output_item.done", gjson.GetBytes(item, "type").String())
	_, terminal, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(terminal, "type").String(), string(terminal))
	require.Equal(t, "incomplete", gjson.GetBytes(terminal, "response.status").String())
	require.Equal(t, "interrupted", gjson.GetBytes(terminal, "response.incomplete_details.reason").String())
	require.Equal(t, "retained-reasoning", gjson.GetBytes(terminal, "response.output.0.encrypted_content").String())
	require.JSONEq(t, gjson.GetBytes(terminal, "response").Raw, string(<-retained))
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP request was not canceled after the safe output boundary")
	}
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"test","previous_response_id":"resp_active","input":"continue"}`)))
	_, next, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "resp_next", gjson.GetBytes(next, "response.id").String())
}

func TestResponsesWebSocketInterruptReportsRetentionFailure(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var retained atomic.Bool
	process := func(ctx context.Context, _ *httpclient.Request) (orchestrator.ChatCompletionResult, error) {
		return orchestrator.ChatCompletionResult{
			RetainResponsesContinuation: func([]byte) error {
				retained.Store(true)
				return fmt.Errorf("fixture origin persistence failure")
			},
			ChatCompletionStream: &interruptTestStream{
				ctx: ctx, release: release, canceled: make(chan struct{}),
				events: []*httpclient.StreamEvent{{Data: []byte(`{"type":"response.created","response":{"id":"resp_unretained","status":"in_progress","output":[]}}`)}},
			},
		}, nil
	}
	server := newResponsesWebSocketTestServer(t, process, nil)
	conn := dialResponsesWebSocket(t, server.URL, nil)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"test","input":"first"}`)))
	_, _, err := conn.ReadMessage()
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"resp_unretained"}`)))
	_, ack, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "response.interrupt.accepted", gjson.GetBytes(ack, "type").String())
	_, failure, err := conn.ReadMessage()
	require.NoError(t, err)
	require.True(t, retained.Load())
	require.Equal(t, "error", gjson.GetBytes(failure, "type").String(), "failed retention must not claim a resumable completed snapshot")
	require.False(t, gjson.GetBytes(failure, "response").Exists())
}

func TestResponsesWebSocketInterruptUpstreamTransport(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		advertisedWS bool
		nativeWS     bool
	}{
		{name: "https-only"},
		{name: "handshake-fallback", advertisedWS: true},
		{name: "native-ws", advertisedWS: true, nativeWS: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var handshakes, posts atomic.Int32
			canceled := make(chan struct{}, 1)
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if websocket.IsWebSocketUpgrade(r) {
					handshakes.Add(1)
					if scenario.nativeWS {
						conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
						if err != nil {
							return
						}
						defer conn.Close()
						var request map[string]any
						if conn.ReadJSON(&request) != nil {
							return
						}
						if request["input"] == "next" {
							_ = conn.WriteJSON(gin.H{"type": "response.completed", "response": gin.H{"id": "resp_next", "status": "completed", "output": []any{}}})
							return
						}
						_ = conn.WriteJSON(gin.H{"type": "response.created", "response": gin.H{"id": "resp_http", "status": "in_progress", "output": []any{}}})
						_, _, _ = conn.ReadMessage()
						canceled <- struct{}{}
						return
					}
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				posts.Add(1)
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "next") {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_next\",\"status\":\"completed\",\"output\":[]}}\n\n")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_http\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				canceled <- struct{}{}
			}))
			defer upstream.Close()
			client := httpclient.NewHttpClientWithClient(upstream.Client())
			defer client.CloseIdleConnections()
			var ws *responses.WebSocketExecutor
			if scenario.advertisedWS {
				ws = responses.NewWebSocketExecutor(client)
				defer ws.Close()
			}
			executor := responses.NewTransportExecutor(client, ws, false)
			process := func(ctx context.Context, request *httpclient.Request) (orchestrator.ChatCompletionResult, error) {
				request.URL = upstream.URL + "/v1/responses"
				request.Auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: "local-fixture"}
				stream, err := executor.DoStream(ctx, request)
				return orchestrator.ChatCompletionResult{ChatCompletionStream: stream}, err
			}
			server := newResponsesWebSocketTestServer(t, process, func(_ context.Context, err error) *httpclient.Error {
				return responsesWebSocketRequestError(err.Error(), "", "test_upstream_error")
			})
			conn := dialResponsesWebSocket(t, server.URL, nil)
			defer conn.Close()
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
			require.NoError(t, conn.WriteJSON(gin.H{"type": "response.create", "model": "test", "input": "first"}))
			_, created, err := conn.ReadMessage()
			require.NoError(t, err)
			require.Equal(t, "response.created", gjson.GetBytes(created, "type").String(), string(created))
			require.NoError(t, conn.WriteJSON(gin.H{"type": "response.interrupt", "response_id": "resp_http"}))
			for _, kind := range []string{"response.interrupt.accepted", "response.completed"} {
				_, event, err := conn.ReadMessage()
				require.NoError(t, err)
				require.Equal(t, kind, gjson.GetBytes(event, "type").String(), string(event))
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("upstream request was left running")
			}
			// 同一 WS 连接继续工作；握手失败的渠道也不会再次尝试握手。
			require.NoError(t, conn.WriteJSON(gin.H{"type": "response.create", "model": "test", "input": "next"}))
			_, next, err := conn.ReadMessage()
			require.NoError(t, err)
			require.Equal(t, "resp_next", gjson.GetBytes(next, "response.id").String())
			if scenario.nativeWS {
				require.Zero(t, posts.Load())
				require.Equal(t, int32(2), handshakes.Load())
			} else if scenario.advertisedWS {
				require.Equal(t, int32(2), posts.Load())
				require.Equal(t, int32(1), handshakes.Load())
			} else {
				require.Equal(t, int32(2), posts.Load())
				require.Zero(t, handshakes.Load())
			}
		})
	}
}

func TestResponsesWebSocketInterruptValidationAndLateCompletion(t *testing.T) {
	process := func(_ context.Context, _ *httpclient.Request) (orchestrator.ChatCompletionResult, error) {
		return orchestrator.ChatCompletionResult{ChatCompletion: &httpclient.Response{Body: []byte(`{"id":"resp_done","status":"completed","output":[]}`)}}, nil
	}
	server := newResponsesWebSocketTestServer(t, process, nil)
	conn := dialResponsesWebSocket(t, server.URL, nil)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	for _, invalid := range []string{
		`{"type":"response.interrupt"}`,
		`{"type":"response.interrupt","response_id":"unknown"}`,
		`{"type":"response.interrupt","response_id":"unknown","discard_partial_items":"yes"}`,
	} {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(invalid)))
		_, event, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, "error", gjson.GetBytes(event, "type").String())
	}
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"a","model":"test","input":"first"}`)))
	_, _, err := conn.ReadMessage()
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"resp_done"}`)))
		_, event, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, "response.interrupt.accepted", gjson.GetBytes(event, "type").String())
		require.Equal(t, "a", gjson.GetBytes(event, "stream_id").String())
	}
	other := dialResponsesWebSocket(t, server.URL, nil)
	defer other.Close()
	require.NoError(t, other.SetReadDeadline(time.Now().Add(time.Second)))
	require.NoError(t, other.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"resp_done"}`)))
	_, event, err := other.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "response_not_found", gjson.GetBytes(event, "error.code").String())
}

func TestResponsesWebSocketInterruptIsolatesLanesAndDiscardsOnlyPartialItems(t *testing.T) {
	release := make(chan struct{})
	finishItem := make(chan struct{})
	t.Cleanup(func() { close(release) })
	canceledA, canceledB := make(chan struct{}), make(chan struct{})
	process := func(ctx context.Context, request *httpclient.Request) (orchestrator.ChatCompletionResult, error) {
		id := gjson.GetBytes(request.Body, "input").String()
		canceled := canceledA
		if id == "b" {
			canceled = canceledB
		}
		return orchestrator.ChatCompletionResult{ChatCompletionStream: &interruptTestStream{
			ctx: ctx, release: release, finishItem: finishItem, canceled: canceled,
			events: []*httpclient.StreamEvent{
				{Type: "response.created", Data: []byte(fmt.Sprintf(`{"type":"response.created","response":{"id":"resp_%s","status":"in_progress","output":[]}}`, id))},
				{Type: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"partial","type":"function_call","arguments":""}}`)},
				{Type: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"partial","type":"function_call","call_id":"call_b","name":"read","arguments":"{}"}}`)},
			},
		}}, nil
	}
	server := newResponsesWebSocketTestServer(t, process, nil)
	conn := dialResponsesWebSocket(t, server.URL, nil)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	for _, lane := range []string{"a", "b"} {
		require.NoError(t, conn.WriteJSON(gin.H{"type": "response.create", "stream_id": lane, "model": "test", "input": lane}))
		for range 2 {
			_, _, err := conn.ReadMessage()
			require.NoError(t, err)
		}
	}
	require.NoError(t, conn.WriteJSON(gin.H{"type": "response.interrupt", "response_id": "resp_a", "discard_partial_items": true}))
	for index, kind := range []string{"response.interrupt.accepted", "response.completed"} {
		_, event, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, kind, gjson.GetBytes(event, "type").String())
		require.Equal(t, "a", gjson.GetBytes(event, "stream_id").String())
		require.Equal(t, int64(index+2), gjson.GetBytes(event, "sequence_number").Int())
		if kind == "response.completed" {
			require.Empty(t, gjson.GetBytes(event, "response.output").Array())
		}
	}
	select {
	case <-canceledA:
	case <-time.After(time.Second):
		t.Fatal("discard requested but partial generation was not canceled")
	}
	select {
	case <-canceledB:
		t.Fatal("interrupt canceled another lane")
	default:
	}
	require.NoError(t, conn.WriteJSON(gin.H{"type": "response.interrupt", "response_id": "resp_a"}))
	_, lateAck, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, int64(4), gjson.GetBytes(lateAck, "sequence_number").Int())
	require.NoError(t, conn.WriteJSON(gin.H{"type": "response.interrupt", "response_id": "resp_b"}))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)
	close(finishItem)
	for _, kind := range []string{"response.output_item.done", "response.completed"} {
		_, event, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, kind, gjson.GetBytes(event, "type").String())
		require.Equal(t, "b", gjson.GetBytes(event, "stream_id").String())
	}
}

type interruptTestStream struct {
	ctx        context.Context
	release    <-chan struct{}
	finishItem <-chan struct{}
	canceled   chan struct{}
	events     []*httpclient.StreamEvent
	index      int
}

var _ streams.Stream[*httpclient.StreamEvent] = (*interruptTestStream)(nil)

func (s *interruptTestStream) Next() bool {
	if s.index == 2 {
		select {
		case <-s.finishItem:
		case <-s.ctx.Done():
			return false
		case <-s.release:
			return false
		}
	}
	if s.index < len(s.events) {
		s.index++
		return true
	}
	select {
	case <-s.ctx.Done():
	case <-s.release:
	}
	return false
}

func (s *interruptTestStream) Current() *httpclient.StreamEvent { return s.events[s.index-1] }
func (s *interruptTestStream) Err() error                       { return s.ctx.Err() }
func (s *interruptTestStream) Close() error {
	if s.ctx.Err() != nil {
		close(s.canceled)
	}
	return nil
}
