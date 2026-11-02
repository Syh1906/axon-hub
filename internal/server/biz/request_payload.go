package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/payloadcache"
	"github.com/looplj/axonhub/internal/pkg/xjson"
	"github.com/looplj/axonhub/internal/server/scheduler"
	"github.com/looplj/axonhub/llm/httpclient"
)

const (
	debugPayloadTTL      = 5 * time.Minute
	debugPayloadMaxBytes = 5 << 20
	contextPayloadTTL    = 2 * time.Hour
)

type RequestPayloadConfig struct {
	Directory string `conf:"directory" yaml:"directory" json:"directory"`
}

// RequestPayloadService separates short debug copies from continuation state.
// Neither store writes large JSON into request rows or into a database backup.
type RequestPayloadService struct {
	debug   *payloadcache.Cache
	session *payloadcache.Cache
}

func NewRequestPayloadService(config RequestPayloadConfig) *RequestPayloadService {
	return &RequestPayloadService{
		debug: payloadcache.New(payloadcache.Directory(config.Directory, "debug"), payloadcache.Limits{
			TTL: debugPayloadTTL, EntryBytes: debugPayloadMaxBytes, TotalBytes: 256 << 20, Entries: 4096,
		}),
		session: payloadcache.New(payloadcache.Directory(config.Directory, "sessions"), payloadcache.Limits{
			TTL: contextPayloadTTL, EntryBytes: 32 << 20, TotalBytes: 512 << 20, Entries: 2048,
		}),
	}
}

