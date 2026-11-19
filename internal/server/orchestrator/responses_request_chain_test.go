package orchestrator

import (
	"bytes"
	"context"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/transformer"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRestoredCiphertextCannotFallbackToAnotherHTTPEndpoint(t *testing.T) {
	var gets, posts atomic.Int32
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gets.Add(1); w.WriteHeader(405) }))
	defer ws.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); w.WriteHeader(200) }))
	defer other.Close()
	wsOut, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: ws.URL, Transport: responses.TransportWebSocket, APIKeyProvider: auth.NewStaticKeyProvider("test")})
	require.NoError(t, err)
	defer wsOut.Stop()
	httpOut, err := responses.NewOutboundTransformer(other.URL, "test")
	require.NoError(t, err)
	ch := &biz.Channel{Channel: &ent.Channel{ID: 1, BaseURL: ws.URL, Settings: &objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "test", APIFormats: []string{"openai/responses-ws", "openai/responses"}}}}}, Outbounds: map[string]transformer.Outbound{"openai/responses": httpOut}}
	candidate := &ChannelModelsCandidate{Channel: ch, Models: []biz.ChannelModelEntry{{ActualModel: "test"}}}
	ctx := shared.WithResponsesWebSocket(t.Context())
	state := &PersistenceState{CurrentCandidate: candidate, ChannelService: new(biz.ChannelService), ResponsesContinuation: &responsesContinuation{complete: true, owner: &shared.ResponsesRecord{ID: "old", Origin: shared.ResponsesOrigin{Endpoint: wsOut.ResponsesURL()}}}}
	p := &PersistentOutboundTransformer{state: state}
	raw := &httpclient.Request{Method: "POST", URL: wsOut.ResponsesURL(), APIFormat: "openai/responses-ws", Body: []byte(`{"model":"test","input":[{"type":"reasoning","encrypted_content":"only-A-can-read"}]}`)}
	exec := &ent.RequestExecution{Selection: &objects.RequestExecutionSelection{}}
	require.NoError(t, p.configureWebSocket(ctx, raw, exec))
	require.True(t, raw.ResponsesWebSocket.NativeOnly)
	require.Zero(t, raw.ResponsesWebSocket.HandshakeTimeout)
	_, err = wsOut.CustomizeExecutor(httpclient.NewHttpClient()).DoStream(ctx, raw)
	require.Error(t, err)
	require.EqualValues(t, 1, gets.Load())
	require.Zero(t, posts.Load())
	candidate.continuationUpstreamRef = biz.ResponsesUpstreamRef(httpOut.ResponsesURL(), raw.Headers)
	require.NoError(t, p.configureWebSocket(ctx, raw, exec))
	require.Equal(t, "continuation_uses_http_origin", raw.ResponsesWebSocket.HTTPReason)
	candidate.continuationUpstreamRef = "third-origin"
	require.Error(t, p.configureWebSocket(ctx, raw, exec))
	raw.Body = []byte(`{"model":"test","input":"ordinary"}`)
	require.NoError(t, p.configureWebSocket(ctx, raw, exec))
	require.Equal(t, "websocket_temporarily_unavailable", raw.ResponsesWebSocket.HTTPReason)
	_, err = wsOut.CustomizeExecutor(httpclient.NewHttpClient()).Do(ctx, raw)
	require.NoError(t, err)
	require.EqualValues(t, 1, posts.Load())
}

func TestDeliveredHistoryKeepsEffectiveInputAndClientOutput(t *testing.T) {
	ctx := shared.WithSessionScope(shared.WithResponsesAPI(t.Context()), "fixture-project/key")
	store := newResponsesSessionStore()
	writes := 0
	store.save = func(_ context.Context, _, _ []byte) { writes++ }
	original := []byte(`{"model":"gpt","input":"question"}`)
	effective := []byte(`{"model":"gpt","input":[{"role":"user","content":"question"},{"role":"user","content":"plugin-1"}]}`)
	provider := []byte(`{"id":"first","status":"completed","output":[{"type":"function_call","name":"run","call_id":"tool-1","arguments":"{}"}]}`)
	c := &responsesContinuation{holdHTTPHistory: true, complete: true, snapshotBody: effective, effective: effective, store: store, capture: &responsesCompletion{body: provider}}
	c.accept(ctx)
	require.Zero(t, writes)
	delivered := bytes.Replace(provider, []byte("tool-1"), []byte("client-tool"), 1)
	c.recordDelivered(ctx, store, original, delivered)
	body, _, err := store.prepare(ctx, []byte(`{"model":"gpt","previous_response_id":"first","input":[{"type":"function_call_output","call_id":"client-tool","output":"ok"}]}`))
	require.NoError(t, err)
	require.Contains(t, string(body), "plugin-1")
	require.Contains(t, string(body), "client-tool")
	require.NotContains(t, string(body), "previous_response_id")
	require.Equal(t, 1, writes)
	c.flushHTTPHistory(ctx)
	require.Equal(t, 1, writes)

	// A client-visible ID produced by a plugin must not discard the native ID.
	c.capture.body = provider
	c.accept(ctx)
	c.recordDelivered(ctx, store, original, bytes.Replace(delivered, []byte(`"first"`), []byte(`"client-id"`), 1))
	require.Equal(t, 3, writes)
	require.NotNil(t, store.lookup(ctx, "first"))
	require.NotNil(t, store.lookup(ctx, "client-id"))

	// Closing before client aggregation still saves the provider's completed fact.
	c.capture.body = provider
	c.accept(ctx)
	c.flushHTTPHistory(ctx)
	c.flushHTTPHistory(ctx)
	require.Equal(t, 4, writes)
}

