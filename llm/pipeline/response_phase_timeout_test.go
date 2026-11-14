package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/stretchr/testify/require"
)

func TestHTTPUploadDoesNotConsumeResponseBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	request := &httpclient.Request{Method: http.MethodPost, URL: server.URL, Body: bytes.Repeat([]byte("x"), 16<<20), TransportTrace: new(httpclient.TransportTrace)}
	parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx, guard := newResponsePhaseGuard(parent, request, 300*time.Millisecond, time.Second, ErrNonStreamResponseTimeout)
	response, err := httpclient.NewHttpClient().Do(ctx, request)
	err = guard.finishBeforeFirstEvent(err)
	require.NoError(t, err)
	require.Equal(t, `{"ok":true}`, string(response.Body))
	trace := request.TransportTrace.Snapshot()
	require.True(t, trace.UploadCompleted)
	require.NotNil(t, trace.UploadMs)
	require.NotNil(t, trace.FirstByteWaitMs)
	require.GreaterOrEqual(t, *trace.UploadMs, int64(100))
	require.GreaterOrEqual(t, *trace.FirstByteWaitMs, int64(100))
}

func TestUploadTimeoutRecoversPoolWithoutReplayingRequest(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", h2), func(t *testing.T) {
			finish := make(chan struct{})
			var calls atomic.Int64
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/blocked" {
					select {
					case <-finish:
					case <-r.Context().Done():
					}
					return
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			defer close(finish)
			client := httpclient.NewHttpClientWithProxy(&httpclient.ProxyConfig{Type: httpclient.ProxyTypeDisabled}, httpclient.WithInsecureSkipVerify(true))
			defer client.CloseIdleConnections()
			request := &httpclient.Request{Method: http.MethodPost, URL: server.URL + "/blocked", Body: bytes.Repeat([]byte("x"), 16<<20)}
			parent, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			ctx, guard := newResponsePhaseGuard(parent, request, time.Second, 250*time.Millisecond, ErrNonStreamResponseTimeout)
			_, err := client.Do(ctx, request)
			require.ErrorIs(t, guard.finishBeforeFirstEvent(err), ErrRequestUploadTimeout)
			before := request.TransportTrace.Snapshot()
			require.Equal(t, "pool_replaced", before.PoolRecovery)
			require.False(t, before.UploadCompleted)
			require.EqualValues(t, 1, calls.Load(), "connection recovery must not replay the failed POST")
			next := &httpclient.Request{Method: http.MethodGet, URL: server.URL}
			_, err = client.Do(parent, next)
			require.NoError(t, err)
			after := next.TransportTrace.Snapshot()
			require.NotEqual(t, before.PoolGeneration, after.PoolGeneration)
			require.NotEqual(t, before.ConnectionID, after.ConnectionID)
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestRedirectUploadTimeoutRecoversOnlyCurrentOrigin(t *testing.T) {
	finish := make(chan struct{})
	var calls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-finish:
		case <-r.Context().Done():
		}
	}))
	defer target.Close()
	defer close(finish)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := httpclient.NewHttpClientWithProxy(&httpclient.ProxyConfig{Type: httpclient.ProxyTypeDisabled})
	defer client.CloseIdleConnections()
	request := &httpclient.Request{Method: http.MethodPost, URL: source.URL, Body: bytes.Repeat([]byte("x"), 16<<20)}
	ctx, guard := newResponsePhaseGuard(t.Context(), request, 2*time.Second, 250*time.Millisecond, ErrNonStreamResponseTimeout)
	_, err := client.Do(ctx, request)
	require.ErrorIs(t, guard.finishBeforeFirstEvent(err), ErrRequestUploadTimeout)
	require.Equal(t, "pool_replaced", request.TransportTrace.Snapshot().PoolRecovery)
	require.False(t, request.TransportTrace.Snapshot().UploadCompleted)
	require.EqualValues(t, 1, calls.Load())
}

func TestResponsePhaseTimeoutClassification(t *testing.T) {
	for _, tc := range []struct {
		name             string
		upload, response time.Duration
		uploaded         bool
		want             error
	}{
		{"upload", 20 * time.Millisecond, time.Second, false, ErrRequestUploadTimeout},
		{"response", time.Second, 20 * time.Millisecond, true, ErrStreamFirstEventTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &httpclient.Request{}
			ctx, guard := newResponsePhaseGuard(context.Background(), request, tc.response, tc.upload, ErrStreamFirstEventTimeout)
			request.ReportHTTPProgress(httpclient.TransportStarted, time.Now())
			if tc.uploaded {
				request.ReportHTTPProgress(httpclient.TransportUploaded, time.Now())
			}
			<-ctx.Done()
			require.ErrorIs(t, guard.finishBeforeFirstEvent(ctx.Err()), tc.want)
		})
	}
}

