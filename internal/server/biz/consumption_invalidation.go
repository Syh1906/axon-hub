package biz

import (
	"context"
	"fmt"
	"slices"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/consumptionsnapshot"
	"github.com/looplj/axonhub/internal/ent/predicate"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/log"
)

type consumptionMutation interface {
	ent.Mutation
	Client() *ent.Client
	Tx() (*ent.Tx, error)
	IDs(context.Context) ([]int, error)
}
type consumptionDate struct {
	ProjectID int       `json:"project_id"`
	At        time.Time `json:"occurred_at"`
}

var consumptionStatisticFields = map[string][]string{
	"Request":          {"status", "channel_id", "result_execution_id", "failure"},
	"RequestExecution": {"status", "metrics_latency_ms", "metrics_first_token_latency_ms", "metrics_first_byte_latency_ms", "metrics_first_byte_recorded", "response_status_code", "error_message", "selection"},
	"UsageLog":         nil, "UsageArchive": nil, "CacheEffectivenessWindow": nil,
	"BudgetCharge": {"usage_log_id"}, "APIKey": {"name", "group_ref", "deleted_at"}, "APIKeyGroup": {"name"},
	"User": {"first_name", "last_name", "email", "deleted_at"}, "Channel": {"name", "deleted_at", "tags", "remark"}, "Project": {"name"},
}
var consumptionSourceTables = map[string]string{"Request": "requests", "RequestExecution": "request_executions", "UsageLog": "usage_logs", "UsageArchive": "usage_archives", "BudgetCharge": "budget_charges", "CacheEffectivenessWindow": "cache_effectiveness_windows"}

func consumptionAffectsStatistics(m ent.Mutation) bool {
	fields, ok := consumptionStatisticFields[m.Type()]
	if !ok {
		return false
	}
	if m.Op().Is(ent.OpCreate|ent.OpDelete|ent.OpDeleteOne) || fields == nil {
		return true
	}
	for _, field := range append(m.Fields(), m.ClearedFields()...) {
		if slices.Contains(fields, field) {
			return true
		}
	}
	return false
}

func (s *ConsumptionService) snapshotInvalidation(next ent.Mutator) ent.Mutator {
	return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
		if !consumptionAffectsStatistics(mutation) {
			return next.Mutate(ctx, mutation)
		}
		m := mutation.(consumptionMutation)
		var ids []int
		var dates []consumptionDate
		var locateErr error
		metadata := consumptionSourceTables[m.Type()] == ""
		deleting := m.Op().Is(ent.OpDelete | ent.OpDeleteOne)
		_, locateErr = authz.RunAsHost(ctx, m.Client(), "consumption-source-dates", func(ctx context.Context) (bool, error) {
			ctx = contexts.WithPrimaryRead(schematype.SkipSoftDelete(ctx))
			if metadata || m.Op().Is(ent.OpCreate) {
				return true, nil
			}
			var err error
			ids, err = m.IDs(ctx)
			if err != nil {
				return false, err
			}
			if deleting {
				dates, err = consumptionChangedDates(ctx, m.Client(), m.Type(), ids)
			}
			return true, err
		})
		value, err := next.Mutate(ctx, mutation)
		if err != nil {
			return value, err
		}
		if m.Op().Is(ent.OpCreate) {
			if id, ok := m.(interface{ ID() (int, bool) }).ID(); ok {
				ids = []int{id}
			}
		}
		if !metadata && !deleting && locateErr == nil {
			dates, locateErr = authz.RunAsHost(ctx, m.Client(), "consumption-source-dates", func(ctx context.Context) ([]consumptionDate, error) {
				return consumptionChangedDates(contexts.WithPrimaryRead(ctx), m.Client(), m.Type(), ids)
			})
		}
		if locateErr != nil {
			log.Warn(ctx, "locate consumption source dates", log.Cause(locateErr))
			metadata = true
		}
		invalidate := func() {
			invalidCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, err := authz.RunAsHost(invalidCtx, s.client, "consumption-invalidate", func(ctx context.Context) (int, error) {
				if !metadata && len(dates) == 0 {
					return 0, nil
				}
				q := s.client.ConsumptionSnapshot.Update().SetSourceVersion(uuid.NewString()).SetPending(true)
				if metadata {
					q.SetCheckedAt(time.Unix(0, 0).UTC()).SetLeaseToken(uuid.NewString()).SetLeaseUntil(time.Unix(0, 0).UTC())
				} else {
					q.Where(consumptionDatePredicate(dates))
				}
				return q.Save(ctx)
			})
			if err != nil {
				log.Error(invalidCtx, "invalidate consumption snapshots", log.Cause(err))
			}
		}
		if tx, txErr := m.Tx(); txErr == nil {
			tx.OnCommit(func(next ent.Committer) ent.Committer {
				return ent.CommitFunc(func(ctx context.Context, tx *ent.Tx) error {
					if err := next.Commit(ctx, tx); err != nil {
						return err
					}
					invalidate()
					return nil
				})
			})
		} else {
			invalidate()
		}
		return value, nil
	})
}