func nativePassThroughFixture(t testing.TB, size int) (*PersistentOutboundTransformer, *llm.Request) {
	t.Helper()
	raw := append([]byte(`{"model":"fixture","input":[{"role":"user","content":[{"type":"input_text","text":"`), bytes.Repeat([]byte("a"), size)...)
	raw = append(raw, []byte(`"}]}],"provider_extension":{"keep":true},"stream":true}`)...)
	in := &httpclient.Request{Body: raw}
	r, err := responses.NewInboundTransformer().TransformRequest(t.Context(), in)
	require.NoError(t, err)
	r.RawRequest = in
	out, err := responses.NewOutboundTransformer("https://example.invalid", "fixture")
	require.NoError(t, err)
	ch := &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{PassThroughBody: new(true)}}}
	p := &PersistentOutboundTransformer{wrapped: out, state: &PersistenceState{OriginalRequestStream: r.Stream, LlmRequest: r, CurrentCandidate: &ChannelModelsCandidate{Channel: ch}}}
	return p, r
}
func TestSparseResponsesTerminalUsesHistoryAggregator(t *testing.T) {
	// Dropped deltas cannot be reconstructed from an empty terminal array.
	require.False(t, completeHistoryOutput(gjson.Parse("[]"), nil, true))
	chunks := []*httpclient.StreamEvent{{Type: "response.output_text.delta", Data: []byte(`{"output_index":0,"content_index":0,"delta":"longer answer"}`)}}
	item := `{"type":"message","content":[{"type":"output_text","text":"short"}]}`
	output := gjson.Parse("[" + item + "]")
	require.False(t, completeHistoryOutput(output, chunks, false))
	require.True(t, completeHistoryOutput(output, nil, false))
	done := []*httpclient.StreamEvent{{Type: "response.output_item.done", Data: []byte(`{"output_index":0,"item":` + item + `}`)}}
	require.True(t, completeHistoryOutput(output, done, false))
}

func TestNativePassThroughPreservesBytesAndCredential(t *testing.T) {
	p, r := nativePassThroughFixture(t, 128)
	actual, err := p.transformProviderRequest(t.Context(), r)
	require.NoError(t, err)
	require.True(t, p.preparedPassThrough)
	actual, err = applyPassThroughRequestBody(p, nil).OnOutboundRawRequest(t.Context(), actual)
	require.NoError(t, err)
	require.Equal(t, r.RawRequest.Body, actual.Body)
	require.Equal(t, "fixture", actual.Auth.APIKey)
	r.Model = "mapped"
	req, err := p.transformProviderRequest(t.Context(), r)
	require.NoError(t, err)
	require.Contains(t, string(req.Body), `"model":"mapped"`)
	require.Contains(t, string(req.Body), "provider_extension")
}
func BenchmarkResponsesNativePassThrough(b *testing.B) {
	for _, fast := range []bool{false, true} {
		name := "rebuild"
		if fast {
			name = "prepared"
		}
		b.Run(name, func(b *testing.B) {
			p, r := nativePassThroughFixture(b, 13<<20)
			mw := applyPassThroughRequestBody(p, nil)
			b.ReportAllocs()
			b.SetBytes(int64(len(r.RawRequest.Body)))
			b.ResetTimer()
			for b.Loop() {
				var raw *httpclient.Request
				var err error
				if fast {
					raw, err = p.transformProviderRequest(b.Context(), r)
				} else {
					raw, err = p.wrapped.TransformRequest(b.Context(), r)
				}
				if err == nil {
					_, err = mw.OnOutboundRawRequest(b.Context(), raw)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
