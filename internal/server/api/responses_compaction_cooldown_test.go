package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesCompactionCooldownAdmission(t *testing.T) {
	const metadata = `{"request_kind":"compaction","session_id":"s","thread_id":"thread","turn_id":"task"}`
	const terminal = `{"type":"response.completed","response":{"id":"resp_ok","object":"response","model":"synthetic-model","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}}}`
	for _, tc := range []struct {
		name, mode string
		ws         bool
	}{{"HTTP", "enforce", false}, {"WS", "enforce", true}, {"observe", "observe", false}} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			server := responsesIntegrationServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				if posts.Add(1) == 1 {
					_, _ = io.WriteString(w, ": heartbeat\n\n")
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
					case <-time.After(5 * time.Second):
					}
					return
				}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", terminal)
			}))
			db, h, proj := newResponsesIntegrationFixture(t, server.URL, func(c *ent.ChannelCreate) {
				c.SetSettings(&objects.ChannelSettings{StreamFirstEventTimeoutSeconds: new(1), PassThroughBody: new(true)})
			})
			h.ResponseCompletionHandlers.ChatCompletionOrchestrator.RequestService.PayloadService = biz.NewRequestPayloadService(biz.RequestPayloadConfig{Directory: t.TempDir(), CompactionCooldownMode: tc.mode})
			site := responsesIntegrationGateway(t, db, h, proj)
			headers := http.Header{}
			headers.Set(codex.TurnMetadataHeader, metadata)
			var conn *websocket.Conn
			if tc.ws {
				var err error
				conn, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(site.URL, "http")+"/v1/responses", headers)
				require.NoError(t, err)
				defer conn.Close()
			}
			send := func(input string, useWS bool) (int, string, string) {
				t.Helper()
				body := fmt.Sprintf(`"client_metadata":{"x-codex-turn-metadata":%q},"input":[{"content":%q,"role":"user"}],"model":"synthetic-model","prompt_cache_key":"fixture-session","stream":true`, metadata, input)
				if useWS {
					b := responsesIntegrationTurn(t, conn, `{"type":"response.create",`+body+"}")
					if gjson.GetBytes(b, "type").String() == "response.completed" {
						return 200, string(b), ""
					}
					return int(gjson.GetBytes(b, "status").Int()), string(b), ""
				}
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, site.URL+"/v1/responses", strings.NewReader("{"+body+"}"))
				require.NoError(t, err)
				request.Header = headers.Clone()
				request.Header.Set("Cache-Control", "max-age=0")
				request.Header.Set("Content-Type", "application/json")
				resp, err := site.Client().Do(request)
				require.NoError(t, err)
				b, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				return resp.StatusCode, string(b), resp.Header.Get("Retry-After")
			}
			status, _, _ := send("first", tc.ws)
			require.Equal(t, 504, status)
			status, body, retryAfter := send("first", tc.ws)
			if tc.mode == "observe" {
				require.Equal(t, 200, status, body)
				require.EqualValues(t, 2, posts.Load())
				return
			}
			require.Equal(t, 503, status, body)
			require.Equal(t, "compaction_cooldown", gjson.Get(body, "error.code").String())
			if !tc.ws {
				require.NotEmpty(t, retryAfter)
			} else {
				status, body, retryAfter = send("first", false)
				require.Equal(t, 503, status, body)
				require.NotEmpty(t, retryAfter)
			}
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), db))
			require.EqualValues(t, 1, posts.Load())
			require.Equal(t, 1, db.RequestExecution.Query().CountX(ctx))
			require.Zero(t, db.UsageLog.Query().CountX(ctx))
			status, body, _ = send("new tool result", tc.ws)
			require.Equal(t, 200, status, body)
			require.EqualValues(t, 2, posts.Load())
		})
	}
}