type requestPayloadReference struct {
	Version   int       `json:"version"`
	State     string    `json:"state"`
	Key       string    `json:"key,omitempty"`
	Size      int       `json:"size,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

type requestPayloadMarker struct {
	Payload requestPayloadReference `json:"_axon_payload"`
}

func payloadMarker(ref requestPayloadReference) objects.JSONRawMessage {
	ref.Version = 1
	data, _ := json.Marshal(requestPayloadMarker{Payload: ref})
	return data
}

func parsePayloadMarker(body []byte) (requestPayloadReference, bool) {
	if len(body) > 1024 {
		return requestPayloadReference{}, false
	}
	var marker requestPayloadMarker
	if json.Unmarshal(body, &marker) != nil || marker.Payload.Version != 1 {
		return requestPayloadReference{}, false
	}
	return marker.Payload, true
}

func (s *RequestPayloadService) saveDebug(ctx context.Context, body []byte) objects.JSONRawMessage {
	ref := requestPayloadReference{State: "cached", Size: len(body)}
	if len(body) > debugPayloadMaxBytes {
		ref.State = "too_large"
		return payloadMarker(ref)
	}
	if !json.Valid(body) {
		ref.State = "invalid"
		return payloadMarker(ref)
	}
	ref.Key = payloadcache.Digest(body)
	expiresAt, err := s.debug.Put(ref.Key, body)
	if err != nil {
		log.Warn(ctx, "Debug payload was not retained", log.Cause(err))
		ref.State, ref.Key = "unavailable", ""
	} else {
		ref.ExpiresAt = expiresAt
	}
	return payloadMarker(ref)
}

func (s *RequestPayloadService) loadDebug(ctx context.Context, body []byte) (objects.JSONRawMessage, bool) {
	ref, ok := parsePayloadMarker(body)
	if !ok {
		return nil, false
	}
	if ref.State == "cached" {
		if !time.Now().Before(ref.ExpiresAt) {
			ref.State = "expired"
		} else {
			data, err := s.debug.Get(ref.Key)
			if err == nil {
				return data, true
			}
			ref.State = "evicted"
			if !errors.Is(err, payloadcache.ErrMissing) {
				log.Warn(ctx, "Unable to read debug payload", log.Cause(err))
				ref.State = "unavailable"
			}
		}
	}
	// Internal file keys are never exposed to clients.
	ref.Key = ""
	return payloadMarker(ref), true
}

func (s *RequestService) debugResponsePayload(ctx context.Context, body any) objects.JSONRawMessage {
	policy, err := s.SystemService.StoragePolicy(ctx)
	if err != nil {
		log.Warn(ctx, "Unable to read response storage policy", log.Cause(err))
		return payloadMarker(requestPayloadReference{State: "unavailable"})
	}
	if !policy.StoreResponseBody {
		return payloadMarker(requestPayloadReference{State: "disabled"})
	}
	data, err := xjson.Marshal(body)
	if err != nil {
		log.Warn(ctx, "Unable to serialize response debug payload", log.Cause(err))
		return payloadMarker(requestPayloadReference{State: "invalid"})
	}
	return s.PayloadService.saveDebug(ctx, data)
}

func (s *RequestService) debugStreamPayload(ctx context.Context, chunks []*httpclient.StreamEvent) []objects.JSONRawMessage {
	enabled, err := s.SystemService.StoreChunks(ctx)
	if err != nil || !enabled || len(chunks) == 0 {
		return nil
	}
	var stored []objects.JSONRawMessage
	size := 2
	for _, chunk := range chunks {
		if shouldSkipStoredStreamChunk(chunk) {
			continue
		}
		data, err := marshalStreamEventForStorage(chunk)
		if err != nil {
			return []objects.JSONRawMessage{payloadMarker(requestPayloadReference{State: "invalid"})}
		}
		size += len(data) + 1
		if size > debugPayloadMaxBytes {
			return []objects.JSONRawMessage{payloadMarker(requestPayloadReference{State: "too_large", Size: size})}
		}
		stored = append(stored, data)
	}
	if len(stored) == 0 {
		return nil
	}
	data, _ := json.Marshal(stored)
	return []objects.JSONRawMessage{s.PayloadService.saveDebug(ctx, data)}
}

func (s *RequestService) loadDebugStreamPayload(ctx context.Context, chunks []objects.JSONRawMessage) ([]objects.JSONRawMessage, bool) {
	if len(chunks) != 1 {
		return nil, false
	}
	body, managed := s.PayloadService.loadDebug(ctx, chunks[0])
	if !managed {
		return nil, false
	}
	if _, missing := parsePayloadMarker(body); missing {
		return []objects.JSONRawMessage{body}, true
	}
	var result []objects.JSONRawMessage
	if json.Unmarshal(body, &result) != nil {
		return nil, true
	}
	return result, true
}

func (s *RequestPayloadService) RegisterScheduledTasks(ctx context.Context, sched *scheduler.Scheduler) error {
	return sched.Register(ctx, scheduler.TaskSpec{
		Name: "request-payload-expiry", Description: "清理过期的请求调试正文和续聊缓存", CronExpr: "*/1 * * * *", Timezone: "UTC",
	}, func(ctx context.Context) {
		if err := errors.Join(s.debug.Sweep(), s.session.Sweep()); err != nil {
			log.Warn(ctx, "Unable to expire request payloads", log.Cause(err))
		}
	})
}

func responsesContextKey(ctx context.Context, responseID string) (string, bool) {
	apiKey, ok := contexts.GetAPIKey(ctx)
	if !ok || apiKey == nil || responseID == "" {
		return "", false
	}
	projectID, _ := contexts.GetProjectID(ctx)
	return payloadcache.Digest([]byte(fmt.Sprintf("%d:%d:%s", projectID, apiKey.ID, responseID))), true
}

// SaveResponsesContext stores only the input/output needed for continuation.
// Turning either body switch off also disables disk continuation copies; the
// existing bounded in-memory session cache remains independent of debug data.
func (s *RequestService) SaveResponsesContext(ctx context.Context, requestBody, responseBody []byte) {
	policy, err := s.SystemService.StoragePolicy(ctx)
	if err != nil || !policy.StoreRequestBody || !policy.StoreResponseBody {
		return
	}
	var input struct {
		Input              json.RawMessage `json:"input"`
		PreviousResponseID string          `json:"previous_response_id,omitempty"`
	}
	var output struct {
		ID                string          `json:"id"`
		Status            string          `json:"status"`
		Output            json.RawMessage `json:"output"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details,omitempty"`
	}
	if json.Unmarshal(requestBody, &input) != nil || json.Unmarshal(responseBody, &output) != nil {
		return
	}
	// 网关中断保留已完成的输出项；普通失败和上游意外截断仍不能作为完整续聊。
	interrupted := output.Status == "incomplete" && output.IncompleteDetails != nil && output.IncompleteDetails.Reason == "interrupted"
	if output.Status != "completed" && !interrupted {
		return
	}
	key, ok := responsesContextKey(ctx, output.ID)
	if !ok {
		return
	}
	data, err := json.Marshal(struct {
		Request  any `json:"request"`
		Response any `json:"response"`
	}{input, output})
	if err != nil {
		return
	}
	if _, err := s.PayloadService.session.Put(key, data); err != nil {
		log.Warn(ctx, "Responses continuation was not retained on disk", log.Cause(err))
	}
}

func (s *RequestService) loadResponsesContext(ctx context.Context, responseID string) ([]byte, []byte, bool) {
	key, ok := responsesContextKey(ctx, responseID)
	if !ok {
		return nil, nil, false
	}
	data, err := s.PayloadService.session.Get(key)
	if err != nil {
		if !errors.Is(err, payloadcache.ErrMissing) {
			log.Warn(ctx, "Unable to load Responses continuation", log.Cause(err))
		}
		return nil, nil, false
	}
	var stored struct {
		Request  json.RawMessage `json:"request"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(data, &stored) != nil {
		return nil, nil, false
	}
	return stored.Request, stored.Response, true
}
