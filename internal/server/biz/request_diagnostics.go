package biz

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

// FinalizeRequestMetadata updates the intake row's normalized model
// once mapping succeeds, preserving the existing request-log semantics.
func (s *RequestService) FinalizeRequestMetadata(ctx context.Context, record *ent.Request, parsed *llm.Request) error {
	if record.ModelID == parsed.Model && record.ReasoningEffort == parsed.ReasoningEffort {
		return nil
	}
	err := s.entFromContext(ctx).Request.UpdateOneID(record.ID).Modify(func(update *sql.UpdateBuilder) {
		update.Set(request.FieldModelID, parsed.Model)
		update.Set(request.FieldReasoningEffort, parsed.ReasoningEffort)
	}).Exec(ctx)
	if err == nil {
		record.ModelID, record.ReasoningEffort = parsed.Model, parsed.ReasoningEffort
	}
	return err
}

func (s *RequestService) SetExecutionSelection(ctx context.Context, id int, selection *objects.RequestExecutionSelection) error {
	encoded, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	// Selection identity stays immutable; dispatch and transport are filled in as the attempt runs.
	return s.entFromContext(ctx).RequestExecution.UpdateOneID(id).Modify(func(u *sql.UpdateBuilder) {
		u.Set(requestexecution.FieldSelection, string(encoded))
	}).Exec(ctx)
}

func (s *RequestService) SetExecutionRetryDecision(ctx context.Context, executionID int, decision string) error {
	return s.entFromContext(ctx).RequestExecution.UpdateOneID(executionID).SetRetryDecision(decision).Exec(ctx)
}

// AdoptRequestExecution publishes the winner and its channel together. The
// caller owns this request; losing lanes never call this method.
func (s *RequestService) AdoptRequestExecution(ctx context.Context, execution *ent.RequestExecution, sticky bool) error {
	record, err := s.entFromContext(ctx).Request.UpdateOneID(execution.RequestID).
		SetChannelID(execution.ChannelID).SetResultExecutionID(execution.ID).Save(ctx)
	if err != nil {
		return err
	}
	if sticky {
		s.cachePreviousChannelForRequest(ctx, record)
	}
	return nil
}

// MarkExecutionHedgeLost runs after the losing attempt's stream has finalized.
// Preserve genuine provider failures; a canceled speculative attempt is a
// gateway decision, not evidence that the user canceled the logical request.
func (s *RequestService) MarkExecutionHedgeLost(ctx context.Context, executionID int) error {
	client := s.entFromContext(ctx)
	execution, err := client.RequestExecution.Get(ctx, executionID)
	if err != nil {
		return err
	}
	update := client.RequestExecution.UpdateOneID(executionID).SetRetryDecision("hedge_lost")
	if execution.Status == requestexecution.StatusCanceled && (execution.Failure == nil || execution.Failure.Source == "client_canceled") {
		update.SetFailure(&objects.RequestExecutionFailure{Source: "gateway", Code: "hedge_lost"}).
			SetErrorMessage("Another title attempt produced output first; this attempt was canceled by the gateway.")
	}
	return update.Exec(ctx)
}

func (s *RequestService) SetRequestClientStatus(ctx context.Context, requestID, status int) error {
	return s.entFromContext(ctx).Request.UpdateOneID(requestID).SetClientStatusCode(status).Exec(ctx)
}

func (s *RequestService) SetExecutionUpstreamStatus(ctx context.Context, executionID, status int) error {
	return s.entFromContext(ctx).RequestExecution.UpdateOneID(executionID).SetResponseStatusCode(status).Exec(ctx)
}

func diagnosticRequestURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		switch strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key)) {
		case "key", "apikey", "token", "accesstoken", "refreshtoken", "authorization", "password", "secret":
			query.Set(key, "[REDACTED]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
