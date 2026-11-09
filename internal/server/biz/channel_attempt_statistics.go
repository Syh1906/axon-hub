package biz

import (
	"context"
	"strconv"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
)

func channelAttemptExpressions(q *sql.Selector) (outcome, first, speed string) {
	r := sql.Table(request.Table).As("attempt_request")
	u := sql.Table(usagelog.Table).As("attempt_usage")
	q.Join(r).OnP(sql.And(sql.ColumnsEQ(q.C("request_id"), r.C("id")), sql.ColumnsEQ(q.C("project_id"), r.C("project_id"))))
	q.LeftJoin(u).OnP(sql.And(sql.ColumnsEQ(q.C("request_id"), u.C("request_id")),
		sql.ColumnsEQ(q.C("channel_id"), u.C("channel_id")), sql.ColumnsEQ(q.C("project_id"), u.C("project_id")),
		sql.ExprP(usageanalytics.ExecutionUsageMatchSQL(q.C("id"), q.C("request_id"), q.C("channel_id"), q.C("project_id"), u.C("id"))),
	))
	outcome = usageanalytics.ChannelOutcomeSQL(q.C("status"), q.C("selection"), q.C("response_status_code"), q.C("error_message"), q.Dialect())
	first = usageanalytics.FirstTokenSQL(q.C("metrics_first_token_latency_ms"), q.C("metrics_latency_ms"), q.C("stream"), q.C("status"))
	speed = usageanalytics.OutputSpeedSQL(usageanalytics.TextTokensSQL(u.C("completion_tokens"), u.C("cost_snapshot"), u.C("completion_audio_tokens"), q.Dialect()),
		q.C("metrics_latency_ms"), q.C("metrics_first_byte_latency_ms"), q.C("stream"), q.C("format"))
	return
}

func excludeArchivedExecutions(q *sql.Selector) {
	a := sql.Table(usagearchive.Table).As("archived_execution")
	q.Where(sql.Not(sql.Exists(sql.Dialect(q.Dialect()).Select(a.C("id")).From(a).Where(sql.And(
		sql.EQ(a.C("kind"), string(usagearchive.KindExecution)), sql.ColumnsEQ(a.C("source_id"), q.C("id")),
		sql.ColumnsEQ(a.C("project_id"), q.C("project_id")),
	)))))
}

func queryChannelAttemptGroups(ctx context.Context, client *ent.Client, projectID int, input ConsumptionFilter, ids []int, start *time.Time, end time.Time) ([]consumptionGroup, error) {
	end = end.UTC()
	if start != nil {
		utc := start.UTC()
		start = &utc
	}
	query := client.RequestExecution.Query().Where(requestexecution.CreatedAtLT(end), requestexecution.StatusIn("completed", "failed", "canceled"))
	if start != nil {
		query.Where(requestexecution.CreatedAtGTE(*start))
	}
	if projectID > 0 {
		query.Where(requestexecution.ProjectIDEQ(projectID))
	}
	if len(ids) > 0 {
		query.Where(requestexecution.ChannelIDIn(ids...))
	}
	var rows []consumptionGroup
	err := query.Modify(func(q *sql.Selector) {
		excludeArchivedExecutions(q)
		outcome, first, speed := channelAttemptExpressions(q)
		model := "COALESCE(NULLIF(TRIM(" + q.C("model_id") + "), ''), " + sql.Table(request.Table).As("attempt_request").C("model_id") + ")"
		selectConsumptionEntity(q, ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Search: input.Search}, model)
		q.AppendSelect(sql.As("COALESCE(SUM("+outcome+"),0)", "success_count"), sql.As("COUNT("+outcome+")", "outcome_count"),
			sql.As("COALESCE(SUM("+first+"),0)", "first_token_sum"), sql.As("COUNT("+first+")", "first_token_count"),
			sql.As("COALESCE(SUM("+speed+"),0)", "speed_sum"), sql.As("COUNT("+speed+")", "speed_count"))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	archives := client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindExecution), usagearchive.OccurredAtLT(end))
	if start != nil {
		archives.Where(usagearchive.OccurredAtGTE(*start))
	}
	if projectID > 0 {
		archives.Where(usagearchive.ProjectIDEQ(projectID))
	}
	if len(ids) > 0 {
		archives.Where(usagearchive.ChannelIDIn(ids...))
	}
	var archived []consumptionGroup
	err = archives.Modify(func(q *sql.Selector) {
		selectConsumptionEntity(q, ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Search: input.Search}, q.C("model_id"))
		for _, field := range []string{"success_count", "outcome_count", "first_token_sum", "first_token_count"} {
			q.AppendSelect(sql.As("COALESCE(SUM("+q.C(field)+"),0)", field))
		}
		for _, field := range []string{"speed_sum", "speed_count"} {
			q.AppendSelect(sql.As("COALESCE(SUM(CASE WHEN "+q.C("speed_version")+" = "+strconv.Itoa(usageanalytics.OutputSpeedVersion)+" THEN "+q.C(field)+" ELSE 0 END),0)", field))
		}
	}).Scan(ctx, &archived)
	if err != nil {
		return nil, err
	}
	return mergeConsumptionGroups(rows, archived), nil
}

func replaceChannelPerformance(charges, attempts []consumptionGroup, includeAttemptOnly bool) []consumptionGroup {
	type key struct {
		id    int
		model string
	}
	byKey := make(map[key]int, len(charges))
	for i := range charges {
		byKey[key{charges[i].EntityID, charges[i].Model}] = i
		charges[i].SuccessCount, charges[i].OutcomeCount = 0, 0
		charges[i].FirstTokenSum, charges[i].FirstTokenCount = 0, 0
		charges[i].SpeedSum, charges[i].SpeedCount = 0, 0
	}
	for _, attempt := range attempts {
		i, found := byKey[key{attempt.EntityID, attempt.Model}]
		if !found {
			if includeAttemptOnly && attempt.OutcomeCount > 0 {
				charges = append(charges, attempt)
			}
			continue
		}
		charges[i].SuccessCount, charges[i].OutcomeCount = attempt.SuccessCount, attempt.OutcomeCount
		charges[i].FirstTokenSum, charges[i].FirstTokenCount = attempt.FirstTokenSum, attempt.FirstTokenCount
		charges[i].SpeedSum, charges[i].SpeedCount = attempt.SpeedSum, attempt.SpeedCount
	}
	return charges
}
