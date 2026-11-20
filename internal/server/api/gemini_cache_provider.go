package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/gemini"
)

type cacheTarget struct {
	channel          *biz.Channel
	base, key, model string
	client           *httpclient.HttpClient
}

func (h *GeminiHandlers) cacheTarget(ctx context.Context, key *ent.APIKey, model string, row *ent.GeminiCache) (*cacheTarget, error) {
	if p := key.GetActiveProfile(); p != nil && len(p.ModelIDs) > 0 && !slices.Contains(p.ModelIDs, model) {
		return nil, &cacheFailure{403, "Model is not authorized"}
	}
	mapped := key.GetActiveProfile().MapModel(model)
	req := &llm.Request{Model: mapped, APIFormat: llm.APIFormatGeminiContents, RequestType: llm.RequestTypeChat}
	candidates, err := authz.RunAsHost(ctx, ent.FromContext(ctx), "gemini-cache-model-routing", func(host context.Context) ([]*orchestrator.ChannelModelsCandidate, error) {
		host = contexts.WithAPIKey(host, key)
		return h.selector.Select(contexts.WithProjectID(host, key.ProjectID), req)
	})
	if err != nil {
		return nil, &cacheFailure{503, "No eligible route for cache model"}
	}
	capable := false
	for _, cand := range candidates {
		ch := cand.Channel
		if ch.Type != channel.TypeGemini || !slices.Contains(h.cacheConfig.ChannelIDs, ch.ID) {
			continue
		}
		if ch.Settings != nil && !ch.Settings.ProtocolPolicy.AllowsClient(llm.APIFormatGeminiContents) {
			continue
		}
		base := geminiCacheBase(ch)
		if base == "" {
			continue
		}
		if row != nil && (row.ChannelID != ch.ID || row.BaseURL != base) {
			continue
		}
		for i, entry := range cand.Models {
			if cand.ModelAPIFormat(i) != string(llm.APIFormatGeminiContents) {
				continue
			}
			capable = true
			if row != nil && row.UpstreamModel != entry.ActualModel {
				continue
			}
			for _, secret := range ch.GetEnabledAPIKeys() {
				if row != nil && row.KeyRef != upstream.KeyRef(secret) {
					continue
				}
				client := ch.HTTPClient
				if client == nil {
					client = h.client
				}
				return &cacheTarget{channel: ch, base: base, key: secret, model: entry.ActualModel, client: client}, nil
			}
		}
	}
	if row != nil || capable || len(candidates) == 0 {
		return nil, &cacheFailure{503, "Cache owner route unavailable"}
	}
	return nil, &cacheFailure{501, "No selected Gemini channel declares explicit cache support"}
}

func geminiCacheBase(ch *biz.Channel) string {
	for _, ep := range ch.ResolveEndpoints() {
		if ep.APIFormat != string(llm.APIFormatGeminiContents) || ep.Path != "" {
			continue
		}
		base := ep.BaseURL
		if base == "" {
			base = ch.BaseURL
		}
		if base == "" {
			base = gemini.DefaultBaseURL
		}
		if a := ch.EnabledUpstreamAddresses(ep.APIFormat); len(a) > 0 {
			base = a[0].URL
		}
		base = strings.TrimRight(base, "/")
		if strings.HasSuffix(base, "/v1") {
			continue
		}
		base = strings.TrimSuffix(base, "/v1beta") + "/v1beta"
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			continue
		}
		return base
	}
	return ""
}

const cacheOperationTimeout = 30 * time.Second

var cacheNamePattern = regexp.MustCompile(`^cachedContents/[A-Za-z0-9_-]+$`)

type cacheMetadata struct {
	Name        string    `json:"name"`
	Model       string    `json:"model"`
	DisplayName string    `json:"displayName,omitempty"`
	CreateTime  string    `json:"createTime,omitempty"`
	UpdateTime  string    `json:"updateTime,omitempty"`
	ExpireTime  time.Time `json:"expireTime"`
	Usage       struct {
		Tokens *int64 `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

func (t *cacheTarget) send(ctx context.Context, method, path string, body []byte) (cacheMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, cacheOperationTimeout)
	defer cancel()
	var m cacheMetadata
	req := &httpclient.Request{
		Method: method, URL: t.base + "/" + path, Body: body,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Auth:    &httpclient.AuthConfig{Type: httpclient.AuthTypeAPIKey, HeaderKey: "x-goog-api-key", APIKey: t.key},
	}
	r, err := t.client.WithRejectRedirects().Do(ctx, req)
	if err != nil {
		var e *httpclient.Error
		if errors.As(err, &e) && e.StatusCode >= 400 && e.StatusCode <= 599 {
			return m, &cacheFailure{e.StatusCode, "Provider rejected cache operation"}
		}
		return m, &cacheFailure{502, "Cache provider unavailable"}
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return m, &cacheFailure{502, "Unexpected cache provider response"}
	}
	if method == "DELETE" {
		return m, nil
	}
	if err := json.Unmarshal(r.Body, &m); err != nil || !cacheNamePattern.MatchString(m.Name) || m.Model != "models/"+t.model || m.ExpireTime.IsZero() || m.Usage.Tokens == nil || *m.Usage.Tokens < 0 {
		return m, &cacheFailure{502, "Invalid cache metadata from provider"}
	}
	if method != "POST" && strings.SplitN(path, "?", 2)[0] != m.Name {
		return m, &cacheFailure{502, "Provider returned a different cache"}
	}
	return m, nil
}
func (t *cacheTarget) discardCreated(ctx context.Context, name string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := t.send(cleanup, http.MethodDelete, name, nil); err != nil {
		log.Warn(cleanup, "Could not remove unregistered Gemini cache", log.Int("channel_id", t.channel.ID), log.String("cache", name), log.Cause(err))
	}
}