// 只读取由来源主键匹配的项目/日期，再在 UPDATE 顶层使用索引约束。
func consumptionChangedDates(ctx context.Context, client *ent.Client, kind string, ids []int) ([]consumptionDate, error) {
	if len(ids) == 0 {
		return []consumptionDate{}, nil
	}
	db := client.Driver().Dialect()
	table, ok := consumptionSourceTables[kind]
	if !ok {
		return nil, fmt.Errorf("unknown consumption source %s", kind)
	}
	source := sql.Table(table).As("changed_source")
	source.SetDialect(db)
	d := sql.Dialect(db)
	base := func(at string) *sql.Selector {
		return d.Select(source.C("project_id"), sql.As(at, "occurred_at")).From(source).Where(sql.InInts(source.C("id"), ids...))
	}
	var query *sql.Selector
	if kind == "Request" {
		query = base(source.C("created_at"))
		execution := sql.Table("request_executions").As("changed_execution")
		execution.SetDialect(db)
		query.UnionAll(base(execution.C("created_at")).Join(execution).OnP(sql.And(sql.ColumnsEQ(execution.C("request_id"), source.C("id")), sql.ColumnsEQ(execution.C("project_id"), source.C("project_id")))))
	} else if kind == "CacheEffectivenessWindow" {
		query = base(source.C("window_end"))
	} else if kind == "UsageArchive" {
		query = base(source.C("occurred_at"))
		for _, item := range [][2]string{{"requests", "created_at"}, {"request_executions", "created_at"}, {"usage_archives", "occurred_at"}} {
			row := sql.Table(item[0]).As("archive_owner_" + item[0])
			row.SetDialect(db)
			field := "request_id"
			if item[0] == "requests" {
				field = "id"
			}
			match := sql.ColumnsEQ(row.C(field), source.C("request_id"))
			if item[0] == "usage_archives" {
				match = sql.And(sql.EQ(row.C("kind"), "request"), sql.ColumnsEQ(row.C("source_id"), source.C("request_id")))
			}
			query.UnionAll(base(row.C(item[1])).Join(row).OnP(sql.And(match, sql.ColumnsEQ(row.C("project_id"), source.C("project_id")))))
		}
	} else {
		request := sql.Table("requests").As("changed_request")
		request.SetDialect(db)
		query = base(request.C("created_at")).Join(request).OnP(sql.And(sql.ColumnsEQ(request.C("id"), source.C("request_id")), sql.ColumnsEQ(request.C("project_id"), source.C("project_id"))))
		execution := sql.Table("request_executions").As("changed_execution")
		execution.SetDialect(db)
		query.UnionAll(base(execution.C("created_at")).Join(execution).OnP(sql.And(sql.ColumnsEQ(execution.C("request_id"), source.C("request_id")), sql.ColumnsEQ(execution.C("project_id"), source.C("project_id")))))
		archive := sql.Table("usage_archives").As("changed_archive")
		archive.SetDialect(db)
		query.UnionAll(base(archive.C("occurred_at")).Join(archive).OnP(sql.And(sql.EQ(archive.C("kind"), "request"), sql.ColumnsEQ(archive.C("source_id"), source.C("request_id")), sql.ColumnsEQ(archive.C("project_id"), source.C("project_id")))))
	}
	stmt, args := query.Query()
	var rows sql.Rows
	if err := client.Driver().Query(ctx, stmt, args, &rows); err != nil {
		return nil, err
	}
	defer rows.Close()
	var dates []consumptionDate
	if err := sql.ScanSlice(&rows, &dates); err != nil {
		return nil, err
	}
	if kind == "CacheEffectivenessWindow" {
		for i := range dates {
			dates[i].At = dates[i].At.Add(-time.Nanosecond)
		}
	}
	// 缺失请求事实时只保留的执行/账目仍可能修正历史，按来源项目失效。
	if len(dates) == 0 {
		var projects []struct {
			ProjectID int `json:"project_id"`
		}
		stmt, args = d.Select(source.C("project_id")).From(source).Where(sql.InInts(source.C("id"), ids...)).Query()
		var raw sql.Rows
		if err := client.Driver().Query(ctx, stmt, args, &raw); err != nil {
			return nil, err
		}
		defer raw.Close()
		if err := sql.ScanSlice(&raw, &projects); err != nil {
			return nil, err
		}
		for _, p := range projects {
			dates = append(dates, consumptionDate{ProjectID: p.ProjectID})
		}
	}
	return dates, nil
}

func consumptionDatePredicate(dates []consumptionDate) predicate.ConsumptionSnapshot {
	type span struct {
		start, end time.Time
		all        bool
	}
	byProject := map[int]span{}
	for _, date := range dates {
		r, exists := byProject[date.ProjectID]
		if date.At.IsZero() {
			r.all = true
		} else {
			if !exists || date.At.Before(r.start) {
				r.start = date.At
			}
			if !exists || date.At.After(r.end) {
				r.end = date.At
			}
		}
		byProject[date.ProjectID] = r
	}
	predicates := []predicate.ConsumptionSnapshot{}
	for id, r := range byProject {
		p := consumptionsnapshot.ProjectIDIn(0, id)
		if !r.all {
			p = consumptionsnapshot.And(p, consumptionsnapshot.RangeStartLTE(r.end), consumptionsnapshot.RangeEndGT(r.start))
		}
		predicates = append(predicates, p)
	}
	return consumptionsnapshot.Or(predicates...)
}
