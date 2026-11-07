package biz

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestRequestPayloadStorageLimitsAndRoundTrip(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:payloads?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	svc := newResponsesSessionRequestService(t, client)
	require.NoError(t, svc.SystemService.SetStoragePolicy(ctx, &StoragePolicy{StoreRequestBody: true, StoreResponseBody: true, StoreChunks: true}))
	body := []byte(`{"model":"example","input":"hello"}`)
	req, err := svc.CreateRequest(ctx, &llm.Request{Model: "example"}, &httpclient.Request{Body: body}, llm.APIFormatOpenAIResponse)
	require.NoError(t, err)
	ref, managed := parsePayloadMarker(req.RequestBody)
	require.True(t, managed)
	require.Equal(t, "cached", ref.State)
	loaded, err := svc.LoadRequestBody(ctx, req)
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(loaded))
	response := []byte(`{"id":"response-1","status":"completed","output":[]}`)
	require.NoError(t, svc.UpdateRequestFinalized(ctx, req.ID, request.StatusCompleted, "response-1", response, nil))
	req = client.Request.GetX(ctx, req.ID)
	require.Less(t, len(req.ResponseBody), 512)
	loaded, err = svc.LoadResponseBody(ctx, req)
	require.NoError(t, err)
	require.JSONEq(t, string(response), string(loaded))
	// Expiration metadata remains visible without presenting it as model output.
	ref.ExpiresAt = time.Now().Add(-time.Second)
	req.RequestBody = payloadMarker(ref)
	loaded, err = svc.LoadRequestBody(ctx, req)
	require.NoError(t, err)
	expired, ok := parsePayloadMarker(loaded)
	require.True(t, ok)
	require.Equal(t, "expired", expired.State)
	require.Empty(t, expired.Key)
	large, err := json.Marshal(map[string]string{"input": strings.Repeat("x", debugPayloadMaxBytes)})
	require.NoError(t, err)
	largeReq, err := svc.CreateRequest(ctx, &llm.Request{Model: "example"}, &httpclient.Request{Body: large}, llm.APIFormatOpenAIResponse)
	require.NoError(t, err, "debug size limits must not reject a valid proxy request")
	largeRef, ok := parsePayloadMarker(largeReq.RequestBody)
	require.True(t, ok)
	require.Equal(t, "too_large", largeRef.State)
	require.Less(t, len(largeReq.RequestBody), 256)
	require.NoError(t, svc.SaveRequestChunks(ctx, req.ID, []*httpclient.StreamEvent{{Type: "response.completed", Data: []byte(`{"type":"response.completed"}`)}}))
	req = client.Request.GetX(ctx, req.ID)
	_, managed = parsePayloadMarker(req.ResponseChunks[0])
	require.True(t, managed)
	chunks, err := svc.LoadResponseChunks(ctx, req)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Contains(t, string(chunks[0]), "response.completed")
}

func TestRequestPayloadDisabledDoesNotWriteBodiesOrContext(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:disabled_payloads?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	ctx = contexts.WithAPIKey(contexts.WithProjectID(ctx, 1), &ent.APIKey{ID: 2})
	svc := newResponsesSessionRequestService(t, client)
	dir := filepath.Join(t.TempDir(), "payloads")
	svc.PayloadService = NewRequestPayloadService(RequestPayloadConfig{Directory: dir})
	require.NoError(t, svc.SystemService.SetStoragePolicy(ctx, &StoragePolicy{}))
	input := []byte(`{"model":"example","input":"private input"}`)
	output := []byte(`{"id":"response-disabled","status":"completed","output":[{"type":"message","content":"private output"}]}`)
	req, err := svc.CreateRequest(ctx, &llm.Request{Model: "example"}, &httpclient.Request{Body: input}, llm.APIFormatOpenAIResponse)
	require.NoError(t, err)
	require.NoError(t, svc.UpdateRequestFinalized(ctx, req.ID, request.StatusCompleted, "response-disabled", output, nil))
	svc.SaveResponsesContext(ctx, input, output)
	svc.SaveResponsesContext(ctx, input, []byte(`{"id":"response-interrupted-disabled","status":"incomplete","incomplete_details":{"reason":"interrupted"},"output":[]}`))
	req = client.Request.GetX(ctx, req.ID)
	for _, body := range [][]byte{req.RequestBody, req.ResponseBody} {
		ref, ok := parsePayloadMarker(body)
		require.True(t, ok)
		require.Equal(t, "disabled", ref.State)
		require.NotContains(t, string(body), "private")
	}
	_, err = os.Stat(dir)
	require.True(t, os.IsNotExist(err), "disabled storage must not even create a payload directory")
}

