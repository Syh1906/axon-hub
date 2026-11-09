package biz

import (
	"context"
	"encoding/json"
	"fmt"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
)

type SelfUsageChannel struct {
	Name            string
	Provider        string
	ChannelType     string
	Tags            []string
	SessionState    string
	Multiplier      *float64
	MultiplierMixed bool
	hasCostSnapshot bool
}

// Only project public display fields for the authorized page. The full selection,
// cost snapshot and channel credentials never enter this path or its API objects.
func (s *SelfUsageService) requestChannelDisplays(ctx context.Context, ids []int) (map[int]*SelfUsageChannel, error) {
	var rows []struct {
		ID              int      `json:"id"`
		ExecutionID     *int     `json:"execution_id"`
		HasSession      bool     `json:"has_session"`
		SnapshotName    *string  `json:"snapshot_name"`
		Provider        *string  `json:"provider"`
		ExecutionName   *string  `json:"execution_name"`
		ExecutionType   *string  `json:"execution_type"`
		ExecutionTags   *string  `json:"execution_tags"`
		RequestName     *string  `json:"request_name"`
		RequestType     *string  `json:"request_type"`
		RequestTags     *string  `json:"request_tags"`
		HasCostSnapshot bool     `json:"has_cost_snapshot"`
		Multiplier      *float64 `json:"multiplier"`
	}
	err := s.client.Request.Query().Where(request.IDIn(ids...)).Modify(func(q *sql.Selector) {
		dialect := sql.Dialect(q.Dialect())
		req := sql.Table(request.Table)
		attempt := sql.Table(requestexecution.Table).As("display_attempt")
		latest := sql.Table(requestexecution.Table).As("latest_attempt")
		selected := FinalRequestChannelAttempt(q.Dialect(), req, requestexecution.FieldID).Where(RequestSessionFinished(req))
		last := dialect.Select(latest.C(requestexecution.FieldID)).From(latest).
			Where(sql.ColumnsEQ(latest.C(requestexecution.FieldRequestID), req.C(request.FieldID))).
			OrderBy(sql.Desc(latest.C(requestexecution.FieldCreatedAt)), sql.Desc(latest.C(requestexecution.FieldID))).Limit(1)
		q.LeftJoin(attempt).OnP(sql.P(func(b *sql.Builder) {
			b.Ident(attempt.C(requestexecution.FieldID)).WriteOp(sql.OpEQ).WriteString("COALESCE(").
				Wrap(func(b *sql.Builder) { b.Join(selected) }).Comma().
				Wrap(func(b *sql.Builder) { b.Join(last) }).WriteByte(')')
		}))
		executionChannel := sql.Table(channel.Table).As("execution_channel")
		requestChannel := sql.Table(channel.Table).As("request_channel")
		q.LeftJoin(executionChannel).On(attempt.C(requestexecution.FieldChannelID), executionChannel.C(channel.FieldID)).
			LeftJoin(requestChannel).On(req.C(request.FieldChannelID), requestChannel.C(channel.FieldID))
		jsonText := func(column, path string) string {
			if q.Dialect() == "postgres" {
				return "(CAST(" + column + " AS jsonb)#>>'{" + path + "}')"
			}
			expression := "JSON_EXTRACT(" + column + ", '$." + path + "')"
			if q.Dialect() == "mysql" {
				expression = "JSON_UNQUOTE(" + expression + ")"
			}
			return expression
		}
		selection := attempt.C(requestexecution.FieldSelection)
		snapshot := attempt.C(requestexecution.FieldCostSnapshot)
		textType := "TEXT"
		if q.Dialect() == "mysql" {
			textType = "CHAR"
		}
		q.Select(q.C(request.FieldID)).
			AppendSelectAs(attempt.C(requestexecution.FieldID), "execution_id").
			AppendSelectAs(jsonText(selection, "channel_name"), "snapshot_name").
			AppendSelectAs(jsonText(selection, "provider"), "provider").
			AppendSelectAs(executionChannel.C(channel.FieldName), "execution_name").
			AppendSelectAs(executionChannel.C(channel.FieldType), "execution_type").
			AppendSelectAs("CAST("+executionChannel.C(channel.FieldTags)+" AS "+textType+")", "execution_tags").
			AppendSelectAs(requestChannel.C(channel.FieldName), "request_name").
			AppendSelectAs(requestChannel.C(channel.FieldType), "request_type").
			AppendSelectAs("CAST("+requestChannel.C(channel.FieldTags)+" AS "+textType+")", "request_tags").
			AppendSelectAs("CASE WHEN "+snapshot+" IS NOT NULL THEN TRUE ELSE FALSE END", "has_cost_snapshot").
			AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
				b.WriteString("CASE WHEN ").Wrap(func(b *sql.Builder) { b.Join(selected) }).WriteString(" IS NOT NULL THEN TRUE ELSE FALSE END")
			}), "has_session")
		appliedPath := "applied.value"
		if q.Dialect() == "postgres" {
			appliedPath = "applied,value"
		}
		q.AppendSelectAs(jsonText(snapshot, appliedPath), "multiplier")
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("query self usage channel display: %w", err)
	}
	result := make(map[int]*SelfUsageChannel, len(rows))
	executionRequests := make(map[int]int, len(rows))
	executionIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		name, kind, tags := row.ExecutionName, row.ExecutionType, row.ExecutionTags
		// Match the admin fallback only when no finished session attempt exists.
		if !row.HasSession && name == nil {
			name, kind, tags = row.RequestName, row.RequestType, row.RequestTags
		}
		if row.SnapshotName != nil && *row.SnapshotName != "" {
			name = row.SnapshotName
		}
		if name == nil || *name == "" {
			continue
		}
		display := &SelfUsageChannel{Name: *name, Tags: []string{}, hasCostSnapshot: row.HasCostSnapshot, Multiplier: row.Multiplier}
		if row.Provider != nil {
			display.Provider = *row.Provider
		}
		if kind != nil {
			display.ChannelType = *kind
		}
		if tags != nil {
			if err := json.Unmarshal([]byte(*tags), &display.Tags); err != nil {
				return nil, fmt.Errorf("decode self usage channel tags: %w", err)
			}
		}
		result[row.ID] = display
		if row.ExecutionID != nil {
			executionIDs = append(executionIDs, *row.ExecutionID)
			executionRequests[*row.ExecutionID] = row.ID
		}
	}
	states := RequestChannelSessions(ctx, s.client, executionIDs)
	for id, state := range states {
		result[executionRequests[id]].SessionState = state
	}
	return result, nil
}
