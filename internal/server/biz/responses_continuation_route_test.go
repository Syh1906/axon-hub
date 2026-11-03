package biz

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm"
)

func TestResponsesUpstreamRefUsesStableAccountIdentity(t *testing.T) {
	headers := http.Header{"Chatgpt-Account-Id": {"fixture-account"}, "Authorization": {"Bearer fixture-old"}}
	origin := ResponsesUpstreamRef("https://provider.example/v1/responses", headers)
	headers.Set("Authorization", "Bearer fixture-refreshed")
	require.Equal(t, origin, ResponsesUpstreamRef("wss://provider.example/v1/responses", headers))
	require.Equal(t, origin, ResponsesUpstreamRef("https://provider.example/v1/responses/compact", headers))
	headers.Set("Chatgpt-Account-Id", "another-account")
	require.NotEqual(t, origin, ResponsesUpstreamRef("https://provider.example/v1/responses", headers))
	headers.Set("Chatgpt-Account-Id", "fixture-account")
	require.NotEqual(t, origin, ResponsesUpstreamRef("https://another.example/v1/responses", headers))
}

func TestResponsesContinuationRouteSurvivesCacheLossAndFailedSelection(t *testing.T) {
	config := xcache.Config{Mode: xcache.ModeMemory}
	ctx, client, service := newRequestStickyTestService(t, config, config)
	thread, trace, ch := createRequestStickyScope(t, ctx, client)
	key := client.APIKey.Create().SetProjectID(thread.ProjectID).SetName("route-owner").SetKey("fixture-owner-key").SaveX(ctx)
	ctx = contexts.WithAPIKey(contexts.WithProjectID(contexts.WithTrace(ctx, trace), thread.ProjectID), key)
	create := func(status request.Status, at time.Time, externalID string, apiKeyID int) *ent.Request {
		return client.Request.Create().SetProjectID(thread.ProjectID).SetAPIKeyID(apiKeyID).
			SetTraceID(trace.ID).SetChannelID(ch.ID).SetModelID("gpt-test").
			SetFormat(string(llm.APIFormatOpenAIResponse)).SetStatus(status).
			SetRequestBody([]byte(`{"_axon_payload":{"version":1,"state":"expired"}}`)).
			SetExternalID(externalID).SetCreatedAt(at).SaveX(ctx)
	}
	succeeded := create(request.StatusCompleted, time.Now().Add(-time.Hour), "resp_succeeded", key.ID)
	client.RequestExecution.Create().SetProjectID(thread.ProjectID).SetRequestID(succeeded.ID).
		SetChannelID(ch.ID).SetModelID("gpt-test").SetStatus("completed").SetRequestBody([]byte(`{}`)).
		SetSelection(&objects.RequestExecutionSelection{KeyRef: "fixture-key-ref", KeyHint: "••••test"}).SaveX(ctx)
	create(request.StatusFailed, time.Now(), "resp_failed", key.ID)
	create(request.StatusProcessing, time.Now(), "resp_processing", key.ID)
	// The ordinary preference can point elsewhere after a failed attempt.
	service.setPreviousTraceChannelID(ctx, trace.ID, ch.ID+10)
	for _, resetCache := range []bool{false, true} {
		if resetCache {
			service.previousChannelCache = xcache.NewFromConfig[int](config)
		}
		route, found, err := service.GetResponsesContinuationRoute(ctx, "resp_succeeded")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, ResponsesContinuationRoute{ChannelID: ch.ID, KeyRef: "fixture-key-ref", KeyHint: "••••test"}, route)
	}
	_, found, err := service.GetResponsesContinuationRoute(ctx, "resp_failed")
	require.NoError(t, err)
	require.False(t, found)

	otherKey := client.APIKey.Create().SetProjectID(thread.ProjectID).SetName("other").SetKey("fixture-other-key").SaveX(ctx)
	otherContext := contexts.WithAPIKey(ctx, otherKey)
	_, found, err = service.GetResponsesContinuationRoute(otherContext, "resp_succeeded")
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = service.GetResponsesContinuationRoute(contexts.WithProjectID(ctx, thread.ProjectID+100), "resp_succeeded")
	require.NoError(t, err)
	require.False(t, found)

	ctx = contexts.WithAPIKey(contexts.WithProjectID(ctx, thread.ProjectID), key)
	newTrace := client.Trace.Create().SetProjectID(thread.ProjectID).SetThreadID(thread.ID).SetTraceID("continuation-next-trace").SaveX(ctx)
	_, found, err = service.GetResponsesContinuationRoute(contexts.WithTrace(ctx, newTrace), "")
	require.NoError(t, err)
	require.False(t, found, "a later trace cannot establish the origin of unspecified history")
}

