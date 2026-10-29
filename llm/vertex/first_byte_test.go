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
