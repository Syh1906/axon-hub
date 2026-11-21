package orchestrator

import (
	"context"
	"errors"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestExecutionSelectionKeepsActualKeyAndSession(t *testing.T) {
	const firstKey, secondKey = "fixture-key-first-a123", "fixture-key-second-b456"
	settings := &objects.ChannelSettings{KeySettings: []objects.ChannelKeySettings{{KeyRef: upstream.KeyRef(firstKey), Remark: "first"}, {KeyRef: upstream.KeyRef(secondKey), Remark: "second"}}}
	candidate := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "channel", Type: channel.TypeOpenai, Settings: settings}}, SessionState: "new", Priority: 7}
	state := &PersistenceState{ChannelModelsCandidates: []*ChannelModelsCandidate{candidate}, RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: "adaptive"}}
	first := executionSelection(contexts.WithChannelAPIKey(context.Background(), firstKey), state)
	candidate.TraceSticky = true
	second := executionSelection(contexts.WithChannelAPIKey(context.Background(), secondKey), state)
	require.Equal(t, "new", first.SessionState)
	require.Equal(t, "reused", second.SessionState)
	require.Equal(t, "first", first.KeyRemark)
	require.Equal(t, "second", second.KeyRemark)
	require.Equal(t, upstream.MaskKey(firstKey), first.KeyHint)
	require.Equal(t, upstream.KeyRef(firstKey), first.KeyRef)
	require.Equal(t, upstream.MaskKey(secondKey), second.KeyHint)
	candidate.Channel.Name = "renamed"
	require.Equal(t, "channel", first.ChannelName)
	require.Equal(t, "channel", first.Candidates[0].ChannelName)
	candidate.TraceSticky = false
	candidate.healthGate = &healthGateCandidateInfo{decision: &healthGateRequestDecision{sticky: true, lookupFailed: true}}
	require.Equal(t, "unrecorded", executionSelection(context.Background(), state).SessionState)
	candidate.continuationBound = true
	bound := executionSelection(context.Background(), state)
	require.Equal(t, "reused", bound.SessionState)
	require.True(t, bound.Candidates[0].SessionReuse)
}

func TestSessionDispatchDoesNotCountLocalContinuationRejection(t *testing.T) {
	ctx, client := setupTest(t)
	req := client.Request.Create().SetModelID("test").SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus("failed").SaveX(ctx)
	attempt := client.RequestExecution.Create().SetRequestID(req.ID).SetModelID("test").
		SetRequestBody(objects.JSONRawMessage(`{}`)).SetStatus("failed").
		SetSelection(&objects.RequestExecutionSelection{Dispatch: objects.RequestExecutionDispatchPending}).SaveX(ctx)
	_, requests, _, _ := setupTestServices(t, client)
	upstream := &mockExecutor{}
	executor := dispatchRecordingExecutor{
		Executor: responses.NewTransportExecutor(upstream, nil, true), service: requests, execution: attempt,
	}
	_, err := executor.Do(ctx, &httpclient.Request{Body: []byte(`{"previous_response_id":"resp_missing"}`)})
	require.ErrorIs(t, err, httpclient.ErrContinuationUnavailable)
	require.False(t, upstream.requestCalled)
	require.Equal(t, objects.RequestExecutionDispatchPending, client.RequestExecution.GetX(ctx, attempt.ID).Selection.Dispatch)

	wire := &httpclient.Request{TransportTrace: new(httpclient.TransportTrace)}
	wire.ReportHTTPProgress(httpclient.TransportStarted, time.Now())
	executor.record(ctx, wire, errors.New("connection failed before upload"))
	require.Equal(t, objects.RequestExecutionDispatchPending, client.RequestExecution.GetX(ctx, attempt.ID).Selection.Dispatch)
	wire.ReportHTTPProgress(httpclient.TransportUploaded, time.Now())
	executor.record(ctx, wire, errors.New("response lost after upload"))
	require.Equal(t, objects.RequestExecutionDispatchStarted, client.RequestExecution.GetX(ctx, attempt.ID).Selection.Dispatch)
}

func TestOutboundTierSendEvidence(t *testing.T) {
	ctx, client := setupTest(t)
	_, requests, _, _ := setupTestServices(t, client)
	for _, tc := range []struct {
		name, provider, transport string
		wroteFrame                bool
		err                       error
		want                      bool
	}{
		{"websocket write failed", "openai", "websocket", false, httpclient.ErrRequestMayHaveExecuted, false},
		{"websocket response lost after write", "openai", "websocket", true, httpclient.ErrRequestMayHaveExecuted, true},
		{"local response", "openai_fake", "http", false, nil, false},
		{"custom HTTP endpoint on fake channel", "openai_fake", "http", true, nil, true},
		{"upstream request written", "openai", "http", true, nil, true},
		{"response without upload evidence", "openai", "http", false, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := client.Request.Create().SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus("failed").SaveX(ctx)
			attempt := client.RequestExecution.Create().SetRequestID(req.ID).SetModelID("model").SetRequestBody([]byte(`{}`)).
				SetStatus("failed").SetCostSnapshot(&objects.RequestCostSnapshot{ServiceTier: &objects.ServiceTierEvidence{Outbound: "priority"}}).
				SetSelection(&objects.RequestExecutionSelection{Dispatch: objects.RequestExecutionDispatchPending, Provider: tc.provider, UpstreamTransport: tc.transport}).SaveX(ctx)
			wire := &httpclient.Request{TransportTrace: new(httpclient.TransportTrace)}
			if tc.wroteFrame {
				if tc.transport == "websocket" {
					wire.TransportTrace.WebSocketFrameWritten()
				} else {
					wire.ReportHTTPProgress(httpclient.TransportStarted, time.Now())
					wire.ReportHTTPProgress(httpclient.TransportUploaded, time.Now())
				}
			}
			recorder := dispatchRecordingExecutor{service: requests, execution: attempt}
			recorder.record(ctx, wire, tc.err)
			result, err := biz.RequestLogResultExecutions(ctx, client, []int{req.ID})
			require.NoError(t, err)
			row := result[req.ID]
			require.NotNil(t, row)
			require.Equal(t, new(tc.want), row.TierSent)
			if tc.want {
				require.Equal(t, new("fast"), row.OutboundServiceTier())
			} else {
				require.Nil(t, row.OutboundServiceTier())
			}
		})
	}
}
