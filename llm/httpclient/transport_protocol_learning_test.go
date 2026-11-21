package httpclient

import (
	"bytes"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUploadProtocolEvidence(t *testing.T) {
	var h protocolHistory
	var key protocolKey
	now := time.Now()
	report := func(s protocolSample, version, code int, connect, write time.Duration) {
		t.Helper()
		u := &poolUpload{ctx: t.Context(), bytes: minProtocolUpload, connected: now.Add(connect), uploaded: now.Add(connect + write)}
		h.observe(s, u, &http.Response{ProtoMajor: version, StatusCode: code}, nil, now.Add(connect+write+time.Minute))
	}
	pick := func() protocolSample { return h.selectProtocol(key, now) }
	report(pick(), 2, 200, 0, time.Millisecond)
	report(pick(), 2, 200, 0, 4*time.Second)
	require.False(t, pick().http1)
	report(pick(), 2, 403, 0, 4*time.Second)
	require.False(t, pick().http1)
	report(pick(), 2, 200, 0, 4*time.Second)
	first := pick()
	require.True(t, first.trial)
	require.False(t, pick().http1)
	report(first, 1, 200, time.Millisecond, time.Second)
	second := pick()
	require.True(t, second.trial)
	report(second, 1, 200, time.Millisecond, time.Second)
	preferred := pick()
	require.True(t, preferred.http1)
	require.False(t, preferred.trial)
	report(preferred, 1, 200, 4*time.Second, time.Second)
	require.False(t, pick().http1)
	now = now.Add(2 * protocolWindow)
	fresh := pick()
	require.NotSame(t, preferred.state, fresh.state)
	require.False(t, fresh.http1)
}

func TestLearnedHTTP1UsesNaturalRequestsAndLimitsConcurrency(t *testing.T) {
	var calls atomic.Int32
	held, release := make(chan struct{}, 2), make(chan struct{})
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Header.Get("X-Hold") != "" {
			held <- struct{}{}
			<-release
		}
		_, _ = io.WriteString(w, r.Proto)
	})
	defer close(release)
	tr := client.client.Transport.(*recoveringTransport)
	body := bytes.Repeat([]byte("x"), minProtocolUpload)
	call := func(hold bool, channel int) (*Response, error) {
		req := &Request{Method: http.MethodPost, URL: server.URL, Body: body, LearnHTTP1: true, EncodingScope: channel, Headers: http.Header{}}
		if hold {
			req.Headers.Set("X-Hold", "1")
		}
		return client.Do(t.Context(), req)
	}
	first, err := call(false, 1)
	require.NoError(t, err)
	require.Equal(t, "HTTP/2.0", string(first.Body))
	require.Len(t, tr.protocols, 1)
	for _, state := range tr.protocols {
		state.slow, state.baseline = 2, float64(4*time.Second)/minProtocolUpload
	}
	for range 2 {
		got, err := call(false, 1)
		require.NoError(t, err)
		require.Equal(t, "HTTP/1.1", string(got.Body))
	}
	other, err := call(false, 2)
	require.NoError(t, err)
	require.Equal(t, "HTTP/2.0", string(other.Body))
	run := func() error { _, err := call(true, 1); return err }
	a, b := poolTestAsync(run), poolTestAsync(run)
	poolTestWait(t, held)
	poolTestWait(t, held)
	overflow, err := call(false, 1)
	require.NoError(t, err)
	require.Equal(t, "HTTP/2.0", string(overflow.Body))
	require.EqualValues(t, 7, calls.Load())
	release <- struct{}{}
	release <- struct{}{}
	require.NoError(t, poolTestWait(t, a))
	require.NoError(t, poolTestWait(t, b))
}

func TestUploadTimeoutBeforeConnectionRevokesLearnedHTTP1(t *testing.T) {
	now := time.Now()
	var history protocolHistory
	key := protocolKey{}
	state := history.selectProtocol(key, now).state
	state.slow = 2
	trial := history.selectProtocol(key, now)
	trace := new(TransportTrace)
	trace.beginPoolAttempt(1, func() (string, func()) {
		t.Fatal("no connection was acquired to replace")
		return "", nil
	}, nil)
	trace.rejectProtocol = func() { history.reject(trial, now) }
	req := &Request{TransportTrace: trace}
	req.RecoverHTTPUpload()
	require.False(t, history.selectProtocol(key, now).http1)
}

// A real HTTP/2 peer holds its receive window closed before consuming a large
// body. This exercises the production upload hooks without seeding history.
func TestSlowHTTP2UploadsSelectHTTP1EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("uses real flow-control delays")
	}
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 {
			time.Sleep(7 * time.Second)
		}
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil || n != 2*minProtocolUpload {
			http.Error(w, "incomplete request body", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, r.Proto)
	})
	body := bytes.Repeat([]byte("x"), 2*minProtocolUpload)
	for _, protocol := range []string{"HTTP/2.0", "HTTP/2.0", "HTTP/1.1", "HTTP/1.1", "HTTP/1.1"} {
		req := &Request{Method: http.MethodPost, URL: server.URL, Body: body, LearnHTTP1: true}
		got, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, protocol, string(got.Body))
	}
}
