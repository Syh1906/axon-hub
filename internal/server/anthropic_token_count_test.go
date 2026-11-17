package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenCountAuthenticatedPipeline(t *testing.T) {
	h := newCodexRouteHarness(t)
	calls := new(atomic.Int32)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1/messages/count_tokens", r.URL.Path)
		assert.Equal(t, "synthetic-upstream", r.Header.Get("X-API-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"input_tokens":27}`)
	}))
	t.Cleanup(upstream.Close)
	source := h.db.Channel.Create().SetName("counter").SetType(channel.TypeAnthropic).SetBaseURL(upstream.URL).SetCredentials(objects.ChannelCredentials{APIKey: "synthetic-upstream"}).SetDefaultTestModel("count-model").SetSupportedModels([]string{"count-model"}).SaveX(h.ctx)
	ch, err := h.channels.GetChannel(h.ctx, source.ID)
	require.NoError(t, err)
	h.channels.SetEnabledChannelsForTest([]*biz.Channel{ch})
	h.registerModels(t, ch, "chat")
	orch := *h.orch
	orch.Inbound = anthropic.NewTokenCountInboundTransformer()
	handler := api.NewChatCompletionHandlers(&orch)
	auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
	for _, path := range []string{"/v1/messages/count_tokens", "/anthropic/v1/messages/count_tokens"} {
		h.router.POST(path, middleware.WithStrictAPIKeyConfig(auth, nil), handler.ChatCompletion)
		for _, testKey := range []string{"", "invalid", h.key.Key} {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"count-model","messages":[{"role":"user","content":"hello"}]}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+testKey)
			rec := httptest.NewRecorder()
			h.router.ServeHTTP(rec, request)
			if testKey != h.key.Key {
				require.Equal(t, 401, rec.Code, rec.Body.String())
			} else {
				require.Equal(t, 200, rec.Code, rec.Body.String())
				require.JSONEq(t, `{"input_tokens":27}`, rec.Body.String())
			}
		}
	}
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, 0, h.db.UsageLog.Query().CountX(h.ctx), "counting must not enter billable usage")
	require.Equal(t, 0, h.db.BudgetPosting.Query().CountX(h.ctx), "counting must not settle a debit")
	require.Equal(t, 2, h.db.Request.Query().CountX(h.ctx), "counts retain request diagnostics")
	restricted := h.db.APIKey.Create().SetName("restricted count").SetKey("ah-count-restricted").SetType(apikey.TypeUser).SetUserID(h.key.UserID).SetProjectID(h.key.ProjectID).SetProfiles(&objects.APIKeyProfiles{ActiveProfile: "only", Profiles: []objects.APIKeyProfile{{Name: "only", ModelIDs: []string{"different-model"}}}}).SaveX(h.ctx)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"count-model","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+restricted.Key)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, request)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "invalid_model_error")
	require.Equal(t, int32(2), calls.Load(), "unauthorized models must not reach the provider")

}
