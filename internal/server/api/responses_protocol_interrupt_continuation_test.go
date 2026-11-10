package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestResponsesProtocolInterruptedIDContinuation(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, afterTool := range []bool{false, true} {
			name := "http"
			if native {
				name = "ws"
			}
			if afterTool {
				name += "/after_tool"
			} else {
				name += "/first_turn"
			}
			t.Run(name, func(t *testing.T) {
				// Given the production gateway and a provider interrupted after visible output.
				var requests, connections atomic.Int32
				frames := make(chan []byte, 5)
				interruptedTurn := int32(1)
				if afterTool {
					interruptedTurn = 2
				}
				events := func(turn int32) []string {
					if turn == interruptedTurn {
						return []string{
							`{"type":"response.created","response":{"id":"interrupted","object":"response","model":"synthetic-model","status":"in_progress","output":[]}}`,
							`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"partial","role":"assistant","status":"in_progress","content":[]}}`,
							`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"partial","delta":"discard-this-partial"}`,
						}
					}
					if afterTool && turn == 1 {
						return []string{`{"type":"response.completed","response":{"id":"tool-parent","status":"completed","output":[{"type":"function_call","id":"fc1","call_id":"call1","name":"lookup","arguments":"{}"}]}}`}
					}
					return []string{`{"type":"response.completed","response":{"id":"recovered","status":"completed","output":[]}}`}
				}
				upstream := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if native {
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
							turn := requests.Add(1)
							for _, event := range events(turn) {
								if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
									return
								}
							}
						}
					}
					body, err := readRequestBody(r)
					if err != nil {
						t.Error(err)
						return
					}
					frames <- body
					turn := requests.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					for _, event := range events(turn) {
						if _, err := w.Write([]byte("data: " + event + "\n\n")); err != nil {
							return
						}
					}
					w.(http.Flusher).Flush()
					if turn == interruptedTurn {
						<-r.Context().Done()
					}
				}))
				client, handlers, project := newResponsesIntegrationFixture(t, upstream.URL, func(create *ent.ChannelCreate) {
					if native {
						create.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses-ws"}})
						create.SetSettings(&objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "synthetic-model", APIFormats: []string{"openai/responses-ws"}}}})
					}
				})
				gateway := responsesIntegrationGateway(t, client, handlers, project)
				conn := responsesIntegrationDial(t, gateway)
				input := `{"type":"response.create","stream_id":"source","model":"synthetic-model","store":false,"input":"root-canary"}`
				if afterTool {
					first := responsesIntegrationTurn(t, conn, input)
					require.Equal(t, "tool-parent", gjson.GetBytes(first, "response.id").String(), string(first))
					<-frames
					input = `{"type":"response.create","stream_id":"source","model":"synthetic-model","store":false,"previous_response_id":"tool-parent","input":[{"type":"function_call_output","call_id":"call1","output":"tool-result-canary"}]}`
				}
				require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
				require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(input)))
				for {
					_, event, err := conn.ReadMessage()
					require.NoError(t, err)
					kind := gjson.GetBytes(event, "type").String()
					require.NotEqual(t, "error", kind, string(event))
					if kind == "response.output_text.delta" {
						break
					}
				}
				interruptedFrame := <-frames
				if native && afterTool {
					require.Equal(t, "tool-parent", gjson.GetBytes(interruptedFrame, "previous_response_id").String())
					require.NotContains(t, string(interruptedFrame), "root-canary")
				}
				// When the client explicitly discards the partial item and continues from that ID.
				require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"interrupted","discard_partial_items":true}`)))
				for _, kind := range []string{"response.interrupt.accepted", "response.completed"} {
					_, event, err := conn.ReadMessage()
					require.NoError(t, err)
					require.Equal(t, kind, gjson.GetBytes(event, "type").String(), string(event))
					if kind == "response.completed" {
						require.Equal(t, "interrupted", gjson.GetBytes(event, "response.incomplete_details.reason").String())
						require.Empty(t, gjson.GetBytes(event, "response.output").Array())
					}
				}
				next := responsesIntegrationTurn(t, conn, `{"type":"response.create","stream_id":"source","model":"synthetic-model","store":false,"previous_response_id":"interrupted","input":"next-canary"}`)
				// Then a single fresh request contains the complete history and no partial output.
				require.Equal(t, "recovered", gjson.GetBytes(next, "response.id").String(), string(next))
				recovered := <-frames
				require.False(t, gjson.GetBytes(recovered, "previous_response_id").Exists(), string(recovered))
				require.Contains(t, string(recovered), "root-canary")
				require.Contains(t, string(recovered), "next-canary")
				require.NotContains(t, string(recovered), "discard-this-partial")
				if afterTool {
					require.Contains(t, string(recovered), "tool-result-canary")
					require.Equal(t, 2, strings.Count(string(recovered), `"call_id":"call1"`))
				}
				require.Equal(t, interruptedTurn+1, requests.Load())
				if native {
					require.Equal(t, int32(2), connections.Load())
				}
				// The new chain immediately returns to incremental sends on the healthy socket.
				final := responsesIntegrationTurn(t, conn, `{"type":"response.create","stream_id":"source","model":"synthetic-model","store":false,"previous_response_id":"recovered","input":"final-canary"}`)
				require.Equal(t, "response.completed", gjson.GetBytes(final, "type").String(), string(final))
				last := <-frames
				if native {
					require.Equal(t, "recovered", gjson.GetBytes(last, "previous_response_id").String())
					require.NotContains(t, string(last), "root-canary")
					require.Equal(t, int32(2), connections.Load())
				}
				require.True(t, json.Valid(last))
			})
		}
	}
}
