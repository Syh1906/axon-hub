package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestResponsesVerifiedHTTPUsesSameUpstreamSocket(t *testing.T) {
	verifyHTTPContinuation(t, 36<<20, false, false)
}

func TestResponsesHTTPRecoversSmallHistoryAfterSocketLossWithoutStorage(t *testing.T) {
	verifyHTTPContinuation(t, 256, true, false)
}

func TestHTTPPreviousChannelRemovedFromKeyProfile(t *testing.T) {
	verifyHTTPContinuation(t, 256, false, true)
}

func verifyHTTPContinuation(t *testing.T, payloadSize int, disconnect, switchProfile bool) {
	t.Helper()
	var connections, generations atomic.Int32
	frames := make(chan []byte, 6)
	upstream := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "test upstream expects WebSocket", http.StatusBadRequest)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			id := generations.Add(1)
			frames <- body
			for _, event := range []string{
				fmt.Sprintf(`{"type":"response.created","response":{"id":"r%d","status":"in_progress"}}`, id),
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs","summary":[]}}`,
				`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"hi"}`,
				fmt.Sprintf(`{"type":"response.completed","response":{"id":"r%d","model":"synthetic-model","status":"completed","output":[{"type":"reasoning","id":"rs","summary":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":20,"output_tokens":4}}}`, id),
			} {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
					return
				}
			}
			if id == 4 || disconnect && id == 3 {
				return
			}
		}
	}))
	client, handlers, project := newResponsesIntegrationFixture(t, upstream.URL, func(create *ent.ChannelCreate) {
		create.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}, {APIFormat: "openai/responses-ws"}})
	})
	gateway := responsesIntegrationGateway(t, client, handlers, project)
	if disconnect || switchProfile {
		require.NoError(t, handlers.SystemService.SetStoragePolicy(authz.WithTestBypass(ent.NewContext(t.Context(), client)), &biz.StoragePolicy{}))
	}
	// Native traffic verifies the same model and credential used by HTTP.
	warm := responsesIntegrationDial(t, gateway)
	require.Equal(t, "r1", gjson.GetBytes(responsesIntegrationTurn(t, warm, `{"type":"response.create","model":"synthetic-model","input":"warm"}`), "response.id").String())
	<-frames
	body := `{"model":"synthetic-model","stream":true,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + strings.Repeat("A", payloadSize) + `"}]}]}`
	for turn := 2; turn <= 4; turn++ {
		if switchProfile && turn == 4 {
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
			received := make(chan []byte, 1)
			replacement := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := readRequestBody(r)
				if err != nil {
					t.Error(err)
					return
				}
				received <- data
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: "+`{"type":"response.completed","response":{"id":"r4","status":"completed","output":[]}}`+"\n\n")
			}))
			b := client.Channel.Create().SetType(channel.TypeOpenaiResponses).SetName("B").SetStatus(channel.StatusEnabled).SetBaseURL(replacement.URL).SetSupportedModels([]string{"synthetic-model"}).SetDefaultTestModel("synthetic-model").SetCredentials(objects.ChannelCredentials{APIKey: "fixture-b"}).SaveX(ctx)
			model := client.Model.Query().OnlyX(ctx)
			settings := *model.Settings
			settings.Associations = append(settings.Associations, &objects.ModelAssociation{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: b.ID, ModelID: "synthetic-model"}})
			model.Update().SetSettings(&settings).SaveX(ctx)
			client.APIKey.Update().SetProfiles(&objects.APIKeyProfiles{ActiveProfile: "restricted", Profiles: []objects.APIKeyProfile{{Name: "restricted", ChannelIDs: []int{b.ID}}}}).ExecX(ctx)
			require.NoError(t, handlers.ChannelService.ReloadEnabledChannelsCache(ctx))
			status, answer := responsesIntegrationPost(t, gateway, "/v1/responses", body)
			require.Equal(t, http.StatusOK, status, string(answer))
			require.Contains(t, string(answer), "r4")
			frame := <-received
			require.False(t, gjson.GetBytes(frame, "previous_response_id").Exists())
			require.Contains(t, string(frame), "data:image/png;base64,")
			require.Equal(t, 2, strings.Count(string(frame), `"text":"hi"`))
			require.EqualValues(t, 3, generations.Load())
			return
		}
		status, answer := responsesIntegrationPost(t, gateway, "/v1/responses", body)
		require.Equal(t, http.StatusOK, status, "%.350s", answer)
		require.Contains(t, string(answer), fmt.Sprintf(`"r%d"`, turn))
		frame := <-frames
		t.Logf("HTTP 客户端第 %d 轮，上游业务帧 %d 字节", turn-1, len(frame))
		if turn == 4 && disconnect {
			require.False(t, gjson.GetBytes(frame, "previous_response_id").Exists())
			require.Contains(t, string(frame), "data:image/png;base64,")
			require.Equal(t, 2, strings.Count(string(frame), `"text":"hi"`), "%s", frame)
		} else if turn > 2 {
			require.Equal(t, fmt.Sprintf("r%d", turn-1), gjson.GetBytes(frame, "previous_response_id").String())
			require.Less(t, len(frame), 1024)
		} else {
			require.Greater(t, len(frame), payloadSize)
		}
		body = fmt.Sprintf(`{"model":"synthetic-model","stream":true,"previous_response_id":"r%d","input":[{"role":"user","content":"continue"}]}`, turn)
	}
	require.EqualValues(t, 4, generations.Load())
	wantConnections := 2
	if disconnect {
		wantConnections++
	}
	require.EqualValues(t, wantConnections, connections.Load())
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	row := client.Request.Query().Where(request.ExternalIDEQ("r4")).OnlyX(ctx)
	require.NotNil(t, row.StreamTiming)
	require.Equal(t, "response.output_item.added.reasoning", row.StreamTiming.FirstEvent)
	require.NotNil(t, row.StreamTiming.ContentWriteMs)
	require.NotNil(t, row.StreamTiming.AfterContentMs)
	require.NotNil(t, row.StreamTiming.AnswerWriteMs)
	require.True(t, row.StreamTiming.SingleAnswerBatch)
	require.Nil(t, row.StreamTiming.AnswerTokensPerSecond)
	require.Equal(t, 1, row.StreamTiming.Attempts)
	details, err := biz.RequestOutputDetailsByID(ctx, client, []int{row.ID})
	require.NoError(t, err)
	require.NotNil(t, details[row.ID].Batches)
	require.Equal(t, 1, details[row.ID].Batches.Count)
	require.Zero(t, *details[row.ID].Batches.SpanMs)
}

