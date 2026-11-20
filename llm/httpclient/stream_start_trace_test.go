package httpclient

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamStartTraceDistinguishesHeartbeatFromEvent(t *testing.T) {
	heartbeat := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, ": idle\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-heartbeat:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "event: response.created\ndata: {}\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	trace := new(TransportTrace)
	stream, err := NewHttpClient().DoStream(ctx, &Request{URL: server.URL, Method: http.MethodGet, TransportTrace: trace})
	require.NoError(t, err)
	defer stream.Close()
	finished := make(chan bool, 1)
	go func() { finished <- stream.Next() }()
	require.Eventually(t, func() bool { return trace.Snapshot().StreamStart.LastByteBeforeEvent != nil }, time.Second, 5*time.Millisecond)
	progress := trace.Snapshot().StreamStart
	require.Equal(t, 200, progress.Status)
	require.Equal(t, "text/event-stream", progress.MediaType)
	require.Zero(t, progress.DecodedEvents)
	close(heartbeat)
	require.True(t, <-finished)
	require.Equal(t, "response.created", stream.Current().Type)
	require.False(t, stream.Next())
	progress = trace.Snapshot().StreamStart
	require.EqualValues(t, 1, progress.DecodedEvents)
	require.Equal(t, "eof", progress.DecoderEnd)
}
