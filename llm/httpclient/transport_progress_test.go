package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExpectContinueConfirmsOnlyTransmittedBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reject" {
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := NewHttpClient()
	defer client.GetNativeClient().CloseIdleConnections()
	for _, encoded := range []bool{false, true} {
		for _, path := range []string{"/reject", "/accept"} {
			r := &Request{URL: server.URL + path, Method: http.MethodPost, Headers: http.Header{"Expect": {"100-continue"}},
				ContentType: "application/json", Body: bytes.Repeat([]byte(" "), minZstdRequestBytes)}
			if encoded {
				r.ZstdEndpoints = []string{r.URL}
			}
			_, err := client.Do(t.Context(), r)
			require.Equal(t, path == "/reject", err != nil)
			require.Equal(t, encoded, r.TransportTrace.Snapshot().ZstdEncoded)
			require.Equal(t, path == "/accept", r.TransportTrace.UploadedBody(), "path=%s encoded=%v", path, encoded)
		}
	}
}

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

func TestTransportTraceDoesNotCarryRecoveryAcrossRedirectOrigins(t *testing.T) {
	trace := new(TransportTrace)
	trace.beginPoolAttempt(1, func() (string, func()) { return "pool_replaced", nil }, func() bool { return true })
	trace.record(TransportUploaded, time.Now())
	trace.record(TransportHeaders, time.Now())
	trace.beginPoolAttempt(2, func() (string, func()) { return "pool_replaced", nil }, nil)
	got := trace.Snapshot()
	require.EqualValues(t, 2, got.PoolGeneration)
	require.False(t, got.UploadCompleted)
	require.Empty(t, got.PoolRecovery, "a redirect must not attribute the previous origin's upload verification to its new pool")
}

func TestConnectionIdentityThroughHTTPSProxyTLS(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	owned := &identifiedConn{Conn: client, id: 42}
	proxyTLS := tls.Client(owned, &tls.Config{ServerName: "proxy.example"})
	upstreamTLS := tls.Client(proxyTLS, &tls.Config{ServerName: "upstream.example"})
	id, _ := connectionIdentity(upstreamTLS)
	require.EqualValues(t, 42, id)
}
