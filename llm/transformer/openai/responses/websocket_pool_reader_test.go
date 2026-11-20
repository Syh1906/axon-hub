package responses

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestPooledWebSocketHandlesIdlePingAndRemoteClose(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var connections, requests atomic.Int32
	pingNow := make(chan struct{})
	pong := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		n := connections.Add(1)
		conn.SetPongHandler(func(string) error { pong <- struct{}{}; return nil })
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			turn := requests.Add(1)
			if err = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprint("resp-", turn), "status": "completed", "output": []any{}}}); err != nil {
				return
			}
			if n == 1 && turn == 1 {
				select {
				case <-pingNow:
				case <-time.After(4 * time.Second):
					return
				}
				if err = conn.WriteControl(websocket.PingMessage, []byte("idle"), time.Now().Add(time.Second)); err != nil {
					return
				}
			}
			if n == 1 && turn == 2 {
				return
			}
		}
	}))
	defer server.Close()
	executor := NewWebSocketExecutor(nil)
	defer executor.Close()
	req := &httpclient.Request{URL: server.URL + "/v1/responses", Headers: http.Header{webSocketSessionHeader: {"idle-test"}}, Body: []byte(`{"model":"test","input":[{"role":"user","content":"hello"}]}`)}
	send := func() {
		t.Helper()
		response, err := executor.Do(webSocketTestContext(), req)
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
	}
	send()
	close(pingNow)
	select {
	case <-pong:
	case <-time.After(3 * time.Second):
		t.Fatal("idle upstream ping did not receive pong")
	}
	req.Body = []byte(`{"model":"test","input":[{"role":"user","content":"hello"},{"role":"user","content":"again"}]}`)
	send()
	require.EqualValues(t, 1, connections.Load())
	require.Eventually(t, func() bool { executor.mu.Lock(); defer executor.mu.Unlock(); return len(executor.pool) == 0 }, 3*time.Second, 5*time.Millisecond)
	send()
	require.EqualValues(t, 2, connections.Load())
	require.EqualValues(t, 3, requests.Load())
}

func TestRetiringExecutorCannotForgetReplacementSocket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[]}}`)); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	lane := shared.NewResponsesLane("same-lane", shared.NewResponsesBudget(1<<20))
	defer lane.Close()
	attempt := &shared.ResponsesAttempt{}
	ctx := shared.WithResponsesAttempt(shared.WithResponsesLane(shared.WithResponsesWebSocket(webSocketTestContext()), lane), attempt)
	req := &httpclient.Request{URL: server.URL, Body: []byte(`{"model":"m","input":"hello"}`)}
	old, replacement := NewWebSocketExecutor(nil), NewWebSocketExecutor(nil)
	defer old.Close()
	defer replacement.Close()
	for _, executor := range []*WebSocketExecutor{old, replacement} {
		_, err := executor.Do(ctx, req)
		require.NoError(t, err)
		lane.Remember(shared.ResponsesRecord{ID: "resp", Origin: attempt.Origin()})
	}
	record := lane.Record()
	require.True(t, lane.CanContinue(record))
	require.NoError(t, old.Close())
	require.True(t, lane.CanContinue(record))
}
