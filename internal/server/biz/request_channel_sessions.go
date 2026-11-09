package biz

import (
	"context"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/log"
)

// Each finished request contributes its last dispatched attempt, even if it failed.
func RequestChannelSessions(ctx context.Context, client *ent.Client, ids []int) map[int]string {
	states := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return states
	}
	var rows []struct {
		ID              int  `json:"id"`
		Channel         int  `json:"channel"`
		PreviousChannel *int `json:"previous_channel"`
		UsedBefore      bool `json:"used_before"`
	}
	err := client.RequestExecution.Query().Where(requestexecution.IDIn(ids...)).Modify(func(q *sql.Selector) {
		requestSessionHistoryQuery(q, ids)
	}).Scan(ctx, &rows)
	for _, id := range ids {
		states[id] = ""
	}
	if err != nil {
		log.Warn(ctx, "Failed to read request channel session history", log.Cause(err))
		return states
	}
	for _, row := range rows {
		switch {
		case row.PreviousChannel != nil && *row.PreviousChannel == row.Channel:
			states[row.ID] = "reused"
		case row.UsedBefore:
			states[row.ID] = "returned"
		default:
			states[row.ID] = "new"
		}
	}
	return states
}
