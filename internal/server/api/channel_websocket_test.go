package api

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
)

func fixtureChannelTester(ctx context.Context, client *ent.Client, api *OpenAIHandlers, projectID int) (context.Context, objects.GUID, *orchestrator.TestChannelOrchestrator) {
	ctx = authz.WithTestBypass(ent.NewContext(ctx, client))
	ctx = contexts.WithSource(contexts.WithProjectID(contexts.WithUser(ctx, client.User.Query().OnlyX(ctx)), projectID), request.SourceTest)
	p := api.ChatCompletionHandlers.ChatCompletionOrchestrator
	return ctx, objects.GUID{Type: "Channel", ID: client.Channel.Query().OnlyIDX(ctx)}, orchestrator.NewTestChannelOrchestrator(p.ChannelService, p.RequestService, p.SystemService, p.UsageLogService, p.PromptProtecter.(*biz.PromptProtectionRuleService), httpclient.NewHttpClient(), orchestrator.ChannelTestTimeout(5*time.Second))
}

func TestWSOnlyBusinessHandshakeKeepsNativeBudget(t *testing.T) {
	upstream := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unexpected HTTP fallback")
			w.WriteHeader(405)
			return
		}
		select {
		case <-time.After(3500 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"native-after-3s","status":"completed","output":[]}}`))
	}))
	cl, h, project := newResponsesIntegrationFixture(t, upstream.URL, func(c *ent.ChannelCreate) {
		c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses-ws"}})
	})
	conn := responsesIntegrationDial(t, responsesIntegrationGateway(t, cl, h, project))
	result := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","input":"hello"}`)
	require.Equal(t, "native-after-3s", gjson.GetBytes(result, "response.id").String(), string(result))
}

func TestOrdinaryChannelTestFinalizesStreamAndJSON(t *testing.T) {
	for _, ws := range []bool{false, true} {
		t.Run(fmt.Sprint(ws), func(t *testing.T) {
			var gets, posts atomic.Int32
			up := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					gets.Add(1)
					w.WriteHeader(405)
					return
				}
				posts.Add(1)
				final := `{"id":"r-test","model":"synthetic-model","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`
				if !ws {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, final)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r-test\",\"model\":\"synthetic-model\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"OK\"}\n\n")
				fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", final)
			}))
			cl, h, project := newResponsesIntegrationFixture(t, up.URL, func(c *ent.ChannelCreate) {
				c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", SupportsWebSockets: new(ws)}})
				if ws {
					c.SetSettings(&objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "synthetic-model", APIFormats: []string{"openai/responses-ws"}}}})
				}
			})
			ctx, id, tester := fixtureChannelTester(t.Context(), cl, h, project.ID)
			result, err := tester.TestChannel(ctx, id, new("synthetic-model"), nil)
			require.NoError(t, err)
			require.True(t, result.Success, "%+v", result.Error)
			require.EqualValues(t, 1, posts.Load())
			if ws {
				require.EqualValues(t, 1, gets.Load())
			} else {
				require.Zero(t, gets.Load())
			}
		})
	}
}

func TestVerifiedChannelLosesWebSocketUsesSavedHTTPForNextTurn(t *testing.T) {
	var frames, posts atomic.Int32
	const terminal = `{"type":"response.completed","response":{"id":"proof","object":"response","model":"synthetic-model","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}}`
	ws := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/native" {
			t.Error("unexpected request to WS endpoint")
			w.WriteHeader(405)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		if err != nil {
			return
		}
		if frames.Add(1) == 1 {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(terminal))
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "no available account"), time.Now().Add(time.Second))
	}))
	https := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/stream", r.URL.Path)
		posts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", terminal)
	}))
	cl, h, project := newResponsesIntegrationFixture(t, https.URL, func(c *ent.ChannelCreate) {
		c.SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", BaseURL: https.URL, Path: "/stream"}, {APIFormat: "openai/responses-ws", BaseURL: ws.URL, Path: "/native"}})
	})
	ctx, id, tester := fixtureChannelTester(t.Context(), cl, h, project.ID)
	v, err := tester.VerifyChannelWebSocket(ctx, id, "synthetic-model", false)
	require.NoError(t, err)
	require.Equal(t, "verified", v.State)
	gateway := responsesIntegrationGateway(t, cl, h, project)
	turn := func() string {
		conn := responsesIntegrationDial(t, gateway)
		defer conn.Close()
		reply := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","input":"new independent turn"}`)
		return gjson.GetBytes(reply, "type").String()
	}
	require.Contains(t, []string{"error", "response.failed"}, turn())
	require.EqualValues(t, 2, frames.Load())
	require.Zero(t, posts.Load())
	require.Equal(t, "response.completed", turn())
	require.EqualValues(t, 2, frames.Load())
	require.EqualValues(t, 1, posts.Load())
}

