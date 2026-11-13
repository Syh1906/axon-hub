package orchestrator

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

func TestRequestImageOptimizationPreservesPassThroughAndStoredAttempt(t *testing.T) {
	ctx, client := setupTest(t)
	channel := createTestChannel(t, ctx, client)
	channel.Settings = &objects.ChannelSettings{PassThroughBody: new(true)}
	executor := &sequenceExecutor{steps: []executorStep{{resp: healthGateResponse()}}}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, channel)
	var original bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&original, image.NewNRGBA(image.Rect(0, 0, 256, 256))))
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(original.Bytes())
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":[{"type":"text","text":"keep exactly"},{"type":"image_url","image_url":{"url":"` + url + `"}}]}],"stream":false,"provider_option":{"keep":true}}`)
	_, err := processor.Process(processCtx, &httpclient.Request{Method: http.MethodPost, Headers: http.Header{"Content-Type": {"application/json"}}, Body: body})
	require.NoError(t, err)
	require.Len(t, executor.requests, 1)
	sent := executor.requests[0]
	require.Less(t, len(sent.Body), len(body))
	require.True(t, gjson.GetBytes(sent.Body, "provider_option.keep").Bool())
	require.Equal(t, "keep exactly", gjson.GetBytes(sent.Body, "messages.0.content.0.text").String())
	execution := client.RequestExecution.Query().OnlyX(ctx)
	require.Equal(t, int64(len(sent.Body)), gjson.GetBytes(execution.RequestBody, "_axon_payload.size").Int())
	require.True(t, execution.PassThroughApplied)
	require.NotNil(t, sent.TransportTrace)
}

func TestRequestImageOptimizerSkipsImageEditingAndOpaqueBodies(t *testing.T) {
	state := &PersistenceState{Request: &ent.Request{ProjectID: 1}, RequestService: &biz.RequestService{}}
	middleware := optimizeRequestImages(&PersistentOutboundTransformer{state: state})
	body := []byte(`{"image":"signed-original","mask":"keep-mask"}`)
	request := &httpclient.Request{APIFormat: string(llm.APIFormatOpenAIImageEdit), Body: body}
	got, err := middleware.OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, body, got.Body)
	require.Empty(t, got.Metadata)
}

func TestUploadTimeoutRemainsGatewayFailure(t *testing.T) {
	err := classifyRequestFailure(context.Background(), pipeline.ErrRequestUploadTimeout)
	info := ExtractErrorInfo(err)
	require.Equal(t, "upstream_upload_timeout", info.Failure.Code)
	require.Equal(t, "structured_error", info.Failure.Source)
	require.Equal(t, 504, *info.StatusCode, "structured gateway status is distinguished by source")
}

func TestImageOptimizationUpdatesRecoveredResponsesSnapshot(t *testing.T) {
	var original bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&original, image.NewNRGBA(image.Rect(0, 0, 256, 256))))
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(original.Bytes())
	body := []byte(`{"model":"model","input":[{"type":"function_call_output","call_id":"same","output":[{"type":"input_image","image_url":"` + url + `"}]}]}`)
	continuation := &responsesContinuation{complete: true, retain: true, effective: body, snapshotBody: body}
	state := &PersistenceState{Request: &ent.Request{ProjectID: 1}, RequestService: &biz.RequestService{}, ResponsesContinuation: continuation}
	request := &httpclient.Request{APIFormat: string(llm.APIFormatOpenAIResponse), Body: body, JSONBody: body}
	got, err := optimizeRequestImages(&PersistentOutboundTransformer{state: state}).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.Less(t, len(got.Body), len(body))
	require.Equal(t, got.Body, got.JSONBody)
	require.Equal(t, got.Body, continuation.effective)
	require.Equal(t, got.Body, continuation.snapshotBody)
	require.Equal(t, gjson.GetBytes(got.Body, "input.0").Raw, string(continuation.input[0]))
}

func TestTransportFailureIsLoggedAfterHTTP200(t *testing.T) {
	logger := log.GetGlobalLogger()
	previous := *logger
	logger.SetLevel(log.InfoLevel)
	t.Cleanup(func() { *logger = previous })
	type captureKey struct{}
	ctx := context.WithValue(context.Background(), captureKey{}, true)
	count := 0
	logger.AddHook(log.HookFunc(func(logCtx context.Context, message string, fields ...log.Field) []log.Field {
		if message == "上游请求分段计时" && logCtx.Value(captureKey{}) == true {
			count++
		}
		return fields
	}))
	for _, semanticFailure := range []bool{false, true} {
		count = 0
		state := &PersistenceState{ExecutionTransport: &executionTransport{bytes: 128}}
		middleware := &persistRequestExecutionMiddleware{outbound: &PersistentOutboundTransformer{state: state}}
		// With no RequestExec this exercises the same response/error ordering
		// without adding persistence dependencies to a diagnostic assertion.
		if semanticFailure {
			state.ResponseTerminalError = errors.New("provider failed")
		}
		_, err := middleware.OnOutboundRawResponse(ctx, &httpclient.Response{StatusCode: http.StatusOK, Body: []byte(`{}`)})
		require.NoError(t, err)
		if !semanticFailure {
			require.Zero(t, count, "successful small bodies stay quiet at info")
			state.ExecutionTransport.record(ctx, true)
		}
		state.ExecutionTransport.record(ctx, true)
		state.ExecutionTransport.record(ctx, false)
		require.Equal(t, 1, count, "a later failure must still produce one info diagnostic")
	}
}
