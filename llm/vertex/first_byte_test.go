package vertex

import (
	"net/http"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
)

func TestTransformRequestPreservesFirstByteObserver(t *testing.T) {
	executor := &Executor{region: "us-central1", projectID: "test-project", baseURL: "https://example.test/"}
	observed := time.Time{}
	request := &httpclient.Request{Method: http.MethodPost, URL: "/v1/messages", Body: []byte(`{"model":"claude-test","stream":true}`),
		OnFirstResponseByte: func(at time.Time) { observed = at }}
	transformed, err := executor.transformRequest(request)
	require.NoError(t, err)
	require.NotNil(t, transformed.OnFirstResponseByte)
	at := time.Now()
	transformed.OnFirstResponseByte(at)
	require.Equal(t, at, observed)
}

func TestTransformRequestPreservesHTTPProgress(t *testing.T) {
	executor := &Executor{region: "us-central1", projectID: "test-project", baseURL: "https://example.test/"}
	var phases []httpclient.TransportPhase
	request := &httpclient.Request{Method: http.MethodPost, URL: "/v1/messages", Body: []byte(`{"model":"claude-test"}`),
		TransportTrace: new(httpclient.TransportTrace),
		OnHTTPProgress: func(phase httpclient.TransportPhase, _ time.Time) { phases = append(phases, phase) },
	}
	transformed, err := executor.transformRequest(request)
	require.NoError(t, err)
	now := time.Now()
	transformed.ReportHTTPProgress(httpclient.TransportStarted, now)
	transformed.ReportHTTPProgress(httpclient.TransportConnected, now.Add(time.Millisecond))
	transformed.ReportHTTPProgress(httpclient.TransportUploaded, now.Add(10*time.Millisecond))
	require.Equal(t, []httpclient.TransportPhase{httpclient.TransportStarted, httpclient.TransportConnected, httpclient.TransportUploaded}, phases)
	trace := request.TransportTrace.Snapshot()
	require.True(t, trace.HTTPObserved)
	require.True(t, trace.UploadCompleted)
	require.Equal(t, int64(9), *trace.UploadMs)
}
