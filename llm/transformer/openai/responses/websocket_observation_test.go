package responses

import (
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebSocketEvidenceRequiresOneResponse(t *testing.T) {
	var results []httpclient.WebSocketObservation
	policy := &httpclient.ResponsesWebSocketPolicy{Observe: func(o httpclient.WebSocketObservation) { results = append(results, o) }}
	e := webSocketEvidence{policy: policy}
	e.event("response.created", []byte(`{"response":{"id":"a"}}`))
	e.event("response.output_text.delta", []byte(`{"response_id":"a","delta":"hello"}`))
	e.event("response.created", []byte(`{"response":{"id":"b"}}`))
	e.event("response.completed", []byte(`{"response":{"id":"b","status":"completed","output":[]}}`))
	require.Equal(t, "empty_output", results[0].Reason)
	reportWebSocketFailure(policy, "handshake", &httpclient.Error{StatusCode: 429, Headers: http.Header{"Retry-After": {"90"}}})
	require.Equal(t, "rate_limited", results[1].Reason)
	require.Equal(t, 90*time.Second, results[1].RetryAfter)
}

func TestWebSocketHandshakeBudgetUsesSavedHTTPAddress(t *testing.T) {
	var handshakes, posts atomic.Int32
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handshakes.Add(1); <-r.Context().Done() }))
	defer ws.Close()
	httpEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/custom/responses", r.URL.Path)
		posts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"one\",\"status\":\"completed\",\"output\":[]}}\n\n"))
	}))
	defer httpEndpoint.Close()
	cli := httpclient.NewHttpClientWithClient(ws.Client())
	e := NewTransportExecutor(cli, NewWebSocketExecutor(cli), false)
	ctx := shared.WithResponsesWebSocket(t.Context())
	req := &httpclient.Request{Method: "POST", URL: ws.URL, Body: []byte(`{"model":"test","input":"hello"}`), ResponsesWebSocket: &httpclient.ResponsesWebSocketPolicy{HandshakeTimeout: 100 * time.Millisecond, HTTPURL: httpEndpoint.URL + "/custom/responses"}}
	start := time.Now()
	st, err := e.DoStream(ctx, req)
	require.NoError(t, err)
	for st.Next() {
	}
	require.NoError(t, st.Err())
	require.NoError(t, st.Close())
	require.Less(t, time.Since(start), 3*time.Second)
	require.EqualValues(t, 1, handshakes.Load())
	require.EqualValues(t, 1, posts.Load())
}