func TestResponsesContextIndependentOfDebugAndScopedAcrossRestart(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:independent_context?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	ctx = contexts.WithAPIKey(contexts.WithProjectID(ctx, 1), &ent.APIKey{ID: 2})
	svc := newResponsesSessionRequestService(t, client)
	dir := t.TempDir()
	svc.PayloadService = NewRequestPayloadService(RequestPayloadConfig{Directory: dir})
	require.NoError(t, svc.SystemService.SetStoragePolicy(ctx, &StoragePolicy{StoreRequestBody: true, StoreResponseBody: true}))
	input := []byte(`{"input":"hello","model":"not-needed-for-restoration"}`)
	output := []byte(`{"id":"response-context","status":"completed","output":[{"type":"message","content":"world"}]}`)
	svc.SaveResponsesContext(ctx, input, output)
	// There is no debug copy or request row: continuation has its own storage.
	svc.PayloadService = NewRequestPayloadService(RequestPayloadConfig{Directory: dir})
	loadedInput, loadedOutput, found, err := svc.LoadCompletedResponsesSession(ctx, "response-context")
	require.NoError(t, err)
	require.True(t, found)
	require.JSONEq(t, `{"input":"hello"}`, string(loadedInput))
	require.JSONEq(t, string(output), string(loadedOutput))
	for _, other := range []context.Context{
		contexts.WithAPIKey(ctx, &ent.APIKey{ID: 3}),
		contexts.WithProjectID(ctx, 4),
	} {
		_, _, found, err := svc.LoadCompletedResponsesSession(other, "response-context")
		require.NoError(t, err)
		require.False(t, found)
	}
}

func TestResponsesInterruptedContextSurvivesRestartWithoutRetainingFailures(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:interrupted_context?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	ctx = contexts.WithAPIKey(contexts.WithProjectID(ctx, 1), &ent.APIKey{ID: 2})
	svc := newResponsesSessionRequestService(t, client)
	dir := t.TempDir()
	svc.PayloadService = NewRequestPayloadService(RequestPayloadConfig{Directory: dir})
	require.NoError(t, svc.SystemService.SetStoragePolicy(ctx, &StoragePolicy{StoreRequestBody: true, StoreResponseBody: true}))
	input := []byte(`{"input":"first"}`)
	output := []byte(`{"id":"response-interrupted","status":"incomplete","incomplete_details":{"reason":"interrupted"},"output":[{"id":"rs_1","type":"reasoning","encrypted_content":"complete-item"}]}`)
	svc.SaveResponsesContext(ctx, input, output)
	for _, rejected := range []string{
		`{"id":"failed","status":"failed","output":[]}`,
		`{"id":"incomplete","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
	} {
		svc.SaveResponsesContext(ctx, input, []byte(rejected))
	}
	svc.PayloadService = NewRequestPayloadService(RequestPayloadConfig{Directory: dir})
	loadedInput, loadedOutput, found, err := svc.LoadCompletedResponsesSession(ctx, "response-interrupted")
	require.NoError(t, err)
	require.True(t, found)
	require.JSONEq(t, string(input), string(loadedInput))
	require.JSONEq(t, string(output), string(loadedOutput))
	for _, id := range []string{"failed", "incomplete"} {
		_, _, found := svc.loadResponsesContext(ctx, id)
		require.False(t, found)
	}
}
