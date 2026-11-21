package biz

import (
	"bytes"
	"context"
	"sync"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/requestpayload"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

type requestImageSummary = requestpayload.ImageSummary

type (
	bodyInspectionKey struct{}
	bodyInspection    struct {
		mu       sync.Mutex
		body     []byte
		analysis *requestpayload.Analysis
		marker   objects.JSONRawMessage
	}
)

// WithRequestBodyInspection shares a single immutable body version across audit and admission.
func WithRequestBodyInspection(ctx context.Context) context.Context {
	return context.WithValue(ctx, bodyInspectionKey{}, new(bodyInspection))
}

func inspectRequestBody(ctx context.Context, body []byte) (*requestpayload.ImageSummary, *requestpayload.ContentSummary) {
	cache, _ := ctx.Value(bodyInspectionKey{}).(*bodyInspection)
	if cache == nil || len(body) == 0 {
		return requestpayload.Inspect(body)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.inspect(body)
	return cache.analysis.Images, cache.analysis.Content
}

func (cache *bodyInspection) inspect(body []byte) {
	if cache.analysis == nil || !bytes.Equal(cache.body, body) {
		cache.analysis = requestpayload.Analyze(body)
		cache.body, cache.marker = body, nil
	}
}

func (s *RequestPayloadService) saveRequestDebug(ctx context.Context, body []byte) objects.JSONRawMessage {
	cache, _ := ctx.Value(bodyInspectionKey{}).(*bodyInspection)
	if cache != nil && len(body) > 0 {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		cache.inspect(body)
		if cache.marker != nil {
			return cache.marker
		}
		ref, _ := parsePayloadMarker(s.saveDebug(ctx, body))
		ref.Images, ref.Content = cache.analysis.Images, cache.analysis.Content
		marker := payloadMarker(ref)
		if ref.State == "cached" {
			cache.marker = marker
		}
		return marker
	}
	ref, _ := parsePayloadMarker(s.saveDebug(ctx, body))
	if ref.State != "invalid" {
		ref.Images, ref.Content = requestpayload.Inspect(body)
	}
	return payloadMarker(ref)
}

func summarizeRequestImages(body []byte) *requestImageSummary {
	images, _ := requestpayload.Inspect(body)
	return images
}

// UpdateRecoveredExecutionBody replaces the delta with the actual outgoing
// history while honoring both the original and current retention decisions.
func (s *RequestService) UpdateRecoveredExecutionBody(ctx context.Context, execution *ent.RequestExecution, body []byte) error {
	ref, ok := parsePayloadMarker(execution.RequestBody)
	storeBody := !ok || ref.State != "disabled"
	effort := responses.EffectiveReasoningEffort(body)
	changed := (execution.ReasoningEvidence == nil && effort != "") ||
		(execution.ReasoningEvidence != nil && execution.ReasoningEvidence.Requested != effort)
	if !storeBody && !changed {
		return nil
	}
	update := s.entFromContext(ctx).RequestExecution.UpdateOneID(execution.ID).Where(requestexecution.StatusEQ(requestexecution.StatusProcessing))
	marker := execution.RequestBody
	if storeBody {
		marker = payloadMarker(requestPayloadReference{State: "disabled"})
		if policy, err := s.SystemService.StoragePolicy(ctx); err == nil && policy.StoreRequestBody {
			marker = s.PayloadService.saveRequestDebug(ctx, body)
		}
		update.Modify(func(u *sql.UpdateBuilder) { u.Set(requestexecution.FieldRequestBody, string(marker)) })
	}
	// Recovery runs before any upstream write. Replace the unsent delta's facts.
	var requested *string
	var evidence *objects.RequestReasoningEvidence
	if changed {
		if effort != "" {
			requested = &effort
			evidence = &objects.RequestReasoningEvidence{Requested: effort}
			update.SetReasoningEvidence(evidence)
		} else {
			update.ClearReasoningEvidence()
		}
		update.Modify(func(u *sql.UpdateBuilder) { u.Set(requestexecution.FieldReasoningEffort, requested) })
	}
	if err := update.Exec(ctx); err != nil {
		return err
	}
	if changed {
		execution.ReasoningEffort, execution.ReasoningEvidence = requested, evidence
	}
	execution.RequestBody = marker
	return nil
}
