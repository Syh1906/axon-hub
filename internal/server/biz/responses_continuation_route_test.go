package biz

import (
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
		route, found, err := service.GetResponsesContinuationRoute(ctx, "")
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
	route, found, err := service.GetResponsesContinuationRoute(contexts.WithTrace(ctx, newTrace), "")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, ch.ID, route.ChannelID)
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
	route, found, err := service.GetResponsesContinuationRoute(contexts.WithTrace(projectContext, trace), "")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, ch.ID, route.ChannelID)
	require.Empty(t, route.KeyRef, "older rows can recover the channel without inventing a credential")
}
