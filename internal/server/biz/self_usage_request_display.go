package biz

import (
	"context"
	"fmt"
	"slices"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
)

type SelfUsageModelExecution struct {
	ModelID            string
	UpstreamModelID    string
	Status             requestexecution.Status
	Format             string
	ReasoningEffort    *string
	PassThroughApplied bool
}

// Only read display metadata for the already-authorized page. Payloads, channel
// IDs, selection snapshots and credentials never enter the self-service API.
func (s *SelfUsageService) requestDisplayFields(ctx context.Context, requests []*ent.Request, channelVisible bool) (map[int]*SelfUsageRequest, error) {
	ids := make([]int, 0, len(requests))
	result := make(map[int]*SelfUsageRequest, len(requests))
	owners := make(map[int]*ent.Request, len(requests))
	resultIDs := make(map[int]int, len(requests))
	var legacy []int
	for _, req := range requests {
		ids = append(ids, req.ID)
		owners[req.ID] = req
		result[req.ID] = &SelfUsageRequest{
			ModelExecutions: []SelfUsageModelExecution{},
			ExecutedModels:  []string{},
			Format:          req.Format, ClientStatusCode: req.ClientStatusCode,
		}
		if req.ResultExecutionID != nil {
			resultIDs[req.ID] = *req.ResultExecutionID
		} else {
			legacy = append(legacy, req.ID)
		}
	}
	if len(legacy) > 0 {
		var matches []struct {
			ID          int
			ExecutionID *int `json:"execution_id"`
		}
		if err := s.client.Request.Query().Where(request.IDIn(legacy...)).Modify(func(q *sql.Selector) {
			q.Select(q.C(request.FieldID)).AppendSelectExprAs(requestLogResultID(q.Dialect(), q.Table()), "execution_id")
		}).Scan(ctx, &matches); err != nil {
			return nil, fmt.Errorf("query request log results: %w", err)
		}
		for _, m := range matches {
			if m.ExecutionID != nil {
				resultIDs[m.ID] = *m.ExecutionID
			}
		}
	}
	var executions []*RequestLogExecution
	err := s.client.RequestExecution.Query().Where(requestexecution.RequestIDIn(ids...)).
		Select(requestexecution.FieldID, requestexecution.FieldRequestID, requestexecution.FieldModelID,
			requestexecution.FieldUpstreamModelID, requestexecution.FieldStatus,
			requestexecution.FieldChannelID,
			requestexecution.FieldReasoningEffort, requestexecution.FieldReasoningEvidence, requestexecution.FieldFormat,
			requestexecution.FieldResponseStatusCode, requestexecution.FieldPassThroughApplied).
		Order(requestexecution.ByCreatedAt(sql.OrderDesc()), requestexecution.ByID(sql.OrderDesc())).
		Modify(selectExecutionTier).Scan(ctx, &executions)
	if err != nil {
		return nil, fmt.Errorf("query self usage execution display: %w", err)
	}
	counts := make(map[int]int, len(requests))
	for _, execution := range executions {
		row := result[execution.RequestID]
		row.ModelExecutions = append(row.ModelExecutions, SelfUsageModelExecution{
			ModelID: execution.ModelID, UpstreamModelID: execution.UpstreamModelID, Status: execution.Status,
			Format: execution.Format, ReasoningEffort: execution.ReasoningEffort, PassThroughApplied: execution.PassThroughApplied,
		})
		owner := owners[execution.RequestID]
		counts[execution.RequestID]++
		// Legacy HTTP status and protocol display already used the last attempt on
		// the adopted channel; changing effort selection must not erase them.
		display := owner.ResultExecutionID == nil || *owner.ResultExecutionID == execution.ID
		if owner.ResultExecutionID == nil && (owner.Status == request.StatusCompleted || owner.Failure != nil && owner.Failure.Code == "stream_delivery_canceled") && owner.ChannelID > 0 {
			display = owner.ChannelID == execution.ChannelID
		}
		if display && row.OutboundFormat == nil {
			row.OutboundFormat = &execution.Format
			if row.ClientStatusCode == nil {
				row.ClientStatusCode = execution.ResponseStatusCode
			}
		}
		if resultIDs[execution.RequestID] == execution.ID {
			row.OutboundServiceTier = execution.OutboundServiceTier()
			row.ReasoningEvidence = execution.ReasoningEvidence
			row.ReasoningEffort = execution.ReasoningEvidence.Effective(execution.ReasoningEffort)
		}
		row.RetryCount = counts[execution.RequestID] - 1
		row.ResultUnconfirmed = row.RetryCount > 0 && resultIDs[owner.ID] == 0 && owner.Status != request.StatusProcessing && owner.Status != request.StatusPending
		if execution.ModelID != "" && !slices.Contains(row.ExecutedModels, execution.ModelID) {
			row.ExecutedModels = append(row.ExecutedModels, execution.ModelID)
		}
		row.PassThroughApplied = row.PassThroughApplied || execution.PassThroughApplied
	}
	if channelVisible {
		channels, err := s.requestChannelDisplays(ctx, ids)
		if err != nil {
			return nil, err
		}
		for id, channel := range channels {
			result[id].Channel = channel
		}
	}
	details, err := RequestOutputDetailsByID(ctx, s.client, ids)
	if err != nil {
		return nil, fmt.Errorf("query self usage output speeds: %w", err)
	}
	for id, detail := range details {
		result[id].OutputRate = detail.Rate
		result[id].OutputBatches = detail.Batches
		if detail.Rate != nil {
			result[id].OutputTokensPerSecond = detail.Rate.TokensPerSecond
		}
	}
	return result, nil
}
