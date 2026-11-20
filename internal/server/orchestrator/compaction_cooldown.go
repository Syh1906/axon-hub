package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

const compactionDelay = 30 * time.Second
const maxCompactionFailures = 1024

type compactionCooldown struct {
	mu       sync.Mutex
	failures map[[32]byte]*compactionFailure
}

type compactionFailure struct {
	requestID int
	until     time.Time
	probing   bool
}

type compactionDispatch struct {
	cache      *compactionCooldown
	key        [32]byte
	prior      *compactionFailure
	requestID  int
	observeHit bool
	trace      *httpclient.TransportTrace
}

type compactionWait struct {
	requestID, seconds int
}

// Only the four identity fields take part in attribution. Other metadata may
// contain a workspace description and need not be decoded to compare tasks.
type compactionIdentity struct {
	Kind    string `json:"request_kind"`
	Session string `json:"session_id"`
	Thread  string `json:"thread_id"`
	Turn    string `json:"turn_id"`
}

func (e *compactionWait) Error() string {
	return fmt.Sprintf("上一次相同的历史压缩请求 #%d 等待首个事件超时。本次未发送，请在 %d 秒后重试。", e.requestID, e.seconds)
}

// CompactionRetryAfter returns the remaining cooldown in whole seconds.
func CompactionRetryAfter(err error) int {
	if wait, ok := errors.AsType[*compactionWait](err); ok {
		return wait.seconds
	}
	return 0
}

func (c *compactionCooldown) begin(key [32]byte, id int, enforce bool, now time.Time) (*compactionDispatch, *compactionWait) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prior := c.failures[key]
	if prior != nil {
		left := prior.until.Sub(now)
		if left > 0 || prior.probing {
			wait := &compactionWait{prior.requestID, max(1, int((left+time.Second-1)/time.Second))}
			if enforce {
				return nil, wait
			}
			return &compactionDispatch{cache: c, key: key, prior: prior, requestID: id, observeHit: true}, wait
		}
		prior.probing = true
	}
	return &compactionDispatch{cache: c, key: key, prior: prior, requestID: id}, nil
}

func (d *compactionDispatch) finish(dispatched, timedOut bool, now time.Time) {
	c := d.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	// Overlapping earlier attempts must not erase or extend a newer failure.
	if d.observeHit || c.failures[d.key] != d.prior {
		return
	}
	if !dispatched {
		if d.prior != nil {
			d.prior.probing = false
		}
		return
	}
	if !timedOut {
		delete(c.failures, d.key)
		return
	}
	if c.failures == nil {
		c.failures = make(map[[32]byte]*compactionFailure)
	}
	if d.prior == nil && len(c.failures) >= maxCompactionFailures {
		var oldestKey [32]byte
		var oldest *compactionFailure
		for key, f := range c.failures {
			if !f.probing && (oldest == nil || f.until.Before(oldest.until)) {
				oldestKey, oldest = key, f
			}
		}
		if oldest == nil {
			return
		}
		delete(c.failures, oldestKey)
	}
	c.failures[d.key] = &compactionFailure{requestID: d.requestID, until: now.Add(compactionDelay)}
}

func compactionRequestKey(request *ent.Request, ch *ent.Channel, task compactionIdentity, raw *httpclient.Request, stripPrevious bool) ([32]byte, error) {
	wire := *responses.PrepareHTTPDispatchRequest(raw, stripPrevious)
	wire.Headers = wire.Headers.Clone()
	out, err := httpclient.BuildStreamHTTPRequest(context.Background(), &wire)
	if err != nil {
		return [32]byte{}, err
	}
	headers := out.Header
	for _, k := range []string{"X-Request-Id", "X-Trace-Id", "X-Client-Request-Id", "Traceparent", "Tracestate"} {
		headers.Del(k)
	}
	// JSON preserves field boundaries and orders header/query maps deterministically.
	// Large payloads are hashed directly. Only the final digest is retained.
	scope, err := json.Marshal([]any{
		request.ProjectID, request.APIKeyID, request.UserID, request.ModelID, task,
		ch.ID, ch.UpdatedAt.UnixNano(), out.Method, out.URL.String(), headers, sha256.Sum256(wire.Body),
	})
	return sha256.Sum256(scope), err
}

