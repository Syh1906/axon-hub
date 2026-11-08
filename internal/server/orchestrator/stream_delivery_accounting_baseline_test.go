package orchestrator

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/lifecycle"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestStreamDeliveryFailurePreservesCompletedExecution(t *testing.T) {
	for _, useLifecycle := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			writeErr   error
			cancel     bool
			ack        bool
			prior      bool
			stopCause  error
			wantStatus request.Status
			wantCode   string
		}{
			{name: "canceled terminal write", writeErr: context.Canceled, cancel: true, wantStatus: request.StatusCanceled, wantCode: "stream_delivery_canceled"},
			{name: "canceled without write", cancel: true, wantStatus: request.StatusCanceled, wantCode: "stream_delivery_canceled"},
			{name: "write failure survives cleanup cancellation", writeErr: io.ErrClosedPipe, cancel: true, wantStatus: request.StatusFailed, wantCode: "stream_delivery_failed"},
			{name: "write deadline", writeErr: context.DeadlineExceeded, wantStatus: request.StatusFailed, wantCode: "stream_delivery_timeout"},
			{name: "no acknowledgement is not cancellation", wantStatus: request.StatusFailed, wantCode: "stream_delivery_unconfirmed"},
			{name: "acknowledgement survives cancellation", ack: true, cancel: true, wantStatus: request.StatusCompleted},
			{name: "earlier failure is preserved", writeErr: context.Canceled, cancel: true, prior: true, wantStatus: request.StatusFailed, wantCode: "continuation_origin_persistence_failed"},
			{name: "actual request deadline before delivery", stopCause: context.DeadlineExceeded, wantStatus: request.StatusFailed, wantCode: "stream_delivery_timeout"},
			{name: "server shutdown before delivery", stopCause: errors.New("server shutdown"), wantStatus: request.StatusFailed, wantCode: "stream_delivery_failed"},
		} {
			if tc.stopCause != nil && !useLifecycle {
				continue
			}
			name := tc.name
			if useLifecycle {
				name += "/lifecycle"
			}
			t.Run(name, func(t *testing.T) {
				client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
				t.Cleanup(func() { require.NoError(t, client.Close()) })
				ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
				project := createTestProject(t, ctx, client)
				channel := createTestChannel(t, ctx, client)
				_, service, _, usage := setupTestServices(t, client)
				req := client.Request.Create().SetProjectID(project.ID).SetChannelID(channel.ID).
					SetModelID("gpt-test").SetFormat(llm.APIFormatOpenAIResponse.String()).
					SetStatus(request.StatusProcessing).SetStream(true).SetClientStatusCode(200).
					SetRequestBody([]byte(`{"stream":true}`)).SaveX(ctx)
				exec := client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(project.ID).
					SetChannelID(channel.ID).SetModelID("gpt-test").SetFormat(llm.APIFormatOpenAIResponse.String()).
					SetStatus(requestexecution.StatusProcessing).SetStream(true).
					SetRequestBody([]byte(`{"stream":true}`)).SaveX(ctx)
				downstream, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				manager := lifecycle.New()
				if tc.stopCause == context.DeadlineExceeded {
					var deadlineCancel context.CancelFunc
					downstream, deadlineCancel = context.WithTimeout(downstream, time.Second)
					t.Cleanup(deadlineCancel)
				} else if tc.stopCause != nil {
					downstream = manager.Attach(downstream)
				}
				streamCtx := downstream
				var owner *streamLifecycle
				if useLifecycle {
					owner, streamCtx = newStreamLifecycle(downstream)
					t.Cleanup(owner.Close)
				}
				outbound, err := responses.NewOutboundTransformer("https://example.test", "synthetic")
				require.NoError(t, err)
				state := &PersistenceState{}
				event := &httpclient.StreamEvent{Type: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_delivery","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":34,"total_tokens":46}}}`)}
				provider := NewOutboundPersistentStream(streamCtx, streams.SliceStream([]*httpclient.StreamEvent{event}), req, exec, service, usage, outbound, nil, state)
				stream := NewInboundPersistentStream(streamCtx, provider, req, exec, service, responses.NewInboundTransformer(), nil, state)
				require.True(t, stream.Next())
				stream.Current()
				if tc.stopCause != nil {
					require.NoError(t, downstream.Err())
					if tc.stopCause == context.DeadlineExceeded {
						<-downstream.Done()
					} else {
						manager.Shutdown(tc.stopCause)
					}
					<-owner.done
					err := stream.Deliver(downstream, func() (bool, error) {
						t.Error("terminal write ran after deadline or shutdown")
						return true, nil
					})
					require.ErrorIs(t, err, tc.stopCause)
				}
				if tc.prior {
					require.NoError(t, service.UpdateRequestStatusFromError(ctx, req.ID, errors.New("origin persistence failed"), &objects.RequestFailure{
						Stage: "origin_persistence", Type: "gateway_error", Code: tc.wantCode, Message: "original failure",
					}))
				}
				if tc.writeErr != nil || tc.ack {
					err := stream.Deliver(downstream, func() (bool, error) { return tc.ack, tc.writeErr })
					require.ErrorIs(t, err, tc.writeErr)
				}
				if tc.cancel {
					cancel()
				}
				require.NoError(t, stream.Close())
				require.NoError(t, stream.Close()) // Repeated cleanup must not change diagnostics or duplicate usage.
				saved := client.Request.GetX(ctx, req.ID)
				require.Equal(t, tc.wantStatus, saved.Status)
				require.Equal(t, 200, *saved.ClientStatusCode)
				if tc.wantCode == "" {
					require.Nil(t, saved.Failure)
				} else {
					require.NotNil(t, saved.Failure)
					require.Equal(t, tc.wantCode, saved.Failure.Code)
					require.NotEmpty(t, saved.Failure.Message)
					if tc.prior {
						require.Equal(t, "original failure", saved.Failure.Message)
					} else {
						require.Equal(t, "delivery", saved.Failure.Stage)
						require.Equal(t, "gateway_error", saved.Failure.Type)
					}
				}
				savedExec := client.RequestExecution.GetX(ctx, exec.ID)
				require.Equal(t, requestexecution.StatusCompleted, savedExec.Status)
				require.Nil(t, savedExec.Failure)
				require.Empty(t, savedExec.ErrorMessage)
				rows := client.UsageLog.Query().AllX(ctx)
				require.Len(t, rows, 1)
				require.EqualValues(t, 34, rows[0].CompletionTokens)
			})
		}
	}
}

