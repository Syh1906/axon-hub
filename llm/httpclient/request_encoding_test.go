package httpclient

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestConfiguredResponsesEncodingUsesOnePostAndPreservesJSON(t *testing.T) {
	body := append([]byte(`{"input":"`), bytes.Repeat([]byte("a"), minZstdRequestBytes)...)
	body = append(body, '"', '}')
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path == "/plain" {
			if r.Header.Get("Content-Encoding") != "" || !bytes.Equal(body, wire) {
				t.Error("unexpected encoding at unconfigured endpoint")
			}
		} else {
			if r.Header.Get("Content-Encoding") != "zstd" || r.ContentLength != int64(len(wire)) || len(wire) >= len(body) {
				t.Error("expected smaller zstd frame")
			}
			decoder, err := zstd.NewReader(nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer decoder.Close()
			decoded, err := decoder.DecodeAll(wire, nil)
			if err != nil || !bytes.Equal(decoded, body) {
				t.Error("wire request changed model input")
			}
		}
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/plain", http.StatusTemporaryRedirect)
		case "/reject":
			w.WriteHeader(http.StatusUnsupportedMediaType)
		default:
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer server.Close()
	client := NewHttpClient()
	defer client.GetNativeClient().CloseIdleConnections()
	for _, path := range []string{"/responses", "/plain", "/reject", "/redirect"} {
		r := &Request{Method: "POST", URL: server.URL + path, Headers: make(http.Header), Body: body, ContentType: "application/json",
			ZstdEndpoints: []string{server.URL + "/responses", server.URL + "/reject", server.URL + "/redirect"}}
		before := calls.Load()
		_, err := client.Do(t.Context(), r)
		if path == "/reject" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
		want := int32(1)
		if path == "/redirect" {
			want = 2
		}
		require.Equal(t, want, calls.Load()-before)
		require.Empty(t, r.Headers.Get("Content-Encoding"))
		require.Equal(t, body, r.Body)
		require.Equal(t, path == "/responses" || path == "/reject", r.TransportTrace.Snapshot().ZstdEncoded)
	}
}

func TestConfiguredEncodingDoesNotQueueWhenBusy(t *testing.T) {
	for range cap(requestEncodingSlots) {
		requestEncodingSlots <- struct{}{}
		defer func() { <-requestEncodingSlots }()
	}
	source := &Request{Method: http.MethodPost, URL: "https://example.test/v1/responses", ContentType: "application/json",
		Body: bytes.Repeat([]byte(" "), minZstdRequestBytes), ZstdEndpoints: []string{"https://example.test/v1/responses"}}
	raw, err := BuildHttpRequest(t.Context(), source)
	require.NoError(t, err)
	raw = traceHTTPRequest(source, raw)
	require.Same(t, raw, encodeConfiguredRequest(raw))
}
