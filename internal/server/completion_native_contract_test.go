package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompletionNativeHTTPContract(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, withUsage := range []bool{false, true} {
			name := "json"
			if stream {
				name = "stream"
			}
			if withUsage {
				name += "-usage"
			} else {
				name += "-unknown"
			}
			t.Run(name, func(t *testing.T) {
				h := newCodexRouteHarness(t)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					assert.NoError(t, err)
					assert.Equal(t, "/v1/completions", r.URL.Path)
					assert.JSONEq(t, `["hello","world"]`, gjson.GetBytes(body, "prompt").Raw)
					response := `{"id":"batch","object":"text_completion","model":"completion-model","choices":[{"text":"A","index":0,"finish_reason":"stop"},{"text":"B","index":1,"finish_reason":"stop"}]`
					if withUsage {
						response += `,"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}`
					}
					response += "}"
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: "+response+"\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, response)
					}
				}))
				t.Cleanup(upstream.Close)
				source := h.db.Channel.Create().SetName("completion").SetType(channel.TypeOpenai).SetBaseURL(upstream.URL).SetCredentials(objects.ChannelCredentials{APIKey: "synthetic"}).SetDefaultTestModel("completion-model").SetSupportedModels([]string{"completion-model"}).SetEndpoints([]objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAICompletion.String()}}).SetSettings(&objects.ChannelSettings{PassThroughBody: lo.ToPtr(false)}).SaveX(h.ctx)
				ch, err := h.channels.GetChannel(h.ctx, source.ID)
				require.NoError(t, err)
				h.channels.SetEnabledChannelsForTest([]*biz.Channel{ch})
				h.registerModels(t, ch, "chat")
				orch := *h.orch
				orch.Inbound = openai.NewCompletionInboundTransformer()
				h.openAI.CompletionHandlers = api.NewChatCompletionHandlers(&orch)
				auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
				h.router.POST("/v1/completions", middleware.WithStrictAPIKeyConfig(auth, nil), h.openAI.Completion)
				streamValue := "false"
				if stream {
					streamValue = "true"
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"completion-model","prompt":["hello","world"],"stream":`+streamValue+`}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+h.key.Key)
				rec := httptest.NewRecorder()
				h.router.ServeHTTP(rec, req)
				require.Equal(t, 200, rec.Code, rec.Body.String())
				body := rec.Body.String()
				if stream {
					require.Contains(t, body, "[DONE]")
					for _, line := range strings.Split(body, "\n") {
						if strings.HasPrefix(line, "data: ") && !strings.Contains(line, "[DONE]") {
							body = strings.TrimPrefix(line, "data: ")
							break
						}
					}
				}
				require.Equal(t, withUsage, gjson.Get(body, "usage").Exists(), body)
				require.Equal(t, int64(2), gjson.Get(body, "choices.#").Int(), body)
			})
		}
	}
}