func TestResponsesProtocolNativeContinuationRetainsCompletedConnection(t *testing.T) {
	for _, provider := range []channel.Type{channel.TypeOpenaiResponses, channel.TypeCodex} {
		t.Run(string(provider), func(t *testing.T) {
			// Given: a production handler and provider with connection-local context.
			var connections atomic.Int32
			frames := make(chan []byte, 2)
			upstream := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				connections.Add(1)
				defer conn.Close()
				if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
					t.Error(err)
					return
				}
				for turn := 0; ; turn++ {
					_, body, err := conn.ReadMessage()
					if err != nil {
						return
					}
					frames <- body
					response := `{"type":"response.completed","response":{"id":"provider-tool","status":"completed","output":[{"type":"function_call","id":"fc-tool","call_id":"call-tool","name":"payload","arguments":"{}"}]}}`
					if turn > 0 {
						response = `{"type":"response.completed","response":{"id":"provider-final","status":"completed","output":[]}}`
					}
					if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
						t.Error(err)
						return
					}
				}
			}))
			client, handlers, project := newResponsesIntegrationFixture(t, upstream.URL, func(create *ent.ChannelCreate) {
				create.SetType(provider)
				create.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses-ws"}})
				create.SetSettings(&objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "synthetic-model", APIFormats: []string{"openai/responses-ws"}}}})
			})
			gateway := responsesIntegrationGateway(t, client, handlers, project)
			conn := responsesIntegrationDial(t, gateway)
			first := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","input":"root-canary"}`)
			require.Equal(t, "provider-tool", gjson.GetBytes(first, "response.id").String(), string(first))
			firstFrame := <-frames
			require.False(t, gjson.GetBytes(firstFrame, "previous_response_id").Exists())
			output := strings.Repeat("x", (2<<20)+3)
			body, err := json.Marshal(map[string]any{
				"type": "response.create", "model": "synthetic-model", "previous_response_id": "provider-tool",
				"input": []map[string]string{{"type": "function_call_output", "call_id": "call-tool", "output": output}},
			})
			require.NoError(t, err)
			// When: the paired large tool output continues the completed native turn.
			completed := responsesIntegrationTurn(t, conn, string(body))
			// Then: the exact previous ID and output reach the same upstream connection.
			require.Equal(t, "provider-final", gjson.GetBytes(completed, "response.id").String(), string(completed))
			secondFrame := <-frames
			require.Equal(t, int32(1), connections.Load())
			require.Equal(t, "provider-tool", gjson.GetBytes(secondFrame, "previous_response_id").String())
			require.Len(t, gjson.GetBytes(secondFrame, "input").Array(), 1)
			require.Equal(t, "call-tool", gjson.GetBytes(secondFrame, "input.0.call_id").String())
			require.Equal(t, sha256.Sum256([]byte(output)), sha256.Sum256([]byte(gjson.GetBytes(secondFrame, "input.0.output").String())))
		})
	}
}

