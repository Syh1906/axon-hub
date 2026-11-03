package biz

import (
	"context"
	"fmt"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/trace"
	"github.com/looplj/axonhub/llm"
)

// ResponsesContinuationRoute is recovered from successful durable metadata,
// independently of prompt-cache leases and expiring debug payloads.
type ResponsesContinuationRoute struct {
	ChannelID int
	KeyRef    string
	KeyHint   string
}

func (s *RequestService) GetResponsesContinuationRoute(ctx context.Context, previousResponseID string) (ResponsesContinuationRoute, bool, error) {
	projectID, ok := contexts.GetProjectID(ctx)
	if !ok {
		return ResponsesContinuationRoute{}, false, nil
	}
	apiKeyID := 0
	apiKey, hasAPIKey := contexts.GetAPIKey(ctx)
	if hasAPIKey && apiKey != nil {
		apiKeyID = apiKey.ID
	}
	client := s.entFromContext(ctx)
	query := client.Request.Query().Where(
		request.ProjectIDEQ(projectID),
		request.StatusEQ(request.StatusCompleted), request.ChannelIDNEQ(0),
		request.FormatIn(string(llm.APIFormatOpenAIResponse), string(llm.APIFormatOpenAIResponseWebSocket), string(llm.APIFormatOpenAIResponseCompact)),
	).Select(request.FieldID, request.FieldChannelID).Order(ent.Desc(request.FieldCreatedAt), ent.Desc(request.FieldID))
	if hasAPIKey && apiKey != nil {
		query.Where(request.APIKeyIDEQ(apiKeyID))
	} else {
		query.Where(request.APIKeyIDIsNil())
	}
	scoped := query.Clone()
	threadID := 0
	if previousResponseID != "" {
		scoped.Where(request.ExternalIDEQ(previousResponseID))
	} else if current, ok := contexts.GetTrace(ctx); ok && current != nil {
		threadID = current.ThreadID
		scoped.Where(request.TraceIDEQ(current.ID))
	} else if current, ok := contexts.GetThread(ctx); ok && current != nil {
		scoped.Where(request.HasTraceWith(trace.ThreadIDEQ(current.ID)))
	} else {
		return ResponsesContinuationRoute{}, false, nil
	}
	req, err := scoped.First(ctx)
	if ent.IsNotFound(err) && previousResponseID == "" && threadID != 0 {
		req, err = query.Where(request.HasTraceWith(trace.ThreadIDEQ(threadID))).First(ctx)
	}
	if ent.IsNotFound(err) {
		return ResponsesContinuationRoute{}, false, nil
	}
	if err != nil {
		return ResponsesContinuationRoute{}, false, fmt.Errorf("load Responses continuation route: %w", err)
	}
	route := ResponsesContinuationRoute{ChannelID: req.ChannelID}
	execution, err := s.entFromContext(ctx).RequestExecution.Query().Where(
		requestexecution.RequestIDEQ(req.ID), requestexecution.ChannelIDEQ(req.ChannelID),
		requestexecution.StatusEQ(requestexecution.StatusCompleted),
	).Select(requestexecution.FieldSelection).Order(ent.Desc(requestexecution.FieldID)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return ResponsesContinuationRoute{}, false, fmt.Errorf("load Responses continuation credential: %w", err)
	}
	if execution != nil && execution.Selection != nil {
		route.KeyRef = execution.Selection.KeyRef
		route.KeyHint = execution.Selection.KeyHint
	}
	return route, true, nil
}
