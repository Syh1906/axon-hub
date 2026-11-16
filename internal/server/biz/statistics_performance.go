package biz

import (
	"context"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
)

type statisticsOwner struct {
	ProjectID int `json:"project_id"`
	RequestID int `json:"request_id"`
}

func statisticsExecutionOwners(ctx context.Context, client *ent.Client, kind string, ids []int) ([]statisticsOwner, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	t := sql.Table(consumptionSourceTables[kind])
	t.SetDialect(client.Driver().Dialect())
	q := sql.Dialect(client.Driver().Dialect()).Select(t.C("project_id"), t.C("request_id")).From(t).Where(sql.InInts(t.C("id"), ids...))
	statement, args := q.Query()
	var cursor sql.Rows
	if err := client.Driver().Query(ctx, statement, args, &cursor); err != nil {
		return nil, err
	}
	defer cursor.Close()
	var rows []statisticsOwner
	err := sql.ScanSlice(&cursor, &rows)
	return rows, err
}

func invalidateExecutionStatistics(ctx context.Context, client *ent.Client, owners []statisticsOwner) error {
	if len(owners) == 0 {
		return nil
	}
	seen := map[statisticsOwner]bool{}
	byProject := map[int][]int{}
	for _, owner := range owners {
		if seen[owner] {
			continue
		}
		seen[owner] = true
		byProject[owner.ProjectID] = append(byProject[owner.ProjectID], owner.RequestID)
	}
	for projectID, ids := range byProject {
		for offset := 0; offset < len(ids); offset += 512 {
			query, args := sql.Dialect(client.Driver().Dialect()).Update(requestexecution.Table).Set(requestexecution.FieldStatisticsReady, false).
				Add(requestexecution.FieldStatisticsVersion, 1).
				Where(sql.And(sql.EQ("project_id", projectID), sql.InInts("request_id", ids[offset:min(offset+512, len(ids))]...))).Query()
			if err := client.Driver().Exec(ctx, query, args, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// Final model statistics follow the request date; channel attempts follow the
// execution date. Only changed executions in the requested scope are projected.
func prepareStatisticsPerformance(ctx context.Context, client *ent.Client, projectID int, ids []int, start *time.Time, end time.Time, requestDates bool) error {
	for {
		var rows []struct {
			ID             int      `json:"id"`
			Version        int64    `json:"statistics_version"`
			Outcome        *float64 `json:"outcome"`
			ChannelOutcome *float64 `json:"channel_outcome"`
			First          *float64 `json:"first"`
			Speed          *float64 `json:"speed"`
		}
		q := client.RequestExecution.Query().Where(requestexecution.StatisticsReadyEQ(false), requestexecution.StatusIn("completed", "failed", "canceled")).Order(ent.Asc(requestexecution.FieldID)).Limit(128)
		if projectID > 0 {
			q.Where(requestexecution.ProjectIDEQ(projectID))
		}
		if len(ids) > 0 {
			q.Where(requestexecution.ChannelIDIn(ids...))
		}
		// Select a bounded set before evaluating legacy usage matching. Otherwise
		// ORDER BY/LIMIT may repeatedly evaluate every remaining row in the day.
		keys, err := q.Modify(func(q *sql.Selector) {
			at := q.C("created_at")
			owner := sql.Table("requests").As("attempt_request")
			owner.SetDialect(q.Dialect())
			q.Join(owner).OnP(sql.And(sql.ColumnsEQ(q.C("request_id"), owner.C("id")), sql.ColumnsEQ(q.C("project_id"), owner.C("project_id"))))
			if requestDates {
				at = owner.C("created_at")
			}
			q.Where(sql.LT(at, end.UTC()))
			if start != nil {
				q.Where(sql.GTE(at, start.UTC()))
			}
		}).IDs(ctx)
		if err != nil || len(keys) == 0 {
			return err
		}
		selected := client.RequestExecution.Query().Where(requestexecution.IDIn(keys...))
		if projectID > 0 {
			selected.Where(requestexecution.ProjectIDEQ(projectID))
		}
		err = selected.Modify(func(q *sql.Selector) {
			channelOutcome, first, speed := channelAttemptExpressions(q)
			e := sql.Table(requestexecution.Table)
			e.SetDialect(q.Dialect())
			q.Select(q.C("id"), q.C("statistics_version"), sql.As(consumptionOutcome(e), "outcome"), sql.As(channelOutcome, "channel_outcome"), sql.As(first, "first"), sql.As(speed, "speed"))
		}).Scan(ctx, &rows)
		if err != nil || len(rows) == 0 {
			return err
		}
		keys = make([]int, len(rows))
		versions := make([]*sql.Predicate, len(rows))
		for i, row := range rows {
			keys[i] = row.ID
			versions[i] = sql.And(sql.EQ("id", row.ID), sql.EQ(requestexecution.FieldStatisticsVersion, row.Version))
		}
		update := sql.Dialect(client.Driver().Dialect()).Update(requestexecution.Table).Set(requestexecution.FieldStatisticsReady, true).Where(sql.Or(versions...))
		for column, values := range map[string][]*float64{
			requestexecution.FieldStatisticsOutcome: nil, requestexecution.FieldStatisticsChannelOutcome: nil,
			requestexecution.FieldStatisticsFirstTokenMs: nil, requestexecution.FieldStatisticsOutputSpeed: nil,
		} {
			for _, row := range rows {
				switch column {
				case requestexecution.FieldStatisticsOutcome:
					values = append(values, row.Outcome)
				case requestexecution.FieldStatisticsChannelOutcome:
					values = append(values, row.ChannelOutcome)
				case requestexecution.FieldStatisticsFirstTokenMs:
					values = append(values, row.First)
				case requestexecution.FieldStatisticsOutputSpeed:
					values = append(values, row.Speed)
				}
			}
			update.Set(column, sql.ExprFunc(func(b *sql.Builder) {
				b.WriteString("CASE ").Ident("id")
				for i, id := range keys {
					b.WriteString(" WHEN ").Arg(id).WriteString(" THEN ")
					if values[i] == nil {
						b.WriteString("NULL")
					} else {
						b.Arg(*values[i])
					}
				}
				b.WriteString(" ELSE NULLIF(0.0, 0.0) END")
			}))
		}
		if err := saveStatisticsProjection(ctx, client, update, len(rows)); err != nil {
			return err
		}
	}
}
