package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

func archiveExecutionStatistics(ctx context.Context, client *ent.Client, requestIDs []int) error {
	var rows []struct {
		ID         int       `json:"id"`
		RequestID  int       `json:"request_id"`
		ProjectID  int       `json:"project_id"`
		ChannelID  int       `json:"channel_id"`
		CreatedAt  time.Time `json:"created_at"`
		Model      string    `json:"model"`
		Outcome    *int64    `json:"outcome"`
		FirstToken *float64  `json:"first_token"`
		Speed      *float64  `json:"speed"`
	}
	err := client.RequestExecution.Query().Where(requestexecution.RequestIDIn(requestIDs...), requestexecution.StatusIn("completed", "failed", "canceled")).Modify(func(q *sql.Selector) {
		excludeArchivedExecutions(q)
		outcome, first, speed := channelAttemptExpressions(q)
		q.Select(q.C("id"), q.C("request_id"), q.C("project_id"), q.C("channel_id"), q.C("created_at"),
			sql.As("COALESCE(NULLIF(TRIM("+q.C("model_id")+"), ''), "+sql.Table(request.Table).As("attempt_request").C("model_id")+")", "model"),
			sql.As(outcome, "outcome"), sql.As(first, "first_token"), sql.As(speed, "speed"))
	}).Scan(ctx, &rows)
	if err != nil {
		return err
	}
	for _, row := range rows {
		create := client.UsageArchive.Create().SetKind(usagearchive.KindExecution).SetSourceID(row.ID).SetRequestID(row.RequestID).
			SetProjectID(row.ProjectID).SetChannelID(row.ChannelID).SetModelID(row.Model).SetOccurredAt(row.CreatedAt).
			SetSpeedVersion(usageanalytics.OutputSpeedVersion)
		if row.Outcome != nil {
			create.SetOutcomeCount(1).SetSuccessCount(*row.Outcome)
		}
		if row.FirstToken != nil {
			create.SetFirstTokenCount(1).SetFirstTokenSum(*row.FirstToken)
		}
		if row.Speed != nil {
			create.SetSpeedCount(1).SetSpeedSum(*row.Speed)
		}
		if err := create.OnConflict(sql.ConflictColumns(usagearchive.FieldKind, usagearchive.FieldSourceID)).DoNothing().Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
