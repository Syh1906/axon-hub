package api

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

type terminalErrorResponseWriter struct {
	gin.ResponseWriter
	mu  sync.Mutex
	err error
}

func (w *terminalErrorResponseWriter) fail(err error) {
	w.mu.Lock()
	w.err = err
	w.mu.Unlock()
}

func (w *terminalErrorResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	err := w.err
	w.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(data)
}

func (w *terminalErrorResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func TestStreamDeliveryE2E_ResponsesTerminalHasDiagnostics(t *testing.T) {
	for _, heartbeat := range []bool{false, true} {
		name := "ordinary"
		if heartbeat {
			name = "heartbeat"
		}
		for _, tc := range []struct {
			name       string
			err        error
			wantStatus request.Status
			wantCode   string
		}{
			{"canceled", context.Canceled, request.StatusCanceled, "stream_delivery_canceled"},
			{"write_error", io.ErrClosedPipe, request.StatusFailed, "stream_delivery_failed"},
			{"write_timeout", context.DeadlineExceeded, request.StatusFailed, "stream_delivery_timeout"},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				// Cancellation can discard SQLite's sole connection. Keep this test
				// database on disk so reconnecting does not erase the evidence.
				dsn := filepath.ToSlash(filepath.Join(t.TempDir(), "delivery.sqlite")) + "?_fk=0"
				f := newStreamDeliveryE2EFixture(t, llm.APIFormatOpenAIResponse, streamDeliveryResponses(12, 34, "completed"), heartbeat, dsn)
				h := f.handler(t)
				h.ChatCompletionOrchestrator.Inbound = responses.NewInboundTransformer()
				ctx, cancel := context.WithCancel(f.ctx)
				defer cancel()
				terminalObserved := false
				h.StreamWriter = func(c *gin.Context, source streams.Stream[*httpclient.StreamEvent]) {
					writer := &terminalErrorResponseWriter{ResponseWriter: c.Writer}
					c.Writer = writer
					// Fail the real terminal write. Write errors leave the context
					// active so diagnostics cannot reconstruct the error from ctx.Err.
					adapted := streams.Map(source, func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
						if event.Type == "response.completed" {
							terminalObserved = true
							if tc.err == context.Canceled {
								cancel()
							} else {
								writer.fail(tc.err)
							}
						}
						return event
					})
					runDeliverySSE(c, preserveStreamDelivery(adapted, source), heartbeat)
				}
				engine := gin.New()
				engine.Use(f.testContext)
				engine.POST("/v1/responses", h.ChatCompletion)
				recorder := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","input":"hello","stream":true}`)).WithContext(ctx)
				req.Header.Set("Content-Type", "application/json")
				engine.ServeHTTP(recorder, req)
				require.True(t, terminalObserved)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.NotContains(t, recorder.Body.String(), "response.completed")
				saved := f.client.Request.Query().OnlyX(f.ctx)
				require.Equal(t, tc.wantStatus, saved.Status)
				require.NotNil(t, saved.Failure)
				require.Equal(t, "delivery", saved.Failure.Stage)
				require.Equal(t, tc.wantCode, saved.Failure.Code)
				require.Contains(t, saved.Failure.Message, tc.err.Error())
				exec := f.client.RequestExecution.Query().OnlyX(f.ctx)
				require.Equal(t, requestexecution.StatusCompleted, exec.Status)
				require.Nil(t, exec.Failure)
				usage := f.client.UsageLog.Query().AllX(f.ctx)
				require.Len(t, usage, 1)
				require.EqualValues(t, 34, usage[0].CompletionTokens)
			})
		}
	}
}

func assertStreamDeliveryWire(t *testing.T, response *http.Response, heartbeat bool) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Contains(t, response.Header.Get("Content-Type"), "text/event-stream")
	require.NotEmpty(t, body)

	text := string(body)
	require.Contains(t, text, "data:")
	require.Contains(t, text, "[DONE]")
	if heartbeat {
		t.Log("heartbeat mode exercised; heartbeat framing is covered by TestStreamDeliverySSELoopback")
	}
	return text
}

func TestStreamDeliveryE2E_PostTerminalDisconnectPersistsCompletion(t *testing.T) {
	fixture := newStreamDeliveryE2EFixture(t, llm.APIFormatOpenAIChatCompletion, streamDeliveryChat(12, 34, true), false)
	h := fixture.handler(t)
	engine := gin.New()
	engine.Use(fixture.testContext)
	done := make(chan struct{})
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		defer close(done)
		h.ChatCompletion(c)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	server.Client().Timeout = 5 * time.Second

	req, err := http.NewRequestWithContext(fixture.ctx, http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(req)
	require.NoError(t, err)
	reader := bufio.NewReader(response.Body)
	var wire strings.Builder
	for !strings.Contains(wire.String(), "[DONE]") {
		line, readErr := reader.ReadString('\n')
		wire.WriteString(line)
		require.NoError(t, readErr)
	}
	require.NoError(t, response.Body.Close())
	require.Contains(t, wire.String(), "data:")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not finalize after disconnect")
	}
	fixture.assertDatabase(t, request.StatusCompleted, true)
}