func compactionTask(raw *httpclient.Request, websocket bool) (compactionIdentity, bool) {
	var task compactionIdentity
	header := raw.Headers.Get(codex.TurnMetadataHeader)
	if !strings.Contains(header, "compaction") || json.Unmarshal([]byte(header), &task) != nil {
		return task, false
	}
	if task.Kind != "compaction" || task.Session == "" || task.Thread == "" || task.Turn == "" {
		return task, false
	}
	for _, name := range []string{codex.SessionHeader, codex.SessionHeaderHyphen} {
		if id := raw.Headers.Get(name); id != "" && id != task.Session {
			return task, false
		}
	}
	if id := raw.Headers.Get(codex.ThreadIDHeader); id != "" && id != task.Thread {
		return task, false
	}
	if websocket {
		// The upgrade headers can outlive a turn. Require a matching declaration on this frame.
		var frame compactionIdentity
		meta := gjson.GetBytes(raw.JSONBody, "client_metadata.x-codex-turn-metadata")
		if meta.Type != gjson.String || json.Unmarshal([]byte(meta.String()), &frame) != nil || frame != task {
			return task, false
		}
	}
	return task, true
}

func checkCompactionCooldown(cache *compactionCooldown, out *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("compaction_cooldown", func(ctx context.Context, req *httpclient.Request) (*httpclient.Request, error) {
		s := out.state
		if cache == nil || s.compactionTask == nil || s.Request == nil || !s.Request.Stream || s.Request.APIKeyID == 0 {
			return req, nil
		}
		inbound := llm.APIFormat(s.Request.Format)
		if (inbound != llm.APIFormatOpenAIResponse && inbound != llm.APIFormatOpenAIResponseWebSocket) || req.APIFormat != string(llm.APIFormatOpenAIResponse) {
			return req, nil
		}
		mode := s.RequestService.PayloadService.CompactionCooldownMode()
		if mode == "off" {
			return req, nil
		}
		policy, hasPolicy := out.wrapped.(interface{ StripPreviousResponseIDForHTTP() bool })
		key, err := compactionRequestKey(s.Request, out.GetCurrentChannel().Channel, *s.compactionTask, req, hasPolicy && policy.StripPreviousResponseIDForHTTP())
		if err != nil {
			return req, nil
		}
		dispatch, wait := cache.begin(key, s.Request.ID, mode == "enforce", time.Now())
		s.compactionAttempt = dispatch
		if wait == nil {
			return req, nil
		}
		log.Info(ctx, "历史压缩命中首事件超时冷却", log.String("mode", mode), log.Int("axon_request_id", s.Request.ID), log.Int("previous_request_id", wait.requestID), log.Int("retry_after_seconds", wait.seconds))
		if mode != "enforce" {
			return req, nil
		}
		return nil, &pipeline.DispatchStoppedError{Cause: &llm.ResponseError{StatusCode: http.StatusServiceUnavailable, Detail: llm.ErrorDetail{Code: "compaction_cooldown", Type: "gateway_error", Message: wait.Error()}, Cause: wait}}
	})
}

// FinishDispatchAttempt releases a trial even if a later admission check rejected it.
// A returned stream has passed its first-event guard but can still fail during delivery.
func (p *PersistentOutboundTransformer) FinishDispatchAttempt(err error) {
	if d := p.state.compactionAttempt; d != nil {
		uploaded, headers := d.trace.ResponseTimes()
		d.finish(uploaded != nil || headers != nil, errors.Is(err, pipeline.ErrStreamFirstEventTimeout), time.Now())
		p.state.compactionAttempt = nil
	}
}
