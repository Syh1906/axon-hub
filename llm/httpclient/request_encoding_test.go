package httpclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/encodingbudget"
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
	for range encodingbudget.Capacity {
		require.True(t, encodingbudget.TryAcquire())
		defer encodingbudget.Release()
	}
	source := &Request{Method: http.MethodPost, URL: "https://example.test/v1/responses", ContentType: "application/json",
		Body: bytes.Repeat([]byte(" "), minZstdRequestBytes), ZstdEndpoints: []string{"https://example.test/v1/responses"}}
	raw, err := BuildHttpRequest(t.Context(), source)
	require.NoError(t, err)
	raw = traceHTTPRequest(source, raw)
	wire, _ := new(requestEncodingCache).prepare(raw)
	require.Same(t, raw, wire)
}

func TestResponsesLearnsPerResourceAndChannelWithoutReplaying(t *testing.T) {
	type seen struct{ path, coding string }
	got := make(chan seen, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		got <- seen{r.URL.Path, r.Header.Get("Content-Encoding")}
		w.Header().Set("Accept-Encoding", "gzip, zstd;q=0.7")
		if r.URL.Path == "/reject" && r.Header.Get("Content-Encoding") != "" {
			w.WriteHeader(415)
			return
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/other", 307)
			return
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	client := NewHttpClient()
	defer client.GetNativeClient().CloseIdleConnections()
	big := bytes.Repeat([]byte{' '}, minZstdRequestBytes)
	send := func(path string, scope int, body []byte, learn bool, wantErr bool) {
		r := &Request{Method: "POST", URL: server.URL + path, Headers: make(http.Header), ContentType: "application/json", Body: body, LearnZstd: learn, EncodingScope: scope}
		_, err := client.Do(t.Context(), r)
		if wantErr {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	send("/reject", 1, []byte("{}"), true, false)
	require.Equal(t, seen{"/reject", ""}, <-got)
	send("/reject", 2, big, true, false)
	require.Equal(t, seen{"/reject", ""}, <-got)
	send("/reject", 1, big, true, true)
	require.Equal(t, seen{"/reject", "zstd"}, <-got)
	send("/reject", 1, big, true, false)
	require.Equal(t, seen{"/reject", ""}, <-got)
	send("/redirect", 1, big, true, false)
	require.Equal(t, seen{"/redirect", ""}, <-got)
	require.Equal(t, seen{"/other", ""}, <-got)
	send("/other", 1, big, true, false)
	require.Equal(t, seen{"/other", ""}, <-got)
	send("/other", 1, big, false, false)
	require.Equal(t, seen{"/other", ""}, <-got)
	a, b := http.Header{}, http.Header{}
	a.Set("Authorization", "synthetic-account-a")
	b.Set("Authorization", "synthetic-account-b")
	require.NotEqual(t, encodingAccount(a), encodingAccount(b))
	disabled := &Request{Method: "POST", URL: server.URL + "/other", ContentType: "application/json", Body: big, LearnZstd: true, EncodingScope: 1,
		ZstdEndpoints: []string{server.URL + "/other"}, NoZstdEndpoints: []string{server.URL + "/other"}}
	_, err := client.Do(t.Context(), disabled)
	require.NoError(t, err)
	require.Equal(t, seen{"/other", ""}, <-got)
	require.Empty(t, got)
}

func TestEncodingEvidenceUsesUploadSavingsAndExpires(t *testing.T) {
	for _, v := range []string{"*", "zstd;q=0", "zstd;q=1e-1", "zstd;q=NaN", "zstd;q=.5", "zstd;q=1.001", "zstd, zstd;q=0"} {
		require.False(t, acceptsZstd([]string{v}), v)
	}
	require.True(t, acceptsZstd([]string{"gzip", " ZSTD ; q=0.125 "}))
	now := time.Now()
	key := encodingResource{scope: 1}
	a := encodingAttempt{key: key, start: now, size: minZstdRequestBytes}
	cache := &requestEncodingCache{entries: map[encodingResource]encodingEvidence{key: {until: now.Add(time.Minute)}}}
	require.True(t, cache.shouldEncode(&a))
	require.False(t, cache.shouldEncode(&a), "only one simultaneous trial per size class")
	e := cache.entries[key]
	e.costs[0] = encodingCost{savedFraction: .25, cpuPerByte: 4, nextTrial: now.Add(time.Minute), plainAt: now, plainPerByte: 5}
	cache.entries[key] = e
	require.False(t, cache.shouldEncode(&a), "1.25ns saved does not pay 4ns per byte")
	e.costs[0].plainPerByte = 40
	cache.entries[key] = e
	require.True(t, cache.shouldEncode(&a))
	a.start = now.Add(2 * time.Minute)
	require.False(t, cache.shouldEncode(&a))
}

func TestNegotiatedZstdPaysForEncodingOnSlowReceiver(t *testing.T) {
	// Use the same logical content both times. The receiver deliberately delays
	// each read, so only reducing actual HTTP body bytes can remove that delay.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [32 << 10]byte
		for {
			n, err := r.Body.Read(buf[:])
			if n > 0 {
				time.Sleep(3 * time.Millisecond)
			}
			if err != nil {
				break
			}
		}
		w.Header().Set("Accept-Encoding", "zstd")
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	client := NewHttpClient()
	defer client.GetNativeClient().CloseIdleConnections()
	body := append([]byte(`{"input":"`), bytes.Repeat([]byte{'p'}, 2<<20)...)
	body = append(body, '"', '}')
	send := func() (time.Duration, TransportTimings) {
		r := &Request{Method: "POST", URL: server.URL + "/v1/responses", ContentType: "application/json", Body: body, LearnZstd: true}
		start := time.Now()
		_, err := client.Do(t.Context(), r)
		elapsed := time.Since(start)
		require.NoError(t, err)
		return elapsed, r.TransportTrace.Snapshot()
	}
	first, plain := send()
	second, encoded := send()
	require.False(t, plain.ZstdEncoded)
	require.True(t, encoded.ZstdEncoded)
	require.Less(t, encoded.WireBodyBytes, plain.WireBodyBytes)
	require.Less(t, second, first)
	t.Logf("slow receiver: plain %d bytes in %s, negotiated %d bytes in %s", plain.WireBodyBytes, first, encoded.WireBodyBytes, second)
}

func TestEncodingCostUsesOnlyPlainUploadsInSameSizeClass(t *testing.T) {
	now := time.Now()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Accept-Encoding": {"zstd"}}}
	sample := func(n int64, d time.Duration) *TransportTrace {
		return &TransportTrace{wireBytes: n, times: map[TransportPhase]time.Time{TransportConnected: now, TransportUploaded: now.Add(d)}}
	}
	cache := new(requestEncodingCache)
	a := encodingAttempt{size: minZstdRequestBytes, start: now}
	cache.observe(&a, resp, sample(int64(a.size), time.Millisecond))
	require.True(t, cache.shouldEncode(&a))
	a.encoded, a.saved, a.cpu = true, a.size-512, 8*time.Millisecond
	cache.observe(&a, resp, sample(512, 2*time.Millisecond))
	require.False(t, cache.shouldEncode(&a), "a 512-byte write cannot redefine the speed of an uncompressed MiB")
	big := encodingAttempt{size: a.size * 8, start: now}
	cache.observe(&big, resp, sample(int64(big.size), time.Second))
	require.False(t, cache.shouldEncode(&a), "a different size class does not replace the baseline")
	plain := encodingAttempt{size: a.size, start: now}
	cache.observe(&plain, resp, sample(int64(a.size), 50*time.Millisecond))
	require.True(t, cache.shouldEncode(&a))
	withdraw := encodingAttempt{size: a.size, start: now.Add(time.Second)}
	cache.observe(&withdraw, &http.Response{StatusCode: 200, Header: http.Header{"Accept-Encoding": {"identity"}}}, nil)
	cache.observe(&a, resp, nil)
	require.False(t, cache.shouldEncode(&a), "a late older response cannot undo a newer withdrawal")
}

func TestNegotiatedFastLinkKeepsUnprofitableRequestsPlain(t *testing.T) {
	codings := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		codings <- r.Header.Get("Content-Encoding")
		w.Header().Set("Accept-Encoding", "zstd")
	}))
	defer server.Close()
	client := NewHttpClient()
	defer client.GetNativeClient().CloseIdleConnections()
	body := append([]byte(`{"input":"`), bytes.Repeat([]byte{'x'}, minZstdRequestBytes)...)
	body = append(body, '"', '}')
	r := &Request{Method: "POST", URL: server.URL, ContentType: "application/json", Body: body, LearnZstd: true}
	// Mark deterministic upload times on the real write callbacks. The server
	// still receives an actual POST and the real encoder runs on every trial.
	r.OnHTTPProgress = func(phase TransportPhase, at time.Time) {
		if phase == TransportUploaded {
			r.TransportTrace.mu.Lock()
			r.TransportTrace.times[TransportConnected] = at.Add(-time.Microsecond)
			r.TransportTrace.mu.Unlock()
		}
	}
	for _, want := range []string{"", "zstd", ""} {
		_, err := client.Do(t.Context(), r)
		require.NoError(t, err)
		require.Equal(t, want, <-codings)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := client.Do(ctx, r)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, codings)
}

func TestNegotiatedEncodingSeparatesTokenHeaderAndConfigGeneration(t *testing.T) {
	type upload struct{ token, encoding string }
	seen := make(chan upload, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		token := r.Header.Get("X-Api-Token")
		seen <- upload{token, r.Header.Get("Content-Encoding")}
		if token == "fixture-one" {
			w.Header().Set("Accept-Encoding", "zstd")
		}
	}))
	defer server.Close()
	client := NewHttpClient()
	defer client.GetNativeClient().CloseIdleConnections()
	body := bytes.Repeat([]byte(" "), minZstdRequestBytes)
	for _, tc := range []struct {
		token, encoding string
		generation      int64
		body            []byte
	}{
		{"fixture-one", "", 1, []byte("{}")}, {"fixture-two", "", 1, body},
		{"fixture-one", "", 2, body}, {"fixture-one", "zstd", 1, body},
	} {
		r := &Request{Method: "POST", URL: server.URL, ContentType: "application/json", Body: tc.body, LearnZstd: true,
			Headers: http.Header{"X-Api-Token": {tc.token}}, EncodingScope: 5, EncodingGeneration: tc.generation}
		_, err := client.Do(t.Context(), r)
		require.NoError(t, err)
		require.Equal(t, upload{tc.token, tc.encoding}, <-seen)
	}
}

func BenchmarkSmallRequestEncoding(b *testing.B) {
	for _, learn := range []bool{false, true} {
		name := "disabled"
		if learn {
			name = "learning"
		}
		b.Run(name, func(b *testing.B) {
			cache := new(requestEncodingCache)
			r := &Request{Method: "POST", URL: "http://example.invalid/v1/responses", ContentType: "application/json", Body: []byte("{}"), LearnZstd: learn}
			raw, err := BuildHttpRequest(b.Context(), r)
			require.NoError(b, err)
			resp := &http.Response{StatusCode: 200}
			b.ReportAllocs()
			for b.Loop() {
				_, a := cache.prepare(traceHTTPRequest(r, raw))
				if a != nil {
					cache.observe(a, resp, nil)
				}
			}
		})
	}
}
