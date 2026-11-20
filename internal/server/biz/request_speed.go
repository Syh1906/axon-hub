package biz

import (
	"context"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

// RequestOutputSpeeds shares the leaderboard's final-attempt and usage matching.
// The caller's Ent authorization applies before any metrics are returned.
func RequestOutputSpeeds(ctx context.Context, client *ent.Client, ids []int) (map[int]*float64, error) {
	rates, err := RequestOutputDetailsByID(ctx, client, ids)
	result := make(map[int]*float64, len(ids))
	for id, detail := range rates {
		if detail.Rate != nil {
			result[id] = detail.Rate.TokensPerSecond
		}
	}
	return result, err
}

// RequestOutputRate describes one matched usage row and its own execution.
type RequestOutputRate struct {
	OutputTokens    int64
	ReasoningTokens *int64
	DurationMs      int64
	TokensPerSecond *float64
}

type RequestOutputBatches struct {
	Count  int
	SpanMs *int64
}

type RequestOutputDetails struct {
	Batches *RequestOutputBatches
	Rate    *RequestOutputRate
}

func RequestOutputDetailsByID(ctx context.Context, client *ent.Client, ids []int) (map[int]RequestOutputDetails, error) {
	result := make(map[int]RequestOutputDetails, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var rows []struct {
		ContentBatches *int     `json:"content_batches"`
		Span           *int64   `json:"content_batch_span_ms"`
		ID             int      `json:"id"`
		Speed          *float64 `json:"speed"`
		Tokens         *int64   `json:"tokens"`
		Reasoning      *int64   `json:"reasoning"`
		Duration       *int64   `json:"duration"`
	}
	err := client.Request.Query().Where(request.IDIn(ids...), request.StatusEQ(request.StatusCompleted)).Modify(func(q *sql.Selector) {
		execution := joinLatestConsumptionExecution(q)
		u := joinExecutionUsage(q, execution)
		tokens := usageanalytics.TextTokensSQL(u.C("completion_tokens"), u.C("cost_snapshot"), u.C("completion_audio_tokens"), q.Dialect())
		duration := execution.C("metrics_latency_ms")
		format := "COALESCE(" + execution.C("format") + ", '')"
		firstToken := execution.C("metrics_first_token_latency_ms")
		window := "(" + duration + " - " + firstToken + ")"
		speed := "CASE WHEN " + execution.C("status") + " = 'completed' AND " + execution.C("stream") +
			" AND " + firstToken + " >= 0 AND " + window + " > 0 AND " + tokens + " > 1 AND " +
			format + " NOT LIKE '%image%' AND " + format + " NOT LIKE '%audio%' AND " + format + " NOT LIKE '%video%' THEN (" + tokens + " - 1) * 1000.0 / NULLIF(" + window + ", 0) END"
		q.Select(q.C("id"), sql.As(speed, "speed"), sql.As(u.C("completion_tokens"), "tokens"), sql.As(u.C("completion_reasoning_tokens"), "reasoning"), sql.As(duration, "duration"), execution.C("content_batches"), execution.C("content_batch_span_ms"))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		result[id] = RequestOutputDetails{}
	}
	for _, row := range rows {
		var detail RequestOutputDetails
		if row.ContentBatches != nil {
			detail.Batches = &RequestOutputBatches{Count: *row.ContentBatches, SpanMs: row.Span}
		}
		if row.Speed != nil && row.Tokens != nil && row.Duration != nil {
			detail.Rate = &RequestOutputRate{
				OutputTokens: *row.Tokens, ReasoningTokens: row.Reasoning,
				DurationMs: *row.Duration, TokensPerSecond: row.Speed,
			}
		}
		result[row.ID] = detail
	}
	return result, nil
}
