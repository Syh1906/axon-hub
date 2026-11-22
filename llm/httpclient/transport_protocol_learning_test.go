package httpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFirstOutputTrialsRecheckDefaultAndDiscardFailedStreams(t *testing.T) {
	var history protocolHistory
	key := protocolKey{work: ProtocolWorkload{Model: "model-a"}}
	now := time.Now()
	control := func(delay time.Duration) {
		sample := history.selectProtocol(key, now)
		require.False(t, sample.http1)
		history.observeOutput(sample, now.Add(delay), now.Add(delay))
		now = now.Add(delay)
	}
	control(40 * time.Second)
	early := history.selectProtocol(key, now)
	control(35 * time.Second)
	trial := history.selectProtocol(key, now)
	require.True(t, trial.trial)
	history.observeOutput(trial, now.Add(10*time.Second), now.Add(10*time.Second))
	now = now.Add(10 * time.Second)
	history.observeOutput(early, now, now)
	control(12 * time.Second) // The peer also got faster on normal negotiation.
	trial = history.selectProtocol(key, now)
	require.True(t, trial.trial)
	history.observeOutput(trial, now.Add(10*time.Second), now.Add(10*time.Second))
	require.False(t, history.selectProtocol(key, now.Add(11*time.Second)).http1)
	now = now.Add(2 * protocolWindow)
	control(30 * time.Second)
	control(30 * time.Second)
	trial = history.selectProtocol(key, now)
	require.True(t, trial.http1)
	history.observeOutput(trial, time.Time{}, now)
	require.False(t, history.selectProtocol(key, now).http1)
}

func TestFirstOutputProtocolKeyIsolation(t *testing.T) {
	scope := protocolScope{channel: 20, work: ProtocolWorkload{Model: "model-a", Effort: "high"}, varyHeaders: []string{"X-Channel-Account"}}
	key := func(scope protocolScope, account, requestID string) protocolKey {
		ctx := context.WithValue(t.Context(), protocolScopeKey{}, scope)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.invalid/chat", bytes.NewReader([]byte("{}")))
		require.NoError(t, err)
		req.Header.Set("X-Channel-Account", account)
		req.Header.Set("X-Request-Id", requestID)
		k, ok := uploadProtocolKey(req, &http.Transport{}, false)
		require.True(t, ok)
		return k
	}
	base := key(scope, "a", "request-1")
	require.Equal(t, base, key(scope, "a", "request-2"))
	require.NotEqual(t, base, key(scope, "b", "request-1"))
	scope.work.Effort = "low"
	require.NotEqual(t, base, key(scope, "a", "request-1"))
}

func TestFirstOutputWithinOneClockTick(t *testing.T) {
	var history protocolHistory
	key := protocolKey{work: ProtocolWorkload{Model: "fast-peer"}}
	now := time.Now()
	baseline := history.selectProtocol(key, now)
	history.observeOutput(baseline, now, now)
	require.True(t, baseline.state.cooldown)
	now = now.Add(2 * protocolWindow)
	for range 2 {
		sample := history.selectProtocol(key, now)
		now = now.Add(time.Second)
		history.observeOutput(sample, now, now)
	}
	trial := history.selectProtocol(key, now)
	require.True(t, trial.trial)
	history.observeOutput(trial, trial.start, now)
	require.Equal(t, 1, trial.state.wins)
	require.False(t, trial.state.cooldown)
}

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

