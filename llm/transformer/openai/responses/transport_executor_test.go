package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
	"github.com/stretchr/testify/require"
)

func TestTransportFollowsClientAndCapability(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, clientWS := range []bool{false, true} {
			t.Run(map[bool]string{false: "http-capability", true: "ws-capability"}[enabled]+"/"+map[bool]string{false: "http-client", true: "ws-client"}[clientWS], func(t *testing.T) {
				var wsCalls, httpCalls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if websocket.IsWebSocketUpgrade(r) {
						wsCalls.Add(1)
						c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
						if err != nil {
							return
						}
						defer c.Close()
						var event map[string]any
						if c.ReadJSON(&event) != nil {
							return
						}
						_ = c.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_1", "status": "completed", "output": []any{}}})
					} else {
						httpCalls.Add(1)
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]}}\n\n"))
					}
				}))
				defer srv.Close()
				transport := ""
				if enabled {
					transport = TransportWebSocket
				}
				out, err := NewOutboundTransformerWithConfig(&Config{BaseURL: srv.URL, APIKeyProvider: auth.NewStaticKeyProvider("fixture"), Transport: transport})
				require.NoError(t, err)
				defer out.Stop()
				ctx := context.Background()
				if clientWS {
					ctx = WithTransportSession(shared.WithResponsesWebSocket(ctx))
				}
				client := httpclient.NewHttpClientWithClient(srv.Client())
				defer client.CloseIdleConnections()
				stream, err := out.CustomizeExecutor(client).DoStream(ctx, &httpclient.Request{Method: "POST", URL: srv.URL + "/responses", Body: []byte(`{"model":"test","input":"hello","stream":true}`), Headers: make(http.Header)})
				require.NoError(t, err)
				for stream.Next() {
				}
				require.NoError(t, stream.Err())
				require.NoError(t, stream.Close())
				if enabled && clientWS {
					require.Equal(t, int32(1), wsCalls.Load())
					require.Zero(t, httpCalls.Load())
				} else {
					require.Zero(t, wsCalls.Load())
					require.Equal(t, int32(1), httpCalls.Load())
				}
			})
		}
	}
}

func TestTransportHandshakeFallbackAndConnectionMemory(t *testing.T) {
	var handshakes, posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			handshakes.Add(1)
			w.WriteHeader(405)
			return
		}
		posts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[]}}\n\n"))
	}))
	defer srv.Close()
	client := httpclient.NewHttpClientWithClient(srv.Client())
	defer client.CloseIdleConnections()
	ws := NewWebSocketExecutor(client)
	defer ws.Close()
	executor := NewTransportExecutor(client, ws, false)
	ctx := WithTransportSession(shared.WithResponsesWebSocket(context.Background()))
	var reasons []string
	for range 2 {
		r := &httpclient.Request{Method: "POST", URL: srv.URL + "/responses", Headers: make(http.Header), Body: []byte(`{"input":"hello","stream":true}`), OnTransportSelected: func(_, reason string) {
			if reason != "" {
				reasons = append(reasons, reason)
			}
		}}
		stream, err := executor.DoStream(ctx, r)
		require.NoError(t, err)
		for stream.Next() {
		}
		require.NoError(t, stream.Err())
		_ = stream.Close()
	}
	require.Equal(t, int32(1), handshakes.Load())
	require.Equal(t, int32(2), posts.Load())
	require.Equal(t, []string{"websocket_handshake_failed", "connection_uses_http_fallback"}, reasons)
}

func TestTransportDoesNotReplayAuthFailureOrUnknownExecution(t *testing.T) {
	for _, status := range []int{401, 403, 429, 101} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var posts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !websocket.IsWebSocketUpgrade(r) {
					posts.Add(1)
					return
				}
				if status != 101 {
					w.WriteHeader(status)
					return
				}
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				var event json.RawMessage
				_ = c.ReadJSON(&event)
			}))
			defer srv.Close()
			client := httpclient.NewHttpClientWithClient(srv.Client())
			defer client.CloseIdleConnections()
			ws := NewWebSocketExecutor(client)
			defer ws.Close()
			stream, err := NewTransportExecutor(client, ws, false).DoStream(shared.WithResponsesWebSocket(context.Background()), &httpclient.Request{Method: "POST", URL: srv.URL, Headers: make(http.Header), Body: []byte(`{"input":"hello","stream":true}`)})
			if status == 101 {
				require.NoError(t, err)
				require.False(t, stream.Next())
				require.ErrorIs(t, stream.Err(), httpclient.ErrRequestMayHaveExecuted)
				_ = stream.Close()
			} else {
				require.Error(t, err)
			}
			require.Zero(t, posts.Load())
		})
	}
}

func TestTransportMissingContinuationDoesNotSendIncompleteHistory(t *testing.T) {
	inner := &httpTransportCaptureExecutor{}
	_, err := NewTransportExecutor(inner, nil, true).DoStream(context.Background(), &httpclient.Request{Body: []byte(`{"previous_response_id":"missing","input":[{"type":"function_call_output","call_id":"call","output":"ok"}]}`)})
	require.ErrorIs(t, err, httpclient.ErrContinuationUnavailable)
	require.Nil(t, inner.request)
}

func TestTransportNormalizesUppercaseWebSocketURLForHTTP(t *testing.T) {
	e := &transportExecutor{}
	r := &httpclient.Request{URL: "WSS://example.com/v1/responses"}
	require.Equal(t, "https://example.com/v1/responses", e.httpRequest(r).URL)
	require.Equal(t, "WSS://example.com/v1/responses", r.URL)
}
