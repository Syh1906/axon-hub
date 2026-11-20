package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	gc "github.com/looplj/axonhub/internal/ent/geminicache"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm/transformer/gemini"
)

type GeminiCacheConfig struct {
	APIKeyIDs  []int `conf:"api_key_ids" yaml:"api_key_ids" json:"api_key_ids"`
	ChannelIDs []int `conf:"channel_ids" yaml:"channel_ids" json:"channel_ids"`
}

func (h *GeminiHandlers) WithCacheConfig(config GeminiCacheConfig) *GeminiHandlers {
	if h == nil {
		return nil
	}
	copy := *h
	copy.cacheConfig = config
	return &copy
}

type cacheFailure struct {
	status  int
	message string
}

func (e *cacheFailure) Error() string { return e.message }
func cacheError(c *gin.Context, status int, msg string) {
	code := map[int]string{400: "INVALID_ARGUMENT", 403: "PERMISSION_DENIED", 404: "NOT_FOUND", 409: "ABORTED", 429: "RESOURCE_EXHAUSTED", 501: "UNIMPLEMENTED", 503: "UNAVAILABLE"}[status]
	if code == "" {
		code = "INTERNAL"
	}
	c.JSON(status, gemini.GeminiError{Error: gemini.ErrorDetail{Code: status, Status: code, Message: msg}})
}
func reportCacheError(c *gin.Context, err error) {
	var e *cacheFailure
	switch {
	case errors.As(err, &e):
		cacheError(c, e.status, e.message)
	case ent.IsNotFound(err):
		cacheError(c, 404, "Cache not found")
	default:
		cacheError(c, 500, "Could not access cache metadata")
	}
}
func (h *GeminiHandlers) cacheCaller(c *gin.Context) *ent.APIKey {
	ctx := c.Request.Context()
	key, ok := contexts.GetAPIKey(ctx)
	if !ok || !slices.Contains(h.cacheConfig.APIKeyIDs, key.ID) || key.Type == apikey.TypeNoauth || !authz.HasScope(ctx, scopes.ScopeWriteRequests) {
		cacheError(c, 403, "Explicit caching requires an authorized integration key")
		return nil
	}
	return key
}
func ownedCache(db *ent.Client, key *ent.APIKey) *ent.GeminiCacheQuery {
	return db.GeminiCache.Query().Where(gc.ProjectIDEQ(key.ProjectID), gc.APIKeyIDEQ(key.ID))
}
func loadCache(ctx context.Context, key *ent.APIKey, id string) (*ent.GeminiCache, error) {
	db := ent.FromContext(ctx)
	return authz.RunAsHost(ctx, db, "gemini-cache-owner", func(host context.Context) (*ent.GeminiCache, error) {
		return ownedCache(db, key).Where(gc.PublicIDEQ(id), gc.ExpireTimeGT(time.Now().UTC())).Only(contexts.WithPrimaryRead(host))
	})
}
func (h *GeminiHandlers) bindCacheReference(c *gin.Context, body []byte) bool {
	ref := gjson.GetBytes(body, "cachedContent")
	if gjson.GetBytes(body, "cached_content").Exists() {
		cacheError(c, 400, "Use cachedContent for the cache reference")
		return false
	}
	if !ref.Exists() {
		return true
	}
	key := h.cacheCaller(c)
	if key == nil {
		return false
	}
	id, ok := strings.CutPrefix(ref.String(), "cachedContents/")
	if !ok {
		cacheError(c, 400, "Invalid cache name")
		return false
	}
	row, err := loadCache(c.Request.Context(), key, id)
	if err != nil {
		reportCacheError(c, err)
		return false
	}
	action := strings.TrimPrefix(c.Param("action"), "/")
	model, method, _ := strings.Cut(action, ":")
	if model != row.Model || (method != "generateContent" && method != "streamGenerateContent") {
		cacheError(c, 400, "Cache requires the model used at creation")
		return false
	}
	if !slices.Contains(h.cacheConfig.ChannelIDs, row.ChannelID) {
		cacheError(c, 503, "Cache owner channel unavailable")
		return false
	}
	c.Request = c.Request.WithContext(orchestrator.WithGeminiCache(c.Request.Context(), row))
	return true
}

