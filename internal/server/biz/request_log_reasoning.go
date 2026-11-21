package biz

import (
	"context"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

// For records without an explicit winner, attribute effort only when exactly
// one dispatched attempt matches the request's outcome and adopted channel.
func requestLogResultID(db string, req *sql.SelectTable) sql.Querier {
	attempt := sql.Table(requestexecution.Table).As("effort_attempt")
	req.SetDialect(db)
	attempt.SetDialect(db)
	dispatch := sqljson.Path("dispatch")
	// Client delivery can fail after the provider has completed successfully.
	// In that case the completed attempt on the final channel owns the response.
	delivery := sqljson.ValueEQ(req.C(request.FieldFailure), "delivery", sqljson.Path("stage"))
	outcome := sql.Or(
		sql.And(delivery, sql.EQ(attempt.C(requestexecution.FieldStatus), requestexecution.StatusCompleted)),
		sql.And(sql.Or(sql.Not(sqljson.HasKey(req.C(request.FieldFailure), sqljson.Path("stage"))),
			sqljson.ValueNEQ(req.C(request.FieldFailure), "delivery", sqljson.Path("stage"))),
			sql.ColumnsEQ(attempt.C(requestexecution.FieldStatus), req.C(request.FieldStatus))),
	)
	legacy := sql.Dialect(db).Select(sql.Min(attempt.C(requestexecution.FieldID))).From(attempt).
		Where(sql.And(
			sql.ColumnsEQ(attempt.C(requestexecution.FieldRequestID), req.C(request.FieldID)),
			outcome,
			// An abandoned attempt cannot own a later local rejection, even if both failed.
			sql.Or(sql.IsNull(attempt.C(requestexecution.FieldRetryDecision)), sql.NotIn(attempt.C(requestexecution.FieldRetryDecision),
				"same_channel", "next_address", "next_model", "next_channel", "hedge_lost")),
			sql.Or(sql.IsNull(req.C(request.FieldChannelID)),
				sql.EQ(req.C(request.FieldChannelID), 0), sql.ColumnsEQ(attempt.C(requestexecution.FieldChannelID), req.C(request.FieldChannelID))),
			sql.Or(sql.Not(sqljson.HasKey(attempt.C(requestexecution.FieldSelection), dispatch)),
				sqljson.ValueEQ(attempt.C(requestexecution.FieldSelection), objects.RequestExecutionDispatchStarted, dispatch)),
		)).Having(sql.EQ(sql.Count("*"), 1))
	return sql.ExprFunc(func(b *sql.Builder) {
		b.WriteString("COALESCE(").Ident(req.C(request.FieldResultExecutionID)).Comma().Wrap(func(b *sql.Builder) { b.Join(legacy) }).WriteByte(')')
	})
}

// RequestLogExecution contains only the execution fields used to display a request.
type RequestLogExecution struct {
	ID                 int
	RequestID          int    `sql:"request_id"`
	ChannelID          int    `sql:"channel_id"`
	ModelID            string `sql:"model_id"`
	UpstreamModelID    string `sql:"upstream_model_id"`
	Format             string
	Status             requestexecution.Status
	PassThroughApplied bool                              `sql:"pass_through_applied"`
	ResponseStatusCode *int                              `sql:"response_status_code"`
	ReasoningEffort    *string                           `sql:"reasoning_effort"`
	ReasoningEvidence  *objects.RequestReasoningEvidence `sql:"reasoning_evidence"`
	Dispatch           string
	OutboundTier       string `sql:"outbound_tier"`
	TierSent           *bool  `sql:"tier_sent"`
	RecordedProvider   string `sql:"recorded_provider"`
	WebSocketStage     string `sql:"ws_stage"`
}

func selectExecutionTier(q *sql.Selector) {
	selection := q.C(requestexecution.FieldSelection)
	q.AppendSelectExprAs(sqljson.ValuePath(selection, sqljson.Path("dispatch"), sqljson.Unquote(true)), "dispatch").
		AppendSelectExprAs(sqljson.ValuePath(selection, sqljson.Path("outbound_tier_sent"), sqljson.Unquote(true)), "tier_sent").
		AppendSelectExprAs(sqljson.ValuePath(selection, sqljson.Path("provider"), sqljson.Unquote(true)), "recorded_provider").
		AppendSelectExprAs(sqljson.ValuePath(selection, sqljson.Path("ws_evidence", "stage"), sqljson.Unquote(true)), "ws_stage").
		AppendSelectExprAs(sqljson.ValuePath(q.C(requestexecution.FieldCostSnapshot), sqljson.Path("serviceTier", "outbound"), sqljson.Unquote(true)), "outbound_tier")
}

func (e *RequestLogExecution) OutboundServiceTier() *string {
	if e.Dispatch != "" && e.Dispatch != objects.RequestExecutionDispatchStarted {
		return nil
	}
	if e.TierSent != nil {
		if !*e.TierSent {
			return nil
		}
	} else {
		// A legacy HTTP error status can come from headers alone (e.g. 417).
		// Require completion or an observed model event before using a historical tier.
		if e.Status != requestexecution.StatusCompleted && e.WebSocketStage != "model" {
			return nil
		}
		if e.WebSocketStage == "write" || e.RecordedProvider == string(channel.TypeOpenaiFake) || e.RecordedProvider == string(channel.TypeAnthropicFake) {
			return nil
		}
	}
	mode, _ := objects.NormalizeServiceTier(e.OutboundTier)
	if mode == "fast" || mode == "ultrafast" {
		return &mode
	}
	return nil
}

// Fetches only the public result metadata for the already-authorized page.
func RequestLogResultExecutions(ctx context.Context, client *ent.Client, ids []int) (map[int]*RequestLogExecution, error) {
	var rows []*RequestLogExecution
	err := client.RequestExecution.Query().Where(requestexecution.RequestIDIn(ids...), func(q *sql.Selector) {
		req := sql.Table(request.Table).As("effort_owner")
		q.Join(req).On(q.C(requestexecution.FieldRequestID), req.C(request.FieldID)).
			Where(sql.EQ(q.C(requestexecution.FieldID), requestLogResultID(q.Dialect(), req)))
	}).Select(requestexecution.FieldID, requestexecution.FieldRequestID, requestexecution.FieldReasoningEffort, requestexecution.FieldReasoningEvidence,
		requestexecution.FieldStatus, requestexecution.FieldResponseStatusCode).Modify(selectExecutionTier).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	results := make(map[int]*RequestLogExecution, len(ids))
	for _, id := range ids {
		results[id] = nil
	}
	for _, row := range rows {
		results[row.RequestID] = row
	}
	return results, nil
}