func TestChannelWebSocketVerificationAndRouting(t *testing.T) {
	for _, tc := range []struct {
		mode, state, reason string
		enabled             bool
	}{
		{"refused", "unavailable", "upgrade_rejected", false},
		{"empty", "handshake_only", "empty_output", false},
		{"no_account", "unavailable", "no_available_account", true},
		{"verified", "verified", "verified", true},
		{"verified", "verified", "verified", false},
	} {
		t.Run(fmt.Sprintf("%s_enabled_%t", tc.mode, tc.enabled), func(t *testing.T) {
			var handshakes, posts, frames atomic.Int32
			completed := `{"type":"response.completed","response":{"id":"r-fixture","object":"response","model":"synthetic-model","status":"completed","output":[{"id":"msg","type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}}`
			up := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", completed)
					return
				}
				handshakes.Add(1)
				if tc.mode == "refused" {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for {
					_, raw, err := ws.ReadMessage()
					if err != nil {
						return
					}
					if gjson.GetBytes(raw, "type").String() != "response.create" {
						t.Error("expected response.create")
						return
					}
					frames.Add(1)
					if tc.mode == "no_account" {
						_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "no available account"), time.Now().Add(time.Second))
						return
					}
					if tc.mode == "empty" {
						_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"r-empty","status":"completed","output":[]}}`))
						return
					}
					if ws.WriteMessage(websocket.TextMessage, []byte(completed)) != nil {
						return
					}
				}
			}))
			cl, h, project := newResponsesIntegrationFixture(t, up.URL, func(c *ent.ChannelCreate) {
				ep := []objects.ChannelEndpoint{{APIFormat: "openai/responses"}}
				if tc.enabled {
					ep = append(ep, objects.ChannelEndpoint{APIFormat: "openai/responses-ws"})
				}
				c.SetEndpoints(ep)
			})
			ctx, id, tester := fixtureChannelTester(t.Context(), cl, h, project.ID)
			p := h.ChatCompletionHandlers.ChatCompletionOrchestrator
			unknown, err := tester.WebSocketEvidence(ctx, id, "synthetic-model")
			require.NoError(t, err)
			require.Equal(t, "unknown", unknown.State)
			gateway := responsesIntegrationGateway(t, cl, h, project)
			turn := func() {
				conn := responsesIntegrationDial(t, gateway)
				defer conn.Close()
				reply := responsesIntegrationTurn(t, conn, `{"type":"response.create","model":"synthetic-model","input":"hello"}`)
				require.Equal(t, "r-fixture", gjson.GetBytes(reply, "response.id").String(), string(reply))
			}
			turn()
			require.EqualValues(t, 0, handshakes.Load())
			require.EqualValues(t, 1, posts.Load())
			res, err := tester.VerifyChannelWebSocket(ctx, id, "synthetic-model", false)
			require.NoError(t, err)
			require.Equal(t, tc.reason, res.Reason)
			require.Equal(t, tc.state, res.State)
			require.EqualValues(t, 1, handshakes.Load())
			require.EqualValues(t, 1, posts.Load())
			if tc.mode == "refused" {
				require.Zero(t, frames.Load())
			} else {
				require.EqualValues(t, 1, frames.Load())
			}
			again, err := tester.WebSocketEvidence(ctx, id, "synthetic-model")
			require.NoError(t, err)
			require.Equal(t, res.State, again.State)
			if tc.state == "verified" && !tc.enabled {
				cold := biz.NewChannelService(biz.ChannelServiceParams{Ent: cl, CacheConfig: xcache.Config{Mode: xcache.ModeMemory}, SystemService: p.SystemService, HttpClient: httpclient.NewHttpClient()})
				ch, err := cold.GetChannel(ctx, id.ID)
				require.NoError(t, err)
				logged := cl.RequestExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
				require.NotNil(t, logged.Selection)
				require.Equal(t, ch.WebSocketProfile(), logged.Selection.WebSocketProfile)
				require.NotEmpty(t, logged.Selection.WebSocketEndpoint)
				require.NotNil(t, logged.Selection.WebSocketEvidence, "terminal status %s", logged.Status)
				require.Equal(t, res.State, logged.Selection.WebSocketEvidence.State)
				stored, err := cold.LoadWebSocketEvidence(ctx, ch, "synthetic-model")
				require.NoError(t, err)
				require.Equal(t, "verified", stored.State)
				changed, err := cl.Channel.UpdateOneID(id.ID).SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses", SupportsWebSockets: new(true)}}).Save(ctx)
				require.NoError(t, err)
				require.NotNil(t, changed)
				enabled, err := cold.GetChannel(ctx, id.ID)
				require.NoError(t, err)
				require.Equal(t, ch.WebSocketProfile(), enabled.WebSocketProfile())
				_, err = cl.Channel.UpdateOneID(id.ID).SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "openai/responses"}}).Save(ctx)
				require.NoError(t, err)
			}
			if tc.state == "verified" {
				_, err = tester.VerifyChannelWebSocket(ctx, id, "synthetic-model", true)
				require.NoError(t, err)
				require.EqualValues(t, 1, handshakes.Load())
			}
			turn()
			if tc.enabled && tc.state == "verified" {
				require.EqualValues(t, 2, handshakes.Load())
				require.EqualValues(t, 1, posts.Load())
			} else {
				require.EqualValues(t, 1, handshakes.Load())
				require.EqualValues(t, 2, posts.Load())
			}
		})
	}
}