func TestVerifiedHTTPBackgroundAndHandshakeFallbackUseHTTPBody(t *testing.T) {
	for _, mode := range []string{"background", "override", "handshake"} {
		t.Run(mode, func(t *testing.T) {
			var upgrades, posts atomic.Int32
			bodies := make(chan []byte, 1)
			server := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if websocket.IsWebSocketUpgrade(r) {
					if upgrades.Add(1) > 1 {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					if _, _, err := conn.ReadMessage(); err == nil {
						_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"warm","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"warm answer"}]}]}}`))
					}
					return
				}
				posts.Add(1)
				data, err := readRequestBody(r)
				if err != nil {
					t.Error(err)
					return
				}
				bodies <- data
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"result","object":"response","status":"completed","output":[]}`))
			}))
			db, handlers, project := newResponsesIntegrationFixture(t, server.URL, func(c *ent.ChannelCreate) {
				c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}, {APIFormat: "openai/responses-ws"}})
				settings := &objects.ChannelSettings{PassThroughBody: new(true)}
				if mode == "override" {
					settings.BodyOverrideOperations = []objects.OverrideOperation{{Op: "set", Path: "background", Value: "true"}}
				}
				c.SetSettings(settings)
			})
			gateway := responsesIntegrationGateway(t, db, handlers, project)
			warm := responsesIntegrationDial(t, gateway)
			require.Equal(t, "warm", gjson.GetBytes(responsesIntegrationTurn(t, warm, `{"type":"response.create","model":"synthetic-model","input":"warm"}`), "response.id").String())
			fields := `"input":"second","vendor_extension":"keep"`
			if mode == "background" {
				fields += `,"background":true`
			}
			status, _ := responsesIntegrationPost(t, gateway, "/v1/responses", `{"model":"synthetic-model",`+fields+`}`)
			require.Equal(t, http.StatusOK, status)
			require.EqualValues(t, 1, posts.Load())
			request := <-bodies
			require.False(t, gjson.GetBytes(request, "type").Exists())
			require.False(t, gjson.GetBytes(request, "generate").Exists())
			require.Equal(t, "keep", gjson.GetBytes(request, "vendor_extension").String())
			if mode == "handshake" {
				require.EqualValues(t, 2, upgrades.Load())
			} else {
				require.EqualValues(t, 1, upgrades.Load())
				require.True(t, gjson.GetBytes(request, "background").Bool())
			}
		})
	}
}
