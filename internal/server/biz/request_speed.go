package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
)

// RequestOutputSpeeds shares the leaderboard's final-attempt and usage matching.
// The caller's Ent authorization applies before any metrics are returned.
func RequestOutputSpeeds(ctx context.Context, client *ent.Client, ids []int) (map[int]*float64, error) {
	result := make(map[int]*float64, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var rows []struct {
		ID    int      `json:"id"`
		Speed *float64 `json:"speed"`
	}
	err := client.Request.Query().Where(request.IDIn(ids...), request.StatusIn(request.StatusCompleted, request.StatusFailed, request.StatusCanceled)).Modify(func(q *sql.Selector) {
		u, execution := joinConsumption(q, 0, nil, time.Now())
		u.Where(sql.InInts(sql.Table("usage_logs").As("ul").C("request_id"), ids...))
		_, speed := consumptionOutcomeAndSpeed(u, execution)
		q.Select(q.C("id"), sql.As(speed, "speed"))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		result[id] = nil
	}
	for _, row := range rows {
		result[row.ID] = row.Speed
	}
	return result, nil
}
