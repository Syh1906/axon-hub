package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

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
		ownerBase := ""
		if row != nil {
			ownerBase = row.BaseURL
		}
		base := ch.GeminiCacheBase(ownerBase)
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

const cacheOperationTimeout = 30 * time.Second

var errInvalidCacheMetadata = &cacheFailure{http.StatusBadGateway, "Invalid cache metadata from provider"}

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
			if method != http.MethodPost && cacheResourceMissing(e) {
				return m, errCacheMissing
			}
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
	if err := json.Unmarshal(r.Body, &m); err != nil || !cacheNamePattern.MatchString(m.Name) || m.Model != "models/"+t.model || m.ExpireTime.IsZero() || m.Usage.Tokens == nil || *m.Usage.Tokens <= 0 {
		return m, errInvalidCacheMetadata
	}
	if method != "POST" && strings.SplitN(path, "?", 2)[0] != m.Name {
		return m, errInvalidCacheMetadata
	}
	if !m.ExpireTime.After(time.Now()) {
		if method == "POST" {
			return m, &cacheFailure{502, "Provider returned an expired cache"}
		}
		return m, errCacheMissing
	}
	return m, nil
}

// AI Studio and Vertex-compatible gateways can use different statuses for expired resources.
func cacheResourceMissing(e *httpclient.Error) bool {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(e.Body, &response) != nil {
		return false
	}
	msg := strings.ToLower(response.Error.Message)
	switch e.StatusCode {
	case http.StatusForbidden:
		return strings.Contains(msg, "cachedcontent not found")
	case http.StatusBadRequest:
		return strings.Contains(msg, "invalid resource state for cache content")
	case http.StatusNotFound:
		return strings.Contains(msg, "not found") && (strings.Contains(msg, "cached content") || strings.Contains(msg, "cachedcontent"))
	}
	return false
}
func (t *cacheTarget) discardCreated(ctx context.Context, name string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := t.send(cleanup, http.MethodDelete, name, nil); err != nil {
		log.Warn(cleanup, "Could not remove unregistered Gemini cache", log.Int("channel_id", t.channel.ID), log.String("cache", name), log.Cause(err))
	}
}
