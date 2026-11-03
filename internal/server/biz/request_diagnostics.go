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

func (s *RequestService) SetExecutionTransport(ctx context.Context, id int, selection *objects.RequestExecutionSelection) error {
	encoded, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	// Selection identity stays immutable; only the current attempt's transport is completed.
	return s.entFromContext(ctx).RequestExecution.UpdateOneID(id).Modify(func(u *sql.UpdateBuilder) {
		u.Set(requestexecution.FieldSelection, string(encoded))
	}).Exec(ctx)
}

func (s *RequestService) SetExecutionRetryDecision(ctx context.Context, executionID int, decision string) error {
	return s.entFromContext(ctx).RequestExecution.UpdateOneID(executionID).SetRetryDecision(decision).Exec(ctx)
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
