package responses

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesContinuationMissingHistoryFailsBeforeDispatch(t *testing.T) {
	// Given: an explicit old ID without a live owning connection.
	var writes, handshakes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handshakes.Add(1)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var body json.RawMessage
		if conn.ReadJSON(&body) == nil {
			writes.Add(1)
		}
	}))
	t.Cleanup(server.Close)
	executor := NewWebSocketExecutor(nil)
	t.Cleanup(func() { require.NoError(t, executor.Close()) })
	ctx := shared.WithSessionID(webSocketTestContext(), "fresh")
	// When
	stream, err := executor.DoStream(ctx, &httpclient.Request{URL: server.URL, Body: []byte(`{"previous_response_id":"old","input":[]}`)})
	if stream != nil {
		require.NoError(t, stream.Close())
	}
	// Then
	require.Error(t, err)
	require.Zero(t, writes.Load())
	require.Zero(t, handshakes.Load(), "an unavailable old ID must not dial a replacement socket")
}

func TestResponsesNativeContinuationSurvivesRetentionLimit(t *testing.T) {
	// Given: a real WS with two completions and an intentionally tiny optional cache.
	frames := make(chan json.RawMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for _, id := range []string{"resp_1", "resp_2"} {
			var frame json.RawMessage
			if conn.ReadJSON(&frame) != nil {
				return
			}
			frames <- frame
			if conn.WriteJSON(map[string]json.RawMessage{"type": json.RawMessage(`"response.completed"`), "response": json.RawMessage(`{"id":"` + id + `","status":"completed","output":[]}`)}) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	executor := NewWebSocketExecutor(nil)
	executor.maxRetainedInput = 8
	t.Cleanup(func() { require.NoError(t, executor.Close()) })
	ctx := shared.WithSessionID(webSocketTestContext(), "native")
	first := []byte(`{"input":[{"role":"user","content":"` + strings.Repeat("x", 1<<20) + `"}]}`)
	stream, err := executor.DoStream(ctx, &httpclient.Request{URL: server.URL, Body: first})
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	<-frames
	// When: delta references the successful response despite optional retention overflow.
	delta := []byte(`{"previous_response_id":"resp_1","input":[{"role":"user","content":"canary"}]}`)
	stream, err = executor.DoStream(ctx, &httpclient.Request{URL: server.URL, Body: delta})
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	// Then: the delta is unchanged and still bound to the same generation.
	var sent struct {
		Previous string `json:"previous_response_id"`
	}
	require.NoError(t, json.Unmarshal(<-frames, &sent))
	require.Equal(t, "resp_1", sent.Previous)
	executor.mu.Lock()
	defer executor.mu.Unlock()
	for _, pc := range executor.pool {
		_, id := pc.previousTurn()
		require.Equal(t, "resp_2", id)
	}
}

func TestResponsesExpiredConnectionDoesNotDialForOldID(t *testing.T) {
	var upgrades atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		upgrades.Add(1)
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"old","status":"completed","output":[]}}`))
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	executor := NewWebSocketExecutor(nil)
	t.Cleanup(func() { require.NoError(t, executor.Close()) })
	ctx := shared.WithSessionID(webSocketTestContext(), "expiry")
	stream, err := executor.DoStream(ctx, &httpclient.Request{URL: server.URL, Body: []byte(`{"input":"root"}`)})
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	executor.mu.Lock()
	for _, pc := range executor.pool {
		pc.mu.Lock()
		pc.createdAt = time.Now().Add(-time.Hour)
		pc.mu.Unlock()
	}
	executor.mu.Unlock()
	_, err = executor.DoStream(ctx, &httpclient.Request{URL: server.URL, Body: []byte(`{"previous_response_id":"old","input":"new"}`)})
	require.ErrorIs(t, err, shared.ErrResponsesContinuationUnavailable)
	require.Equal(t, int32(1), upgrades.Load())
}