func TestProtocolPreferencesAndHTTP2FailureFallback(t *testing.T) {
	for _, afterHeaders := range []bool{false, true} {
		var calls atomic.Int32
		server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
			n := calls.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			if n == 1 {
				if afterHeaders {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				panic(http.ErrAbortHandler) // The HTTP/2 server sends RST_STREAM(INTERNAL_ERROR).
			}
			_, _ = io.WriteString(w, r.Proto)
		})
		call := func(mode ProtocolPreference, path string, pinned bool) (*Response, error) {
			r := &Request{URL: server.URL + path, Method: http.MethodPost, Body: []byte("{}"),
				LearnHTTP1: true, ProtocolPreference: mode, EncodingScope: 20}
			if pinned {
				r.HTTP1Endpoints = []string{r.URL}
			}
			return client.Do(t.Context(), r)
		}
		_, err := call(ProtocolPreferHTTP2, "/responses", false)
		require.Error(t, err)
		require.EqualValues(t, 1, calls.Load(), "an accepted POST must not be replayed")
		next, err := call(ProtocolPreferHTTP2, "/responses", false)
		require.NoError(t, err)
		require.Equal(t, "HTTP/1.1", string(next.Body))
		other, err := call(ProtocolPreferHTTP2, "/chat", false)
		require.NoError(t, err)
		require.Equal(t, "HTTP/2.0", string(other.Body))
		forced, err := call(ProtocolHTTP1, "/chat", false)
		require.NoError(t, err)
		require.Equal(t, "HTTP/1.1", string(forced.Body))
		pin, err := call(ProtocolPreferHTTP2, "/chat", true)
		require.NoError(t, err)
		require.Equal(t, "HTTP/1.1", string(pin.Body))
	}
}

func TestCostlyUploadCanTrialAboveOldBandwidthThreshold(t *testing.T) {
	var history protocolHistory
	var key protocolKey
	now := time.Now()
	for range 2 {
		u := &poolUpload{ctx: t.Context(), bytes: 32 << 20, connected: now, uploaded: now.Add(8 * time.Second)}
		history.observe(history.selectProtocol(key, now), u, &http.Response{ProtoMajor: 2, StatusCode: 200}, nil, u.uploaded)
		now = u.uploaded
	}
	require.True(t, history.selectProtocol(key, now).trial)
}

func TestHTTP2FailureFallbackSharesHTTP1ConcurrencyLimit(t *testing.T) {
	var calls atomic.Int32
	server, client := poolTestServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			panic(http.ErrAbortHandler)
		}
		_, _ = io.WriteString(w, "ok")
	})
	send := func(preference ProtocolPreference) (*http.Response, error) {
		req := &Request{URL: server.URL, Method: http.MethodPost, Body: []byte("{}"), LearnHTTP1: true, ProtocolPreference: preference}
		raw, err := client.BuildHttpRequest(t.Context(), req)
		require.NoError(t, err)
		return client.GetNativeClient().Do(traceHTTPRequest(req, raw))
	}
	_, err := send(ProtocolPreferHTTP2)
	require.Error(t, err)
	for _, expectation := range []struct {
		mode     ProtocolPreference
		protocol int
	}{{ProtocolAuto, 1}, {ProtocolPreferHTTP2, 1}, {ProtocolAuto, 2}, {ProtocolHTTP1, 1}} {
		resp, err := send(expectation.mode)
		require.NoError(t, err)
		// Keep each response open so the next request sees all earlier reservations.
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, expectation.protocol, resp.ProtoMajor)
	}
	require.EqualValues(t, 5, calls.Load())
}

func TestHTTP2FallbackRecognizesFrameMismatch(t *testing.T) {
	require.True(t, isHTTP2ProtocolFailure(errors.New("http2: frame too large")))
	require.True(t, isHTTP2ProtocolFailure(errors.New("http2: failed reading the frame payload: unexpected EOF, note that the frame header looked like an HTTP/1.1 header")))
	require.True(t, isHTTP2ProtocolFailure(errors.New("connection error: FRAME_SIZE_ERROR")))
	require.False(t, isHTTP2ProtocolFailure(io.ErrUnexpectedEOF))
	require.False(t, isHTTP2ProtocolFailure(context.DeadlineExceeded))
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
		state.controls, state.baseline = 2, float64(4*time.Second)/minProtocolUpload
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
	state.controls = 2
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