func TestResponsesContinuationRouteRequiresKnownScope(t *testing.T) {
	config := xcache.Config{Mode: xcache.ModeMemory}
	ctx, client, service := newRequestStickyTestService(t, config, config)
	thread, trace, ch := createRequestStickyScope(t, ctx, client)
	client.Request.Create().SetProjectID(thread.ProjectID).SetTraceID(trace.ID).SetChannelID(ch.ID).
		SetModelID("gpt-test").SetFormat(string(llm.APIFormatOpenAIResponse)).SetStatus(request.StatusCompleted).
		SetRequestBody([]byte(`{}`)).SetExternalID("resp_legacy").SaveX(ctx)
	_, found, err := service.GetResponsesContinuationRoute(ctx, "resp_legacy")
	require.NoError(t, err)
	require.False(t, found, "a project is required")
	projectContext := contexts.WithProjectID(ctx, thread.ProjectID)
	_, found, err = service.GetResponsesContinuationRoute(projectContext, "")
	require.NoError(t, err)
	require.False(t, found, "an unrelated latest request cannot establish provenance")
	route, found, err := service.GetResponsesContinuationRoute(contexts.WithTrace(projectContext, trace), "resp_legacy")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, ch.ID, route.ChannelID)
	require.Empty(t, route.KeyRef, "older rows can recover the channel without inventing a credential")
}

func TestResponsesContinuationOriginsUseArtifactsAndSurviveInterruption(t *testing.T) {
	config := xcache.Config{Mode: xcache.ModeMemory}
	ctx, client, service := newRequestStickyTestService(t, config, config)
	thread, trace, ch := createRequestStickyScope(t, ctx, client)
	key := client.APIKey.Create().SetProjectID(thread.ProjectID).SetName("origin-owner").SetKey("fixture-origin-key").SaveX(ctx)
	ctx = contexts.WithAPIKey(contexts.WithProjectID(contexts.WithTrace(ctx, trace), thread.ProjectID), key)
	create := func(model, keyRef string) *ent.RequestExecution {
		req := client.Request.Create().SetProjectID(thread.ProjectID).SetAPIKeyID(key.ID).SetTraceID(trace.ID).
			SetChannelID(ch.ID).SetModelID(model).SetFormat(string(llm.APIFormatOpenAIResponse)).
			SetStatus(request.StatusCanceled).SetRequestBody([]byte(`{}`)).SaveX(ctx)
		return client.RequestExecution.Create().SetProjectID(thread.ProjectID).SetRequestID(req.ID).SetChannelID(ch.ID).
			SetModelID(model).SetStatus("canceled").SetRequestBody([]byte(`{}`)).
			SetSelection(&objects.RequestExecutionSelection{KeyRef: keyRef, UpstreamRef: "endpoint"}).SaveX(ctx)
	}
	first := create("astra", "first-account")
	firstBody := []byte(`{"id":"resp_first","status":"incomplete","output":[{"type":"reasoning","status":"completed","encrypted_content":"cipher-first"},{"type":"reasoning","status":"in_progress","encrypted_content":"cipher-partial"}]}`)
	for range 2 {
		require.NoError(t, service.RecordResponsesOrigin(ctx, first, nil, firstBody))
	}
	for _, status := range []string{"failed", "in_progress", "canceled"} {
		require.NoError(t, service.RecordResponsesOrigin(ctx, first, nil,
			[]byte(fmt.Sprintf(`{"id":"resp_not_ready","status":%q,"output":[{"encrypted_content":"cipher-not-ready"}]}`, status))))
	}
	require.Equal(t, 2, client.ResponsesOrigin.Query().CountX(ctx), "duplicate delivery is idempotent")
	second := create("luna", "second-account")
	require.NoError(t, service.RecordResponsesOrigin(ctx, second, nil, []byte(`{"id":"resp_second","status":"completed","output":[{"type":"reasoning","encrypted_content":"cipher-second"}]}`)))
	for _, previous := range []string{"", "resp_first"} {
		route, found, err := service.GetResponsesContinuationRoute(ctx, previous, "cipher-first")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "first-account", route.KeyRef, "later auxiliary calls cannot rewrite origin")
	}
	_, found, err := service.GetResponsesContinuationRoute(ctx, "", "cipher-partial")
	require.NoError(t, err)
	require.False(t, found, "in-progress items are never reusable origin evidence")
	_, _, err = service.GetResponsesContinuationRoute(ctx, "", "cipher-first", "cipher-second")
	require.ErrorIs(t, err, ErrResponsesOriginConflict)
	_, _, err = service.GetResponsesContinuationRoute(ctx, "resp_first", "cipher-second")
	require.ErrorIs(t, err, ErrResponsesOriginConflict, "explicit response ID does not conceal conflicting history")
	_, found, err = service.GetResponsesContinuationRoute(contexts.WithProjectID(ctx, thread.ProjectID+1), "resp_first", "cipher-first")
	require.NoError(t, err)
	require.False(t, found)
	ctx = contexts.WithProjectID(ctx, thread.ProjectID)
	otherKey := client.APIKey.Create().SetProjectID(thread.ProjectID).SetName("origin-other").SetKey("fixture-other-origin").SaveX(ctx)
	_, found, err = service.GetResponsesContinuationRoute(contexts.WithAPIKey(ctx, otherKey), "resp_first", "cipher-first")
	require.NoError(t, err)
	require.False(t, found)
	ctx = contexts.WithAPIKey(ctx, key)
	// Compaction has no response status. It creates new opaque context while
	// echoed input items must keep their original provenance.
	require.NoError(t, service.RecordResponsesOrigin(ctx, second, []byte(`{"input":[{"encrypted_content":"cipher-first"}]}`),
		[]byte(`{"id":"cmp_test","object":"response.compaction","output":[{"type":"compaction","encrypted_content":"cipher-compact"},{"encrypted_content":"cipher-first"}]}`)))
	route, found, err := service.GetResponsesContinuationRoute(ctx, "", "cipher-compact")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "second-account", route.KeyRef)
	_, found, err = service.GetResponsesContinuationRoute(ctx, "", "cipher-first")
	require.NoError(t, err, "echoed history cannot be reassigned")
	require.True(t, found)
	// A new service with no in-memory session/affinity caches still resolves it.
	restarted := &RequestService{AbstractService: service.AbstractService}
	route, found, err = restarted.GetResponsesContinuationRoute(ctx, "", "cipher-first")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "first-account", route.KeyRef)
	client.Channel.UpdateOneID(ch.ID).SetStatus("disabled").SaveX(ctx)
	route, found, err = restarted.GetResponsesContinuationRoute(ctx, "", "cipher-first")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "continuation_channel_disabled", route.UnavailableCode)
}

