package pipeline

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
