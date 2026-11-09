package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
	"github.com/stretchr/testify/assert"
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

// A new delivery and a replay with another tool's result must reach the HTTPS
// endpoint unchanged, whether the client used HTTPS or fell back from WSS.
func TestResponsesStandaloneThreadDeliveryReplay(t *testing.T) {
	const user = `{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect HUD"}]}`
	const first = `{"type":"function_call_output","id":"fco_first","namespace":"codex_app","name":"send_message_to_thread","output":"<codex_delegation><source_thread_id>A</source_thread_id><input>inspect HUD</input></codex_delegation>"}`
	const second = `{"type":"function_call_output","id":"fco_second","namespace":"codex_app","name":"send_message_to_thread","output":"<codex_delegation><source_thread_id>A</source_thread_id><input>save the result</input></codex_delegation>"}`
	const paired = `{"type":"function_call","call_id":"call_lookup","name":"lookup","arguments":"{}"},{"type":"function_call_output","id":"fco_lookup","call_id":"call_lookup","output":"ok"}`
	inputs := []string{"[" + user + "," + first + "]", "[" + user + "," + first + "," + paired + "," + second + "]"}

	for _, tc := range []struct {
		name           string
		clientWS       bool
		wantHandshakes int32
		wantReasons    []string
	}{
		{name: "direct-https"},
		{name: "wss-to-https", clientWS: true, wantHandshakes: 1, wantReasons: []string{"websocket_handshake_failed", "connection_uses_http_fallback"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var handshakes, posts atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if websocket.IsWebSocketUpgrade(r) {
					handshakes.Add(1)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				index := int(posts.Add(1)) - 1
				assert.Equal(t, http.MethodPost, r.Method)
				assert.NotNil(t, r.TLS)
				var body struct{ Input json.RawMessage }
				if index >= len(inputs) || json.NewDecoder(r.Body).Decode(&body) != nil {
					http.Error(w, "unexpected POST", http.StatusBadRequest)
					return
				}
				if !assert.JSONEq(t, inputs[index], string(body.Input)) {
					http.Error(w, "tool history changed", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[]}}\n\n"))
			}))
			defer srv.Close()
			client := httpclient.NewHttpClientWithClient(srv.Client())
			defer client.CloseIdleConnections()
			out, err := NewOutboundTransformerWithConfig(&Config{BaseURL: srv.URL, Transport: TransportWebSocket, APIKeyProvider: auth.NewStaticKeyProvider("fixture")})
			require.NoError(t, err)
			defer out.Stop()
			executor := out.CustomizeExecutor(client)
			ctx := t.Context()
			if tc.clientWS {
				ctx = WithTransportSession(shared.WithResponsesWebSocket(ctx))
			}
			var reasons []string
			for _, input := range inputs {
				body := []byte(`{"model":"test","stream":true,"input":` + input + `}`)
				req, err := NewInboundTransformer().TransformRequest(ctx, &httpclient.Request{Method: http.MethodPost, Body: body})
				require.NoError(t, err)
				forwarded, err := out.TransformRequest(ctx, req)
				require.NoError(t, err)
				forwarded.OnTransportSelected = func(_, reason string) {
					if reason != "" {
						reasons = append(reasons, reason)
					}
				}
				stream, err := executor.DoStream(ctx, forwarded)
				require.NoError(t, err)
				for stream.Next() {
				}
				require.NoError(t, stream.Err())
				require.NoError(t, stream.Close())
			}
			require.Equal(t, tc.wantHandshakes, handshakes.Load())
			require.Equal(t, int32(len(inputs)), posts.Load())
			require.Equal(t, tc.wantReasons, reasons)
		})
	}
}

func TestTransportDoesNotReplayAuthFailureOrUnknownExecution(t *testing.T) {
	for _, tc := range []struct {
		status    int
		closeCode int
	}{
		{status: 401}, {status: 403}, {status: 429}, {status: 101},
		{status: 101, closeCode: websocket.CloseTryAgainLater},
	} {
		t.Run(fmt.Sprintf("%s/close-%d", http.StatusText(tc.status), tc.closeCode), func(t *testing.T) {
			status := tc.status
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
				if c.ReadJSON(&event) == nil && tc.closeCode != 0 {
					_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(tc.closeCode, "no available account"))
				}
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
				if tc.closeCode != 0 {
					require.ErrorContains(t, stream.Err(), "1013: no available account")
				}
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
