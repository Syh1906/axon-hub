package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/llm/transformer/jina"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRerankNegativeUsageDoesNotEnterLedger(t *testing.T) {
	for _, pass := range []bool{false, true} {
		t.Run(fmt.Sprintf("pass-through=%t", pass), func(t *testing.T) {
			h := newCodexRouteHarness(t)
			require.NoError(t, h.system.SetPassThrough(h.ctx, pass))
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"model":"rank","results":[{"index":1,"relevance_score":0.8}],"usage":{"total_tokens":-1}}`)
			}))
			t.Cleanup(upstream.Close)
			row := h.db.Channel.Create().SetName("rank").SetType(channel.TypeJina).SetBaseURL(upstream.URL).
				SetCredentials(objects.ChannelCredentials{APIKey: "synthetic"}).SetDefaultTestModel("rank").SetSupportedModels([]string{"rank"}).SaveX(h.ctx)
			ch, err := h.channels.GetChannel(h.ctx, row.ID)
			require.NoError(t, err)
			h.channels.SetEnabledChannelsForTest([]*biz.Channel{ch})
			h.registerModels(t, ch, "rerank")
			orch := *h.orch
			orch.Inbound = jina.NewRerankInboundTransformer()
			auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
			const path = "/v1/rerank"
			h.router.POST(path, middleware.WithStrictAPIKeyConfig(auth, nil), api.NewChatCompletionHandlers(&orch).ChatCompletion)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"rank","query":"a","documents":["b","a"]}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+h.key.Key)
			rec := httptest.NewRecorder()
			h.router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.EqualValues(t, 1, gjson.Get(rec.Body.String(), "results.0.index").Int())
			require.Zero(t, h.db.UsageLog.Query().CountX(h.ctx))
		})
	}
}
