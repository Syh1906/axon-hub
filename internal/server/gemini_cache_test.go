package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/api"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/internal/server/middleware"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestGeminiExplicitCache(t *testing.T) {
	h := newCodexRouteHarness(t)
	var mu sync.Mutex
	var calls atomic.Int32
	var zeroTokens, missingAtProvider atomic.Bool
	var discarded atomic.Int32
	slowPatchApplied, releasePatch := make(chan struct{}), make(chan struct{})
	owner := ""
	expiry := time.Now().UTC().Add(time.Minute)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls.Add(1)
		account := r.Header.Get("x-goog-api-key")
		if owner == "" {
			owner = r.URL.Path[:2] + "/" + account
		}
		if owner != r.URL.Path[:2]+"/"+account {
			http.Error(w, "wrong origin", 409)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		isStream := strings.HasSuffix(r.URL.Path, ":streamGenerateContent")
		if strings.HasSuffix(r.URL.Path, ":generateContent") || isStream {
			if gjson.GetBytes(body, "cachedContent").String() != "cachedContents/provider123" {
				http.Error(w, "missing cache", 400)
				return
			}
			const result = `{"candidates":[{"content":{"role":"model","parts":[{"text":"hit"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":15,"cachedContentTokenCount":12,"candidatesTokenCount":2,"totalTokenCount":17}}`
			if isStream {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", result)
			} else {
				fmt.Fprint(w, result)
			}
			return
		}
		if r.Method == "PATCH" {
			if gjson.GetBytes(body, "ttl").String() == "3599s" {
				expiry = time.Now().UTC().Add(3599 * time.Second)
				http.Error(w, "upstream result unavailable", http.StatusBadGateway)
				return
			}
			expiry = expiry.Add(time.Minute)
			if gjson.GetBytes(body, "ttl").String() == "123s" {
				fmt.Fprint(w, "{")
				return
			}
		}
		if r.Method == "DELETE" {
			discarded.Add(1)
			fmt.Fprint(w, "{}")
			return
		}
		atResponse := expiry
		if r.Method == "PATCH" && gjson.GetBytes(body, "ttl").String() == "121s" {
			close(slowPatchApplied)
			mu.Unlock()
			select {
			case <-releasePatch:
			case <-r.Context().Done():
			}
			mu.Lock()
		}
		if missingAtProvider.Load() {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":{"message":"CachedContent not found (or permission denied)"}}`)
			return
		}
		tokens := 12
		if zeroTokens.Load() {
			tokens = 0
		}
		json.NewEncoder(w).Encode(map[string]any{"name": "cachedContents/provider123", "model": "models/cached-m", "expireTime": atResponse, "usageMetadata": map[string]int{"totalTokenCount": tokens}})
	}))
	defer local.Close()
	var channels []*biz.Channel
	var ids []int
	for _, name := range []string{"a", "b"} {
		source := h.db.Channel.Create().SetType(channel.TypeGemini).SetName(name).SetBaseURL(local.URL + "/" + name).
			SetCredentials(objects.ChannelCredentials{APIKeys: []string{name + "1", name + "2"}}).SetSupportedModels([]string{"cached-m"}).SetDefaultTestModel("cached-m").SaveX(h.ctx)
		ch, err := h.channels.GetChannel(h.ctx, source.ID)
		require.NoError(t, err)
		channels = append(channels, ch)
		ids = append(ids, source.ID)
	}
	h.channels.SetEnabledChannelsForTest(channels)
	h.registerModels(t, channels[0], "chat")
	registered := h.db.Model.Query().OnlyX(h.ctx)
	registered.Update().SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{
		{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ids[0], ModelID: "cached-m"}},
		{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ids[1], ModelID: "cached-m"}},
	}}).SaveX(h.ctx)
	second := h.db.APIKey.Create().SetKey("ah-second").SetName("second").SetType(apikey.TypeUser).SetProjectID(h.key.ProjectID).SetUserID(h.key.UserID).SaveX(h.ctx)
	restricted := h.db.APIKey.Create().SetKey("ah-restricted").SetName("restricted").SetProjectID(h.key.ProjectID).SetUserID(h.key.UserID).
		SetProfiles(&objects.APIKeyProfiles{ActiveProfile: "limited", Profiles: []objects.APIKeyProfile{{Name: "limited", ChannelTags: []string{"excluded-fixture-tag"}}}}).SaveX(h.ctx)
	auth := biz.NewAuthService(biz.AuthServiceParams{Ent: h.db, SystemService: h.system, APIKeyService: h.keys})
	handler := api.NewGeminiHandlers(api.GeminiHandlersParams{ModelService: h.openAI.ModelService, ChannelService: h.channels, DefaultSelector: orchestrator.NewDefaultSelector(h.channels, h.openAI.ModelService, h.system), RequestService: h.requests, SystemService: h.system, UsageLogService: h.usage, PromptService: h.prompts, PromptProtectionRuleService: h.protect, HttpClient: httpclient.NewHttpClient(), LiveStreamRegistry: biz.NewLiveStreamRegistry(), ChannelLimiterManager: orchestrator.NewChannelLimiterManager()})
	config := api.GeminiCacheConfig{APIKeyIDs: []int{h.key.ID, second.ID, restricted.ID}, ChannelIDs: ids}
	router := func(config api.GeminiCacheConfig) *gin.Engine {
		e := gin.New()
		e.Use(middleware.WithEntClient(h.db))
		registerGeminiRoutes(&Server{Engine: e, Config: Config{GeminiExplicitCache: config, LLMRequestTimeout: 5 * time.Second}}, handler, Services{AuthService: auth, SystemService: h.system, RequestService: h.requests})
		return e
	}
	engine := router(config)
	call := func(method, path, key, body string, status int) gjson.Result {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Goog-Api-Key", key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
			for line := range strings.SplitSeq(w.Body.String(), "\n") {
				if data, ok := strings.CutPrefix(line, "data:"); ok && gjson.Get(data, "usageMetadata.cachedContentTokenCount").Exists() {
					return gjson.Parse(data)
				}
			}
		}
		return gjson.Parse(w.Body.String())
	}
	const collection = "/v1beta/cachedContents"
	const create = `{"model":"models/cached-m","contents":[{"parts":[{"text":"context"}]}],"ttl":"60s"}`
	engine = router(api.GeminiCacheConfig{})
	call("POST", collection, h.key.Key, create, 403)
	engine = router(api.GeminiCacheConfig{APIKeyIDs: config.APIKeyIDs})
	call("POST", collection, h.key.Key, create, 501)
	engine = router(config)
	call("POST", collection, restricted.Key, create, 503)
	call("POST", collection, h.key.Key, strings.Replace(create, "models/cached-m", "models/publisher/cached-m", 1), 400)
	require.Zero(t, calls.Load())
	item := call("POST", collection, h.key.Key, create, 200)
	name := item.Get("name").String()
	require.NotEqual(t, "cachedContents/provider123", name)
	require.EqualValues(t, 12, item.Get("usageMetadata.totalTokenCount").Int())
	path := "/gemini/v1beta/" + name
	generate := fmt.Sprintf(`{"cachedContent":%q,"contents":[{"role":"user","parts":[{"text":"use"}]}]}`, name)
	genPath := "/v1beta/models/cached-m:generateContent"
	engine = router(config) // recover the durable binding through a fresh handler copy
	// An address with a higher priority is added while the creating address remains enabled.
	bound := h.db.GeminiCache.Query().OnlyX(h.ctx)
	reordered := make([]*biz.Channel, 0, len(channels))
	for _, ch := range channels {
		if ch.ID != bound.ChannelID {
			reordered = append(reordered, ch)
			continue
		}
		h.db.Channel.UpdateOneID(ch.ID).SetSettings(&objects.ChannelSettings{UpstreamAddresses: []objects.ChannelUpstreamAddress{
			{ID: "new", URL: local.URL + "/fallback", Enabled: true, Priority: 1},
			{ID: "owner", URL: bound.BaseURL, Enabled: true, Priority: 2},
		}}).SaveX(h.ctx)
		changed, err := h.channels.GetChannel(h.ctx, ch.ID)
		require.NoError(t, err)
		reordered = append(reordered, changed)
	}
	h.channels.SetEnabledChannelsForTest(reordered)
	require.Empty(t, call("GET", collection, second.Key, "", 200).Get("cachedContents").Array())
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		call(method, path, second.Key, `{"ttl":"120s"}`, 404)
	}
	call("POST", genPath, second.Key, generate, 404)
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, name, call("GET", path, h.key.Key, "", 200).Get("name").String())
	require.Equal(t, name, call("GET", collection, h.key.Key, "", 200).Get("cachedContents.0.name").String())
	patched := call("PATCH", path+"?updateMask=ttl", h.key.Key, `{"ttl":"120s"}`, 200)
	require.True(t, patched.Get("expireTime").Time().After(item.Get("expireTime").Time()))
	beforeReadback := calls.Load()
	recovered := call("PATCH", path, h.key.Key, `{"ttl":"123s"}`, 200)
	require.True(t, recovered.Get("expireTime").Time().After(patched.Get("expireTime").Time()))
	require.Equal(t, beforeReadback+2, calls.Load()) // one renewal and one owner-side GET
	// Let the provider apply two renewals but deliver their replies in reverse order.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- nil
			}
		}()
		r := httptest.NewRequest("PATCH", path, strings.NewReader(`{"ttl":"121s"}`))
		r.Header.Set("X-Goog-Api-Key", h.key.Key)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		done <- w
	}()
	select {
	case <-slowPatchApplied:
	case <-time.After(5 * time.Second):
		t.Fatal("first renewal did not reach provider")
	}
	newer := call("PATCH", path, h.key.Key, `{"ttl":"122s"}`, 200)
	close(releasePatch)
	earlier := <-done
	require.NotNil(t, earlier)
	require.Equal(t, 200, earlier.Code, earlier.Body.String())
	current := call("GET", collection, h.key.Key, "", 200).Get("cachedContents.0.expireTime").Time()
	require.Equal(t, newer.Get("expireTime").Time(), current)
	usage := call("POST", genPath, h.key.Key, generate, 200)
	require.EqualValues(t, 12, usage.Get("usageMetadata.cachedContentTokenCount").Int())
	require.EqualValues(t, 12, h.db.UsageLog.Query().OnlyX(h.ctx).PromptCachedTokens)
	stream := call("POST", "/v1beta/models/cached-m:streamGenerateContent?alt=sse", h.key.Key, generate, 200)
	require.EqualValues(t, 12, stream.Get("usageMetadata.cachedContentTokenCount").Int())
	for _, ch := range channels {
		if ch.ID != bound.ChannelID {
			h.channels.SetEnabledChannelsForTest([]*biz.Channel{ch})
		}
	}
	before := calls.Load()
	call("POST", genPath, h.key.Key, generate, 503)
	call("PATCH", path, h.key.Key, `{"ttl":"120s"}`, 503)
	require.Equal(t, before, calls.Load())
	h.channels.SetEnabledChannelsForTest(channels)
	// The channel still serves the same model, but its creating provider key was removed.
	remaining := make([]*biz.Channel, 0, len(channels))
	for _, ch := range channels {
		if ch.ID != bound.ChannelID {
			remaining = append(remaining, ch)
			continue
		}
		keys := ch.GetEnabledAPIKeys()
		var other string
		for _, key := range keys {
			if upstream.KeyRef(key) != bound.KeyRef {
				other = key
				break
			}
		}
		require.NotEmpty(t, other)
		h.db.Channel.UpdateOneID(ch.ID).SetCredentials(objects.ChannelCredentials{APIKey: other}).SaveX(h.ctx)
		changed, err := h.channels.GetChannel(h.ctx, ch.ID)
		require.NoError(t, err)
		remaining = append(remaining, changed)
		h.db.Channel.UpdateOneID(ch.ID).SetCredentials(ch.Credentials).SaveX(h.ctx)
	}
	h.channels.SetEnabledChannelsForTest(remaining)
	call("POST", genPath, h.key.Key, generate, 503)
	call("DELETE", path, h.key.Key, "", 503)
	require.Equal(t, before, calls.Load())
	h.channels.SetEnabledChannelsForTest(channels)
	engine = router(api.GeminiCacheConfig{APIKeyIDs: config.APIKeyIDs})
	call("GET", path, h.key.Key, "", 503)
	call("POST", genPath, h.key.Key, generate, 503)
	engine = router(config)
	call("DELETE", path, h.key.Key, "", 200)
	call("POST", genPath, h.key.Key, generate, 404)
	item = call("POST", collection, h.key.Key, create, 200)
	uncertainPath := "/v1beta/" + item.Get("name").String()
	beforeUnknown := calls.Load()
	unknown := call("PATCH", uncertainPath, h.key.Key, `{"ttl":"3599s"}`, 502)
	require.Equal(t, "GEMINI_CACHE_RENEWAL_OUTCOME_UNKNOWN", unknown.Get("error.details.0.reason").String())
	require.Equal(t, beforeUnknown+1, calls.Load())
	row := h.db.GeminiCache.Query().OnlyX(h.ctx)
	require.NotNil(t, row.RetainUntil)
	row.Update().SetExpireTime(time.Now().UTC().Add(-10 * time.Minute)).SaveX(h.ctx)
	require.Equal(t, "Cached content not found", call("GET", "/v1beta/"+item.Get("name").String(), h.key.Key, "", 404).Get("error.message").String())
	call("POST", genPath, h.key.Key, strings.Replace(generate, name, item.Get("name").String(), 1), 404)
	require.Empty(t, call("GET", collection, h.key.Key, "", 200).Get("cachedContents").Array())
	item = call("POST", collection, h.key.Key, create, 200)
	deleteCalls := discarded.Load()
	call("DELETE", uncertainPath, h.key.Key, "", 200)
	require.Equal(t, deleteCalls+1, discarded.Load())
	missingAtProvider.Store(true)
	call("GET", "/v1beta/"+item.Get("name").String(), h.key.Key, "", 404)
	require.Empty(t, call("GET", collection, h.key.Key, "", 200).Get("cachedContents").Array())
	missingAtProvider.Store(false)
	deletions := discarded.Load()
	zeroTokens.Store(true)
	call("POST", collection, h.key.Key, create, 502)
	require.Equal(t, deletions+1, discarded.Load())
	require.Empty(t, call("GET", collection, h.key.Key, "", 200).Get("cachedContents").Array())
}