func TestResponsesContinuationLegacyCredentialConflict(t *testing.T) {
	config := xcache.Config{Mode: xcache.ModeMemory}
	ctx, client, service := newRequestStickyTestService(t, config, config)
	thread, trace, ch := createRequestStickyScope(t, ctx, client)
	key := client.APIKey.Create().SetProjectID(thread.ProjectID).SetName("origin-owner").SetKey("fixture-owner").SaveX(ctx)
	ctx = contexts.WithAPIKey(contexts.WithProjectID(contexts.WithTrace(ctx, trace), thread.ProjectID), key)
	create := func(id string) *ent.Request {
		return client.Request.Create().SetProjectID(thread.ProjectID).SetAPIKeyID(key.ID).SetTraceID(trace.ID).SetChannelID(ch.ID).
			SetModelID("gpt-test").SetFormat(string(llm.APIFormatOpenAIResponse)).SetStatus(request.StatusCompleted).
			SetRequestBody([]byte(`{}`)).SetExternalID(id).SaveX(ctx)
	}
	legacy := create("resp_legacy")
	client.RequestExecution.Create().SetProjectID(thread.ProjectID).SetRequestID(legacy.ID).SetChannelID(ch.ID).
		SetModelID("gpt-test").SetStatus("completed").SetRequestBody([]byte(`{}`)).
		SetSelection(&objects.RequestExecutionSelection{KeyHint: "masked-AAAA"}).SaveX(ctx)
	fresh := create("resp_new")
	execution := client.RequestExecution.Create().SetProjectID(thread.ProjectID).SetRequestID(fresh.ID).SetChannelID(ch.ID).
		SetModelID("gpt-test").SetStatus("completed").SetRequestBody([]byte(`{}`)).
		SetSelection(&objects.RequestExecutionSelection{KeyRef: responsesDigest("key-B"), KeyHint: "masked-BBBB"}).SaveX(ctx)
	require.NoError(t, service.RecordResponsesOrigin(ctx, execution, nil,
		[]byte(`{"id":"resp_new","status":"completed","output":[{"type":"reasoning","encrypted_content":"new-cipher"}]}`)))
	_, _, err := service.GetResponsesContinuationRoute(ctx, "resp_legacy", "new-cipher")
	require.ErrorIs(t, err, ErrResponsesOriginConflict, "different legacy hints are incompatibility evidence even when one origin has a full reference")
}
