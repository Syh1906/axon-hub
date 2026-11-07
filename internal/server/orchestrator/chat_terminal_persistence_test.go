package orchestrator

import (
	"context"
	"testing"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/stretchr/testify/require"
)

func TestChatTerminalPersistence(t *testing.T) {
	tests := []struct {
		name    string
		choices int
		chunks  []string
		state   streamTerminalState
	}{
		{"error", 1, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"error"}]}`}, streamTerminalFailed},
		{"cancelled", 1, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"cancelled"}]}`}, streamTerminalCanceled},
		{"canceled", 1, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"canceled"}]}`}, streamTerminalCanceled},
		{"error_then_stop", 2, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"error"}]}`, `{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`}, streamTerminalFailed},
		{"cancelled_then_stop", 2, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"cancelled"}]}`, `{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`}, streamTerminalCanceled},
		{"mixed_chunk", 2, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"error"},{"index":1,"delta":{},"finish_reason":"stop"}]}`}, streamTerminalFailed},
		{"error_then_done", 2, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"error"}]}`, `[DONE]`}, streamTerminalFailed},
		{"two_successful_choices", 2, []string{`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, `{"choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`}, streamTerminalCompleted},
	}
	for _, tt := range tests {
		for _, tail := range []bool{false, true} {
			name := tt.name + "/current"
			if tail {
				name = tt.name + "/tail"
			}
			t.Run(name, func(t *testing.T) {
				client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
				defer client.Close()
				ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
				streamCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				project := createTestProject(t, ctx, client)
				channel := createTestChannel(t, ctx, client)
				_, service, _, usageService := setupTestServices(t, client)
				body := []byte(`{"stream":true,"n":1}`)
				if tt.choices == 2 {
					body = []byte(`{"stream":true,"n":2}`)
				}
				req := client.Request.Create().SetProjectID(project.ID).SetChannelID(channel.ID).
					SetModelID("gpt-5").SetStatus(request.StatusProcessing).SetRequestBody(body).SetStream(true).SaveX(ctx)
				execution := client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(project.ID).
					SetChannelID(channel.ID).SetModelID("gpt-5").SetFormat(llm.APIFormatOpenAIChatCompletion.String()).
					SetStatus(requestexecution.StatusProcessing).SetRequestBody(body).SetStream(true).SaveX(ctx)
				events := make([]*httpclient.StreamEvent, 0, len(tt.chunks)+1)
				for _, chunk := range tt.chunks {
					events = append(events, &httpclient.StreamEvent{Data: []byte(chunk)})
				}
				events = append(events, &llm.DoneStreamEvent)
				transformer, err := openai.NewOutboundTransformer("https://example.test", "fixture")
				require.NoError(t, err)
				state := &PersistenceState{}
				raw := NewOutboundPersistentStream(streamCtx, streams.SliceStream(events), req, execution, service, usageService, transformer, nil, state)
				if tail {
					for i, event := range events {
						require.NoError(t, raw.consumeTailEvent(event))
						if i == 0 && len(tt.chunks) > 1 {
							require.Equal(t, streamTerminalNone, state.OutboundStreamTerminal)
						}
					}
					cancel()
					require.NoError(t, raw.Close())
				} else {
					inbound := NewInboundPersistentStream(streamCtx, raw, req, execution, service, openai.NewInboundTransformer(), nil, state)
					for inbound.Next() {
						inbound.Current()
					}
					if tt.state == streamTerminalCompleted {
						inbound.AcknowledgeDelivery()
					}
					cancel()
					require.NoError(t, inbound.Close())
					require.Equal(t, tt.state.requestStatus(), client.Request.GetX(ctx, req.ID).Status)
					require.Equal(t, tt.state == streamTerminalCompleted, state.StreamCompleted)
				}
				require.Equal(t, tt.state, state.OutboundStreamTerminal)
				require.Equal(t, string(tt.state.requestStatus()), string(client.RequestExecution.GetX(ctx, execution.ID).Status))
			})
		}
	}
}
