package httpclient

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

	"github.com/stretchr/testify/require"
)

func TestFirstByteReader(t *testing.T) {
	calls := 0
	reader := &firstByteReader{ReadCloser: io.NopCloser(strings.NewReader("hello")), onFirstByte: func(time.Time) { calls++ }}
	_, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	empty := &firstByteReader{ReadCloser: io.NopCloser(strings.NewReader("")), onFirstByte: func(time.Time) { calls++ }}
	_, err = io.ReadAll(empty)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestHTTPFirstBytePrecedesCompleteSSEEvent(t *testing.T) {
	body, remainder := make(chan struct{}), make(chan struct{})
	var bodyOnce, remainderOnce sync.Once
	releaseBody := func() { bodyOnce.Do(func() { close(body) }) }
	releaseRemainder := func() { remainderOnce.Do(func() { close(remainder) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-body:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "d")
		w.(http.Flusher).Flush()
		select {
		case <-remainder:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "ata: {\"ok\":true}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	defer releaseBody()
	defer releaseRemainder()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	observed := make(chan time.Time, 1)
	stream, err := NewHttpClient().DoStream(ctx, &Request{Method: http.MethodGet, URL: server.URL, OnFirstResponseByte: func(at time.Time) { observed <- at }})
	require.NoError(t, err)
	defer stream.Close()
	select {
	case <-observed:
		t.Fatal("response headers are not body data")
	default:
	}
	finished := make(chan error, 1)
	go func() {
		defer func() {
			if value := recover(); value != nil {
				finished <- fmt.Errorf("stream reader panic: %v", value)
			}
		}()
		if !stream.Next() {
			finished <- fmt.Errorf("stream ended before an event: %v", stream.Err())
			return
		}
		if string(stream.Current().Data) != `{"ok":true}` {
			finished <- fmt.Errorf("unexpected stream data")
			return
		}
		finished <- nil
	}()
	releaseBody()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("first body byte was not observed")
	}
	select {
	case err := <-finished:
		t.Fatalf("event completed before its remainder: %v", err)
	default:
	}
	releaseRemainder()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("event did not complete")
	}
}
