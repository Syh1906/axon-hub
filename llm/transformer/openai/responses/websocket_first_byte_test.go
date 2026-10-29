package responses

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
)

func TestWebSocketFirstBytePrecedesCompleteMessage(t *testing.T) {
	remainder := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(remainder) }) }
	upgrader := websocket.Upgrader{WriteBufferSize: 64}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		writer, err := conn.NextWriter(websocket.TextMessage)
		if err != nil {
			return
		}
		if _, err := io.WriteString(writer, `{"type":"response.completed","padding":"`+strings.Repeat("x", 2048)); err != nil {
			return
		}
		select {
		case <-remainder:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(writer, `","response":{"id":"resp_sample","status":"completed"}}`)
		_ = writer.Close()
	}))
	defer server.Close()
	defer release()
	ctx, cancel := context.WithTimeout(webSocketTestContext(), 3*time.Second)
	defer cancel()
	observed := make(chan time.Time, 1)
	executor := NewWebSocketExecutor(nil)
	defer executor.Close()
	stream, err := executor.DoStream(ctx, &httpclient.Request{Method: http.MethodPost, URL: server.URL + "/v1/responses", Body: []byte(`{"model":"model"}`),
		OnFirstResponseByte: func(at time.Time) { observed <- at }})
	require.NoError(t, err)
	defer stream.Close()
	select {
	case <-observed:
		t.Fatal("the handshake is not response payload")
	default:
	}
	finished := make(chan error, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				finished <- fmt.Errorf("WebSocket reader panic: %v", value)
			}
		}()
		if !stream.Next() {
			finished <- fmt.Errorf("stream ended before an event: %v", stream.Err())
			return
		}
		if stream.Current().Type != "response.completed" {
			finished <- fmt.Errorf("unexpected event type")
			return
		}
		finished <- nil
	}()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("first payload byte was not observed")
	}
	select {
	case err := <-finished:
		t.Fatalf("message completed before its final fragment: %v", err)
	default:
	}
	release()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("message did not complete")
	}
	require.False(t, stream.Next())
	require.NoError(t, stream.Err())
}