func TestStreamDeliveryAccountingBaseline(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		eventType  string
		status     string
		usage      string
		wantStatus requestexecution.Status
		wantRows   int
	}{
		{"failed usage", "response.failed", "failed", `,"usage":{"input_tokens":12,"output_tokens":34,"total_tokens":46}`, requestexecution.StatusFailed, 1},
		{"incomplete zero", "response.incomplete", "incomplete", `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, requestexecution.StatusFailed, 1},
		{"canceled usage", "response.cancelled", "canceled", `,"usage":{"input_tokens":12,"output_tokens":34,"total_tokens":46}`, requestexecution.StatusCanceled, 1},
		{"failed missing usage", "response.failed", "failed", "", requestexecution.StatusFailed, 0},
		{"partial cancel usage", "response.in_progress", "in_progress", `,"usage":{"input_tokens":12,"output_tokens":34,"total_tokens":46}`, requestexecution.StatusCanceled, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given
			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
			project := createTestProject(t, ctx, client)
			channel := createTestChannel(t, ctx, client)
			_, requestService, _, usageService := setupTestServices(t, client)
			req, err := client.Request.Create().SetProjectID(project.ID).SetChannelID(channel.ID).
				SetModelID("gpt-5").SetStatus(request.StatusProcessing).SetStream(true).
				SetRequestBody([]byte(`{"stream":true}`)).Save(ctx)
			require.NoError(t, err)
			exec, err := client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(project.ID).
				SetChannelID(channel.ID).SetModelID("gpt-5").SetFormat(llm.APIFormatOpenAIResponse.String()).
				SetStatus(requestexecution.StatusProcessing).SetStream(true).
				SetRequestBody([]byte(`{"stream":true}`)).Save(ctx)
			require.NoError(t, err)
			outbound, err := responses.NewOutboundTransformer("https://example.test", "synthetic-key")
			require.NoError(t, err)
			streamCtx, cancel := context.WithCancel(ctx)
			t.Cleanup(cancel)
			event := &httpclient.StreamEvent{Type: scenario.eventType, Data: []byte(`{"type":"` + scenario.eventType + `","response":{"id":"resp_baseline","status":"` + scenario.status + `","output":[]` + scenario.usage + `}}`)}
			stream := NewOutboundPersistentStream(streamCtx, streams.SliceStream([]*httpclient.StreamEvent{event}), req, exec, requestService, usageService, outbound, nil, &PersistenceState{})
			// When
			require.True(t, stream.Next())
			stream.Current()
			cancel()
			require.NoError(t, stream.Close())
			// Then
			saved, err := client.RequestExecution.Get(ctx, exec.ID)
			require.NoError(t, err)
			require.Equal(t, scenario.wantStatus, saved.Status)
			rows, err := client.UsageLog.Query().All(ctx)
			require.NoError(t, err)
			require.Len(t, rows, scenario.wantRows)
		})
	}
}