func TestHTTPPoolRecoveryExcludesParentCancellationAndResponseTimeout(t *testing.T) {
	for _, parentTimeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("parentTimeout=%t", parentTimeout), func(t *testing.T) {
			finish := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/quick" {
					_, _ = w.Write([]byte(`{"ok":true}`))
					return
				}
				if !parentTimeout {
					_, _ = io.Copy(io.Discard, r.Body)
				}
				select {
				case <-finish:
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(finish)
			client := httpclient.NewHttpClient()
			defer client.CloseIdleConnections()
			parent, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			body := []byte(`{}`)
			if parentTimeout {
				var stop context.CancelFunc
				parent, stop = context.WithTimeout(parent, 150*time.Millisecond)
				defer stop()
				body = bytes.Repeat([]byte("x"), 16<<20)
			}
			request := &httpclient.Request{Method: http.MethodPost, URL: server.URL, Body: body}
			ctx, guard := newResponsePhaseGuard(parent, request, 250*time.Millisecond, time.Second, ErrNonStreamResponseTimeout)
			_, err := client.Do(ctx, request)
			err = guard.finishBeforeFirstEvent(err)
			if parentTimeout {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, err, ErrNonStreamResponseTimeout)
			}
			before := request.TransportTrace.Snapshot()
			require.Empty(t, before.PoolRecovery)
			next := &httpclient.Request{Method: http.MethodGet, URL: server.URL + "/quick"}
			_, err = client.Do(t.Context(), next)
			require.NoError(t, err)
			require.Equal(t, before.PoolGeneration, next.TransportTrace.Snapshot().PoolGeneration)
		})
	}
}

func TestResponsePhaseKeepsParentCancellationAndStopsAfterFirstEvent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	request := &httpclient.Request{}
	ctx, guard := newResponsePhaseGuard(parent, request, time.Second, time.Second, ErrStreamFirstEventTimeout)
	request.ReportHTTPProgress(httpclient.TransportStarted, time.Now())
	cancel()
	require.ErrorIs(t, guard.finishBeforeFirstEvent(ctx.Err()), context.Canceled)
	require.False(t, guard.timedOut())
	request = &httpclient.Request{}
	ctx, guard = newResponsePhaseGuard(context.Background(), request, 20*time.Millisecond, time.Second, ErrStreamFirstEventTimeout)
	request.ReportHTTPProgress(httpclient.TransportStarted, time.Now())
	request.ReportHTTPProgress(httpclient.TransportUploaded, time.Now())
	require.True(t, guard.acceptFirstEvent())
	time.Sleep(40 * time.Millisecond)
	require.NoError(t, ctx.Err())
	guard.cancelStream()
}

func TestHTTP524IsNotReclassifiedAsLocalTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(524) }))
	defer server.Close()
	request := &httpclient.Request{Method: http.MethodPost, URL: server.URL, Body: []byte(`{}`)}
	ctx, guard := newResponsePhaseGuard(context.Background(), request, time.Second, time.Second, ErrStreamFirstEventTimeout)
	_, err := httpclient.NewHttpClient().DoStream(ctx, request)
	err = guard.finishBeforeFirstEvent(err)
	var upstream *httpclient.Error
	require.True(t, errors.As(err, &upstream))
	require.Equal(t, 524, upstream.StatusCode)
	require.NotErrorIs(t, err, ErrStreamFirstEventTimeout)
}

func TestResponsePhaseTimeoutAfterProcessingNotifiesErrorObservers(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		name := "non-stream"
		if aggregate {
			name = "auto-aggregate"
		}
		t.Run(name, func(t *testing.T) {
			outbound := &mockOutbound{
				transformRequest: func(_ context.Context, request *llm.Request) (*httpclient.Request, error) {
					if aggregate {
						request.Stream = new(true)
					}
					return &httpclient.Request{}, nil
				},
				transformStream: func(_ context.Context, _ *httpclient.Request, source streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
					return streams.Map(source, func(*httpclient.StreamEvent) *llm.Response { return &llm.Response{} }), nil
				},
			}
			executor := &mockExecutor{
				do: func(context.Context, *httpclient.Request) (*httpclient.Response, error) {
					return &httpclient.Response{}, nil
				},
				doStream: func(context.Context, *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
					return streams.SliceStream([]*httpclient.StreamEvent{{Data: []byte(`{}`)}}), nil
				},
			}
			var observed []error
			middleware := &simpleMiddleware{
				name: "slow-response-processing",
				inboundRawResponseHandler: func(ctx context.Context, response *httpclient.Response) (*httpclient.Response, error) {
					<-ctx.Done()
					return response, nil
				},
				outboundRawErrorResponseHandler: func(_ context.Context, err error) { observed = append(observed, err) },
			}
			p := NewFactory(executor).Pipeline(&mockInbound{}, outbound, WithResponseTimeouts(0, 20*time.Millisecond), WithMiddlewares(middleware))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := p.Process(ctx, &httpclient.Request{})
			require.ErrorIs(t, err, ErrNonStreamResponseTimeout)
			require.Len(t, observed, 1, "the attempt must be marked failed before retry or finalization")
			require.ErrorIs(t, observed[0], ErrNonStreamResponseTimeout)
		})
	}
}
