package biz

import (
	"context"
	"fmt"
	"slices"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
)

// Only read display metadata for the already-authorized page. Payloads, channel
// identities, selection snapshots and credentials never enter the self-service API.
func (s *SelfUsageService) requestDisplayFields(ctx context.Context, requests []*ent.Request) (map[int]*SelfUsageRequest, error) {
	ids := make([]int, 0, len(requests))
	result := make(map[int]*SelfUsageRequest, len(requests))
	for _, req := range requests {
		ids = append(ids, req.ID)
		result[req.ID] = &SelfUsageRequest{
			ExecutedModels: []string{},
			Format:         req.Format, ClientStatusCode: req.ClientStatusCode,
		}
		if req.ReasoningEffort != "" {
			result[req.ID].ReasoningEffort = &req.ReasoningEffort
		}
	}
	executions, err := s.client.RequestExecution.Query().Where(requestexecution.RequestIDIn(ids...)).
		Select(requestexecution.FieldID, requestexecution.FieldRequestID, requestexecution.FieldModelID,
			requestexecution.FieldReasoningEffort, requestexecution.FieldFormat,
			requestexecution.FieldResponseStatusCode, requestexecution.FieldPassThroughApplied).
		Order(requestexecution.ByCreatedAt(sql.OrderDesc()), requestexecution.ByID(sql.OrderDesc())).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query self usage execution display: %w", err)
	}
	counts := make(map[int]int, len(requests))
	for _, execution := range executions {
		row := result[execution.RequestID]
		if counts[execution.RequestID] == 0 {
			row.OutboundFormat = &execution.Format
			if execution.ReasoningEffort != nil {
				row.ReasoningEffort = execution.ReasoningEffort
			}
			if row.ClientStatusCode == nil {
				row.ClientStatusCode = execution.ResponseStatusCode
			}
		}
		counts[execution.RequestID]++
		row.RetryCount = counts[execution.RequestID] - 1
		if execution.ModelID != "" && !slices.Contains(row.ExecutedModels, execution.ModelID) {
			row.ExecutedModels = append(row.ExecutedModels, execution.ModelID)
		}
		row.PassThroughApplied = row.PassThroughApplied || execution.PassThroughApplied
	}
	speeds, err := RequestOutputSpeeds(ctx, s.client, ids)
	if err != nil {
		return nil, fmt.Errorf("query self usage output speeds: %w", err)
	}
	for id, speed := range speeds {
		result[id].OutputTokensPerSecond = speed
	}
	return result, nil
}
