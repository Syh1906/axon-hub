package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

type geminiCacheContextKey struct{}

func WithGeminiCache(ctx context.Context, row *ent.GeminiCache) context.Context {
	return context.WithValue(ctx, geminiCacheContextKey{}, row)
}
func boundGeminiCache(ctx context.Context) *ent.GeminiCache {
	row, _ := ctx.Value(geminiCacheContextKey{}).(*ent.GeminiCache)
	return row
}

var errCacheRoute = &llm.ResponseError{StatusCode: 503, Detail: llm.ErrorDetail{Message: "Gemini cache owner route is unavailable"}}

// ModelAPIFormat returns the protocol selected by the ordinary routing policy.
func (c *ChannelModelsCandidate) ModelAPIFormat(i int) string {
	if len(c.modelAPIFormats) == len(c.Models) {
		return c.modelAPIFormats[i]
	}
	return c.APIFormat
}

func onlyCacheOwner(candidates []*ChannelModelsCandidate, row *ent.GeminiCache) ([]*ChannelModelsCandidate, error) {
	for _, candidate := range candidates {
		if candidate.Channel.ID != row.ChannelID {
			continue
		}
		for i, entry := range candidate.Models {
			if entry.ActualModel != row.UpstreamModel || candidate.ModelAPIFormat(i) != string(llm.APIFormatGeminiContents) {
				continue
			}
			owner := *candidate
			owner.geminiCacheBound = true
			owner.Models = []biz.ChannelModelEntry{entry}
			owner.APIFormat = string(llm.APIFormatGeminiContents)
			owner.modelAPIFormats = []string{owner.APIFormat}
			return []*ChannelModelsCandidate{&owner}, nil
		}
	}
	return nil, errCacheRoute
}
func cacheCredential(ch *biz.Channel, row *ent.GeminiCache) (string, error) {
	if ch.ID == row.ChannelID {
		for _, key := range ch.GetEnabledAPIKeys() {
			if upstream.KeyRef(key) == row.KeyRef {
				return key, nil
			}
		}
	}
	return "", errCacheRoute
}

// Check the final address, model and credential after pass-through and channel overrides.
func applyGeminiCache(p *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("gemini-cache-owner", func(ctx context.Context, r *httpclient.Request) (*httpclient.Request, error) {
		row := boundGeminiCache(ctx)
		if row == nil {
			if r.APIFormat == string(llm.APIFormatGeminiContents) && (gjson.GetBytes(r.Body, "cachedContent").Exists() || gjson.GetBytes(r.Body, "cached_content").Exists()) {
				return nil, errors.New("Gemini cachedContent requires an owned cache")
			}
			return r, nil
		}
		u, err := url.Parse(r.URL)
		if err != nil || u.Query().Has("key") || r.APIFormat != string(llm.APIFormatGeminiContents) || p.GetCurrentChannel().ID != row.ChannelID || !row.ExpireTime.After(time.Now()) {
			return nil, errCacheRoute
		}
		u.RawQuery = ""
		prefix := row.BaseURL + "/models/" + row.UpstreamModel + ":"
		if !slices.Contains([]string{prefix + "generateContent", prefix + "streamGenerateContent"}, u.String()) {
			return nil, errCacheRoute
		}
		var credentials []string
		for name, values := range r.Headers {
			if strings.EqualFold(name, "x-goog-api-key") {
				credentials = append(credentials, values...)
			}
			if strings.EqualFold(name, "Authorization") {
				return nil, errCacheRoute
			}
		}
		if len(credentials) != 1 || upstream.KeyRef(credentials[0]) != row.KeyRef || r.Auth != nil || r.Query.Has("key") {
			return nil, errCacheRoute
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, err
		}
		delete(body, "cached_content")
		body["cachedContent"], _ = json.Marshal(row.UpstreamName)
		r.Body, err = json.Marshal(body)
		r.JSONBody = nil
		return r, err
	})
}
