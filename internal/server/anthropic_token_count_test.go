package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/promptprotectionrule"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

func TestTokenCountProtectedRoute(t *testing.T) {
	for _, tc := range []struct {
		name        string
		passThrough bool
		mask        bool
	}{{"protected", false, true}, {"protected-pass-through", true, true}, {"alias-pass-through", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCodexRouteHarness(t)
			require.NoError(t, h.system.SetPassThrough(h.ctx, tc.passThrough))
			require.NoError(t, h.system.SetRetryPolicy(h.ctx, &biz.RetryPolicy{Enabled: true, EmptyResponseDetection: true, MaxChannelRetries: 1, LoadBalancerStrategy: "failover"}))
			text := "sample-label"
			if tc.mask {
				rule, err := h.protect.CreateRule(h.ctx, ent.CreatePromptProtectionRuleInput{Name: "mask-label", Pattern: text, Settings: &objects.PromptProtectionSettings{Action: objects.PromptProtectionActionMask, Replacement: "masked"}})
				require.NoError(t, err)
				_, err = h.protect.UpdateRuleStatus(h.ctx, rule.ID, promptprotectionrule.StatusEnabled)
				require.NoError(t, err)
				require.Eventually(t, func() bool {
					rules, err := h.protect.ListEnabledRules(h.ctx)
					return err == nil && len(rules) == 1
				}, 3*time.Second, 10*time.Millisecond)
				text = "masked"
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/v1/messages/count_tokens", r.URL.Path)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, "native", gjson.GetBytes(body, "model").String())
				assert.Equal(t, text, gjson.GetBytes(body, "messages.0.content.0.text").String())
				assert.Equal(t, text, gjson.GetBytes(body, "system").String())
				assert.Equal(t, "ephemeral", gjson.GetBytes(body, "messages.0.content.0.cache_control.type").String())
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"input_tokens":0}`)
			}))
			t.Cleanup(upstream.Close)
			makeChannel := func(name string, kind channel.Type) *biz.Channel {
				row := h.db.Channel.Create().SetName(name).SetType(kind).SetBaseURL(upstream.URL).
					SetCredentials(objects.ChannelCredentials{APIKey: "synthetic"}).SetDefaultTestModel("native").SetSupportedModels([]string{"native"}).SaveX(h.ctx)
				ch, err := h.channels.GetChannel(h.ctx, row.ID)
				require.NoError(t, err)
				return ch
			}
			bedrock, direct := makeChannel("bedrock", channel.TypeAnthropicAWS), makeChannel("direct", channel.TypeAnthropic)
			h.channels.SetEnabledChannelsForTest([]*biz.Channel{bedrock, direct})
			h.registerModels(t, direct, "chat")
			h.db.Model.UpdateOne(h.db.Model.Query().OnlyX(h.ctx)).SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{
				{Type: "channel_model", Priority: 0, ChannelModel: &objects.ChannelModelAssociation{ChannelID: bedrock.ID, ModelID: "native"}},
				{Type: "channel_model", Priority: 1, ChannelModel: &objects.ChannelModelAssociation{ChannelID: direct.ID, ModelID: "native"}},
			}}).SaveX(h.ctx)
			key := h.db.APIKey.Create().SetName("alias").SetKey("ah-count-test-alias").SetType(apikey.TypeUser).SetUserID(h.key.UserID).SetProjectID(h.key.ProjectID).
				SetProfiles(&objects.APIKeyProfiles{ActiveProfile: "alias", Profiles: []objects.APIKeyProfile{{Name: "alias", ModelMappings: []objects.ModelMapping{{From: "public", To: "native"}}}}}).SaveX(h.ctx)
			orch := *h.orch
			orch.Inbound = anthropic.NewTokenCountInboundTransformer()
			auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
			const path = "/v1/messages/count_tokens"
			h.router.POST(path, middleware.WithStrictAPIKeyConfig(auth, nil), api.NewChatCompletionHandlers(&orch).ChatCompletion)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"public","system":"sample-label","messages":[{"role":"user","content":[{"type":"text","text":"sample-label","cache_control":{"type":"ephemeral"}}]}]}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+key.Key)
			rec := httptest.NewRecorder()
			h.router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.JSONEq(t, `{"input_tokens":0}`, rec.Body.String())
			require.EqualValues(t, 1, calls.Load())
			require.Zero(t, h.db.UsageLog.Query().CountX(h.ctx))
			require.Equal(t, direct.ID, h.db.RequestExecution.Query().OnlyX(h.ctx).ChannelID)
		})
	}
}
