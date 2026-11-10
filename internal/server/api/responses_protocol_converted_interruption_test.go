package api

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesProtocolConvertedInterruptionDoesNotCertifyHistory(t *testing.T) {
	upstream := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := readRequestBody(r)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, err = io.WriteString(w, "data: {\"id\":\"converted-root\",\"object\":\"chat.completion.chunk\",\"model\":\"synthetic-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial\"}}]}\n\n")
		if err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	client, handlers, project := newResponsesIntegrationFixture(t, upstream.URL, func(create *ent.ChannelCreate) {
		create.SetType(channel.TypeOpenai)
		create.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/chat_completions"}})
	})
	hook := &responsesObserveLaneHook{lanes: make(chan *shared.ResponsesLane, 1)}
	handlers.ResponseCompletionHandlers.ChatCompletionOrchestrator.Plugins.Hooks = hook
	gateway := responsesIntegrationGateway(t, client, handlers, project)
	conn := responsesIntegrationDial(t, gateway)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"synthetic-model","input":"root-canary"}`)))
	var id string
	for {
		_, event, err := conn.ReadMessage()
		require.NoError(t, err)
		kind := gjson.GetBytes(event, "type").String()
		require.NotEqual(t, "error", kind, string(event))
		if kind == "response.created" {
			id = gjson.GetBytes(event, "response.id").String()
		}
		if kind == "response.output_text.delta" {
			break
		}
	}
	require.NotEmpty(t, id)
	lane := <-hook.lanes
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "response.interrupt", "response_id": id, "discard_partial_items": true}))
	for _, expected := range []string{"response.interrupt.accepted", "response.completed"} {
		_, event, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, expected, gjson.GetBytes(event, "type").String(), string(event))
	}
	record := lane.Record()
	require.Equal(t, id, record.ID)
	require.Nil(t, record.Snapshot, "converted providers do not have lossless Responses recovery history")
}

type responsesObserveLaneHook struct{ lanes chan *shared.ResponsesLane }

func (h *responsesObserveLaneHook) PassThroughApplied(context.Context, int) {}
func (h *responsesObserveLaneHook) Middleware(context.Context) (orchestrator.RequestHooks, error) {
	return orchestrator.RequestHooks{Attempt: pipeline.OnRawRequest("observe_converted_lane", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		h.lanes <- shared.GetResponsesLane(ctx)
		return request, nil
	})}, nil
}
