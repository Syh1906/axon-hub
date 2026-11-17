package vertex

import (
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestTokenCountTransformPreservesRequestType(t *testing.T) {
	executor := &Executor{baseURL: "https://vertex.example/", projectID: "fixture", region: "global"}
	req, err := executor.transformRequest(&httpclient.Request{Method: http.MethodPost, URL: "/v1/messages/count_tokens", RequestType: "token_count", APIFormat: "anthropic/count_tokens", Body: []byte(`{"model":"claude","messages":[]}`)})
	require.NoError(t, err)
	require.Equal(t, "https://vertex.example/v1/projects/fixture/locations/global/publishers/anthropic/models/count-tokens:rawPredict", req.URL)
	require.Equal(t, "token_count", req.RequestType)
	require.Equal(t, "anthropic/count_tokens", req.APIFormat)
}