func (h *GeminiHandlers) CacheResource(c *gin.Context) {
	key := h.cacheCaller(c)
	if key == nil {
		return
	}
	ctx := c.Request.Context()
	db := ent.FromContext(ctx)
	id := c.Param("id")
	method := c.Request.Method
	if method == "GET" && id == "" {
		h.listCaches(c, key)
		return
	}
	var payload map[string]json.RawMessage
	if method == "POST" || method == "PATCH" {
		data, err := io.ReadAll(c.Request.Body)
		if err != nil || json.Unmarshal(data, &payload) != nil || payload == nil {
			cacheError(c, 400, "Expected a JSON object")
			return
		}
		if !validCacheExpiration(payload) {
			cacheError(c, 400, "Supply one positive ttl in seconds or one future expireTime")
			return
		}
	}
	if method == "POST" {
		h.createCache(c, key, payload)
		return
	}
	row, err := loadCache(ctx, key, id)
	if err != nil {
		reportCacheError(c, err)
		return
	}
	target, err := h.cacheTarget(ctx, key, row.Model, row)
	if err != nil {
		reportCacheError(c, err)
		return
	}
	path := row.UpstreamName
	if method == "PATCH" {
		mask := c.Query("updateMask")
		if len(payload) != 1 || (payload["ttl"] == nil && payload["expireTime"] == nil) || (mask != "" && payload[mask] == nil) {
			cacheError(c, 400, "Only ttl or expireTime can be updated")
			return
		}
		if mask != "" {
			path += "?updateMask=" + url.QueryEscape(mask)
		}
	}
	body, _ := json.Marshal(payload)
	if method == "GET" {
		m, err := target.send(ctx, method, path, nil)
		if err != nil {
			reportCacheError(c, err)
			return
		}
		m.Name = "cachedContents/" + id
		m.Model = "models/" + row.Model
		c.JSON(200, m)
		return
	}
	m, err := target.send(ctx, method, path, body)
	var failure *cacheFailure
	missing := errors.As(err, &failure) && failure.status == http.StatusNotFound
	if err != nil && !(method == "DELETE" && missing) {
		reportCacheError(c, err)
		return
	}
	// Once the provider commits a change, finish the local write even if the client disconnects.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	result, err := authz.RunAsHost(persist, db, "gemini-cache-mutation", func(host context.Context) (cacheMetadata, error) {
		if method == "DELETE" {
			_, err := db.GeminiCache.Delete().Where(gc.IDEQ(row.ID), gc.ProjectIDEQ(key.ProjectID), gc.APIKeyIDEQ(key.ID)).Exec(host)
			return m, err
		}
		// Concurrent renewals may return out of order. On a revision conflict read
		// the provider's current expiration instead of overwriting a later response.
		for {
			m.Name = "cachedContents/" + id
			m.Model = "models/" + row.Model
			data, err := json.Marshal(m)
			if err != nil {
				return m, err
			}
			n, err := db.GeminiCache.Update().Where(gc.IDEQ(row.ID), gc.RevisionEQ(row.Revision)).
				SetMetadata(data).SetExpireTime(m.ExpireTime.UTC()).AddRevision(1).Save(host)
			if err != nil || n == 1 {
				return m, err
			}
			row, err = ownedCache(db, key).Where(gc.PublicIDEQ(id)).Only(contexts.WithPrimaryRead(host))
			if err != nil {
				return m, err
			}
			m, err = target.send(persist, "GET", row.UpstreamName, nil)
			if err != nil {
				return m, err
			}
		}
	})
	if err != nil {
		reportCacheError(c, err)
		return
	}
	if method == "DELETE" {
		c.JSON(200, gin.H{})
		return
	}
	c.JSON(200, result)
}

var cacheTTLPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,9})?s$`)

func validCacheExpiration(payload map[string]json.RawMessage) bool {
	ttl, hasTTL := payload["ttl"]
	expire, hasExpire := payload["expireTime"]
	if hasTTL {
		var value string
		if hasExpire || json.Unmarshal(ttl, &value) != nil || !cacheTTLPattern.MatchString(value) {
			return false
		}
		d, err := time.ParseDuration(value)
		return err == nil && d > 0
	}
	if hasExpire {
		var t time.Time
		return json.Unmarshal(expire, &t) == nil && t.After(time.Now())
	}
	return true
}

func (h *GeminiHandlers) createCache(c *gin.Context, key *ent.APIKey, payload map[string]json.RawMessage) {
	ctx := c.Request.Context()
	var resource string
	if json.Unmarshal(payload["model"], &resource) != nil || !strings.HasPrefix(resource, "models/") || len(resource) <= 7 {
		cacheError(c, 400, "Expected models/{model}")
		return
	}
	model := strings.TrimPrefix(resource, "models/")
	target, err := h.cacheTarget(ctx, key, model, nil)
	if err != nil {
		reportCacheError(c, err)
		return
	}
	payload["model"], _ = json.Marshal("models/" + target.model)
	body, _ := json.Marshal(payload)
	m, err := target.send(ctx, "POST", "cachedContents", body)
	if err != nil {
		if cacheNamePattern.MatchString(m.Name) {
			target.discardCreated(ctx, m.Name)
		}
		reportCacheError(c, err)
		return
	}
	upstreamName := m.Name
	id := uuid.NewString()
	m.Name = "cachedContents/" + id
	m.Model = resource
	data, _ := json.Marshal(m)
	db := ent.FromContext(ctx)
	_, err = authz.RunAsHost(ctx, db, "gemini-cache-create", func(host context.Context) (*ent.GeminiCache, error) {
		// Reclaim this owner's expired bindings after any in-flight renewal and reconciliation can finish.
		_, purgeErr := db.GeminiCache.Delete().Where(gc.ProjectIDEQ(key.ProjectID), gc.APIKeyIDEQ(key.ID),
			gc.ExpireTimeLT(time.Now().UTC().Add(-2*cacheOperationTimeout))).Exec(host)
		if purgeErr != nil {
			log.Warn(host, "Gemini cache metadata cleanup failed", log.Cause(purgeErr))
		}
		return db.GeminiCache.Create().SetPublicID(id).
			SetProjectID(key.ProjectID).SetAPIKeyID(key.ID).
			SetChannelID(target.channel.ID).SetBaseURL(target.base).SetKeyRef(upstream.KeyRef(target.key)).
			SetModel(model).SetUpstreamModel(target.model).SetUpstreamName(upstreamName).
			SetExpireTime(m.ExpireTime.UTC()).SetMetadata(data).Save(host)
	})
	if err != nil {
		target.discardCreated(ctx, upstreamName)
		reportCacheError(c, err)
		return
	}
	c.JSON(200, m)
}

func (h *GeminiHandlers) listCaches(c *gin.Context, key *ent.APIKey) {
	size, err := strconv.Atoi(c.DefaultQuery("pageSize", "100"))
	if err != nil || size < 1 || size > 1000 {
		cacheError(c, 400, "Invalid pageSize")
		return
	}
	after, err := strconv.Atoi(c.DefaultQuery("pageToken", "0"))
	if err != nil || after < 0 {
		cacheError(c, 400, "Invalid pageToken")
		return
	}
	ctx := c.Request.Context()
	db := ent.FromContext(ctx)
	rows, err := authz.RunAsHost(ctx, db, "gemini-cache-list", func(host context.Context) ([]*ent.GeminiCache, error) {
		return ownedCache(db, key).Where(gc.ExpireTimeGT(time.Now().UTC()), gc.IDGT(after)).Order(gc.ByID()).Limit(size + 1).All(contexts.WithPrimaryRead(host))
	})
	if err != nil {
		reportCacheError(c, err)
		return
	}
	items := make([]json.RawMessage, 0, min(size, len(rows)))
	resp := gin.H{}
	for i, row := range rows {
		if i == size {
			resp["nextPageToken"] = fmt.Sprint(rows[i-1].ID)
			break
		}
		items = append(items, row.Metadata)
	}
	resp["cachedContents"] = items
	c.JSON(200, resp)
}
