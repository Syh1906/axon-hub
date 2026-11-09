package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/pkg/xredis"
)

const responsesAffinityTTL = time.Hour

// Origin records prove where history came from. This separate, expiring store
// only nominates a channel for complete history; it never reloads old origins.
// Use one authoritative tier so asynchronous cache promotion cannot revive a
// replaced binding. Redis GETEX renews without writing a stale value back.
type responsesAffinityStore struct {
	memory *cache.Cache
	redis  redis.UniversalClient
	mu     sync.Mutex
}

func newResponsesAffinityStore(cfg xcache.Config) *responsesAffinityStore {
	s := &responsesAffinityStore{}
	switch cfg.Mode {
	case xcache.ModeRedis, xcache.ModeTwoLevel:
		if cfg.Redis.Addr != "" || len(cfg.Redis.Addrs) != 0 || cfg.Redis.URL != "" {
			client, err := xredis.NewClient(cfg.Redis)
			if err != nil {
				panic(fmt.Errorf("invalid Responses affinity redis config: %w", err))
			}
			s.redis = client
			return s
		}
		fallthrough
	case xcache.ModeMemory:
		s.memory = cache.New(responsesAffinityTTL, 10*time.Minute)
	}
	return s
}

func (s *responsesAffinityStore) get(ctx context.Context, key string) (ResponsesContinuationRoute, bool, error) {
	var route ResponsesContinuationRoute
	if s.redis != nil {
		data, err := s.redis.GetEx(ctx, key, responsesAffinityTTL).Bytes()
		if errors.Is(err, redis.Nil) {
			return route, false, nil
		}
		if err != nil {
			return route, false, err
		}
		err = json.Unmarshal(data, &route)
		return route, err == nil && route.ChannelID > 0, err
	}
	if s.memory == nil {
		return route, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, found := s.memory.Get(key)
	if !found {
		return route, false, nil
	}
	route = value.(ResponsesContinuationRoute)
	s.memory.Set(key, route, responsesAffinityTTL)
	return route, true, nil
}

func (s *responsesAffinityStore) put(ctx context.Context, key string, route ResponsesContinuationRoute) error {
	if s.redis != nil {
		data, err := json.Marshal(route)
		if err != nil {
			return err
		}
		return s.redis.Set(ctx, key, data, responsesAffinityTTL).Err()
	}
	if s.memory != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.memory.Set(key, route, responsesAffinityTTL)
	}
	return nil
}

func responsesAffinityKey(projectID, apiKeyID int, model, fingerprint string) string {
	return fmt.Sprintf("axonhub:routing:responses-affinity:v1:%d:%d:%s:%s", projectID, apiKeyID, responsesDigest(model), fingerprint)
}

// GetResponsesAffinity uses exact history identity and the public model, never
// the trace's latest request. Missing/expired preferences stay missing.
func (s *RequestService) GetResponsesAffinity(ctx context.Context, model, previousID, encryptedContent string) (ResponsesContinuationRoute, bool, error) {
	projectID, apiKeyID, ok := responsesOriginScope(ctx)
	if !ok {
		return ResponsesContinuationRoute{}, false, nil
	}
	var fingerprints []string
	if previousID != "" {
		fingerprints = append(fingerprints, responsesDigest("response:"+previousID))
	}
	if encryptedContent != "" {
		fingerprints = append(fingerprints, responsesDigest("encrypted:"+encryptedContent))
	}
	for _, fingerprint := range fingerprints {
		route, found, err := s.responsesAffinity.get(ctx, responsesAffinityKey(projectID, apiKeyID, model, fingerprint))
		if found || err != nil {
			return route, found, err
		}
	}
	return ResponsesContinuationRoute{}, false, nil
}

// RecordResponsesAffinity is called only for a successful terminal response.
// Completed items from a canceled stream retain provenance without acquiring
// routing preference. Success also updates the input tip after a failover.
func (s *RequestService) RecordResponsesAffinity(ctx context.Context, execution *ent.RequestExecution, requestBody, responseBody []byte) error {
	if execution == nil || execution.Selection == nil {
		return nil
	}
	status := gjson.GetBytes(responseBody, "status").String()
	if status != "" && status != "completed" {
		return nil
	}
	projectID, apiKeyID, ok := responsesOriginScope(ctx)
	if !ok {
		return nil
	}
	model := gjson.GetBytes(requestBody, "model").String()
	fingerprints := map[string]bool{}
	for index, body := range [][]byte{requestBody, responseBody} {
		idField, itemsField := "previous_response_id", "input"
		if index == 1 {
			idField, itemsField = "id", "output"
		}
		if id := gjson.GetBytes(body, idField).String(); id != "" {
			fingerprints[responsesDigest("response:"+id)] = true
		}
		var latest string
		for _, item := range gjson.GetBytes(body, itemsField).Array() {
			if value := item.Get("encrypted_content").String(); value != "" {
				latest = value
			}
		}
		if latest != "" {
			fingerprints[responsesDigest("encrypted:"+latest)] = true
		}
	}
	selection := execution.Selection
	route := ResponsesContinuationRoute{ChannelID: execution.ChannelID, KeyRef: selection.KeyRef, KeyHint: selection.KeyHint, UpstreamRef: selection.UpstreamRef}
	if route.KeyRef == "" {
		route.KeyHint = ""
	}
	for fingerprint := range fingerprints {
		if err := s.responsesAffinity.put(ctx, responsesAffinityKey(projectID, apiKeyID, model, fingerprint), route); err != nil {
			return err
		}
	}
	return nil
}
