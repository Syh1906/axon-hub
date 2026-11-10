package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesProtocolRecoveryKeepsPluginInputAndWireRecord(t *testing.T) {
	testResponsesProtocolRecovery(t, "before")
}

func TestResponsesProtocolRecoveryAfterPluginEvictsConnection(t *testing.T) {
	testResponsesProtocolRecovery(t, "plugin_eviction")
}

func TestResponsesProtocolRecoveryAfterAccountHeaderChanges(t *testing.T) {
	testResponsesProtocolRecovery(t, "account")
}

func testResponsesProtocolRecovery(t *testing.T, recoveryMode string) {
	// Given two completed native turns, including the rc1 raw-request plugin stage.
	var connections, calls atomic.Int32
	frames := make(chan []byte, 4)
	upstream := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		connections.Add(1)
		defer conn.Close()
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			frames <- body
			n := calls.Add(1)
			event := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-%d","status":"completed","output":[{"type":"message","id":"msg-%d","role":"assistant","content":[{"type":"output_text","text":"reply-%d"}]}]}}`, n, n, n)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
				return
			}
		}
	}))
	client, handlers, project := newResponsesIntegrationFixture(t, upstream.URL, func(create *ent.ChannelCreate) {
		create.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses-ws"}})
		create.SetSettings(&objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "synthetic-model", APIFormats: []string{"openai/responses-ws"}}}})
	})
	hook := &responsesRecoveryHook{lanes: make(chan *shared.ResponsesLane, 4)}
	if recoveryMode == "plugin_eviction" {
		hook.retireOn = 3
	}
	if recoveryMode == "account" {
		hook.accountOn = 3
	}
	processor := handlers.ResponseCompletionHandlers.ChatCompletionOrchestrator
	processor.Plugins.Hooks = hook
	gateway := responsesIntegrationGateway(t, client, handlers, project)
	conn := responsesIntegrationDial(t, gateway)
	first := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","store":false,"input":"root"}`)
	require.Equal(t, "resp-1", gjson.GetBytes(first, "response.id").String(), string(first))
	<-frames
	lane := <-hook.lanes
	second := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","store":false,"previous_response_id":"resp-1","input":"delta"}`)
	require.Equal(t, "resp-2", gjson.GetBytes(second, "response.id").String(), string(second))
	sent := <-frames
	<-hook.lanes
	require.Equal(t, "resp-1", gjson.GetBytes(sent, "previous_response_id").String())
	require.NotContains(t, string(sent), "plugin-1")
	require.Contains(t, string(sent), "plugin-2")
	// When the gateway knows the owning connection was evicted before the next send.
	if recoveryMode == "before" {
		lane.RetireLeases()
	}
	third := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","store":false,"previous_response_id":"resp-2","service_tier":"priority","input":"recover"}`)
	require.Equal(t, "resp-3", gjson.GetBytes(third, "response.id").String(), string(third))
	recovered := <-frames
	<-hook.lanes
	// Then only the new request is sent, with complete plugin-adjusted history.
	require.Equal(t, int32(2), connections.Load())
	require.Equal(t, int32(3), calls.Load())
	require.Equal(t, int32(3), hook.calls.Load())
	require.False(t, gjson.GetBytes(recovered, "previous_response_id").Exists())
	require.Equal(t, "priority", gjson.GetBytes(recovered, "service_tier").String())
	for _, text := range []string{"root", "reply-1", "delta", "reply-2", "recover", "plugin-1", "plugin-2", "plugin-3"} {
		require.Contains(t, string(recovered), text)
	}
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	execution := client.RequestExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
	recorded, err := processor.RequestService.LoadRequestExecutionRequestBody(ctx, execution)
	require.NoError(t, err)
	require.JSONEq(t, gjson.GetBytes(recovered, "input").Raw, gjson.GetBytes(recorded, "input").Raw)
	require.False(t, gjson.GetBytes(recorded, "previous_response_id").Exists())
	// Recovery remains scoped to this lane and downstream connection.
	other := responsesIntegrationDial(t, gateway)
	denied := responsesIntegrationTurn(t, other, `{"type":"response.create","model":"synthetic-model","previous_response_id":"resp-3","input":"foreign"}`)
	require.Equal(t, "previous_response_not_found", gjson.GetBytes(denied, "error.code").String())
	denied = responsesIntegrationTurn(t, conn, `{"type":"response.create","stream_id":"other","model":"synthetic-model","previous_response_id":"resp-3","input":"foreign"}`)
	require.Equal(t, "previous_response_not_found", gjson.GetBytes(denied, "error.code").String())
	require.Equal(t, int32(3), calls.Load())
}

type responsesRecoveryHook struct {
	calls     atomic.Int32
	lanes     chan *shared.ResponsesLane
	retireOn  int32
	accountOn int32
}

func (h *responsesRecoveryHook) PassThroughApplied(context.Context, int) {}
func (h *responsesRecoveryHook) Middleware(context.Context) (orchestrator.RequestHooks, error) {
	return orchestrator.RequestHooks{Attempt: pipeline.OnRawRequest("recovery_fixture", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			return nil, err
		}
		input := gjson.GetBytes(request.Body, "input")
		var items []json.RawMessage
		if input.IsArray() {
			if err := json.Unmarshal(payload["input"], &items); err != nil {
				return nil, err
			}
		} else {
			item, err := json.Marshal(map[string]string{"role": "user", "content": input.String()})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		n := h.calls.Add(1)
		if n == h.retireOn {
			shared.GetResponsesLane(ctx).RetireLeases()
		}
		if n == h.accountOn {
			request.Headers.Set("Chatgpt-Account-Id", "new-fixture-account")
		}
		items = append(items, json.RawMessage(fmt.Sprintf(`{"role":"user","content":"plugin-%d"}`, n)))
		payload["input"], _ = json.Marshal(items)
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		request.Body = body
		h.lanes <- shared.GetResponsesLane(ctx)
		return request, nil
	})}, nil
}
