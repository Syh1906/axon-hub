package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPTransportTraceIncludesErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(524); _, _ = w.Write([]byte("timeout")) }))
	defer server.Close()
	request := &Request{Method: http.MethodPost, URL: server.URL, Body: []byte(`{}`), TransportTrace: new(TransportTrace)}
	_, err := NewHttpClient().DoStream(context.Background(), request)
	require.Error(t, err)
	got := request.TransportTrace.Snapshot()
	require.True(t, got.UploadCompleted)
	require.NotNil(t, got.ConnectMs)
	require.NotNil(t, got.FirstByteWaitMs)
	require.Nil(t, got.FirstEventWaitMs, "an HTTP error body is not a model stream event")
}

func TestTransportTraceUnknownAndEarlyResponseAreNotZero(t *testing.T) {
	trace := new(TransportTrace)
	require.Equal(t, TransportTimings{}, trace.Snapshot())
	now := time.Now()
	trace.record(TransportStarted, now)
	trace.record(TransportHeaders, now.Add(time.Millisecond))
	require.Nil(t, trace.Snapshot().HeadersWaitMs)
	trace.record(TransportUploaded, now.Add(2*time.Millisecond))
	require.Nil(t, trace.Snapshot().HeadersWaitMs, "response can arrive before upload completes")
}
