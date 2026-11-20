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
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/statisticsrevision"
)

type consumptionMutation interface {
	ent.Mutation
	Client() *ent.Client
	Tx() (*ent.Tx, error)
	IDs(context.Context) ([]int, error)
	ID() (int, bool)
}
type consumptionDate struct {
	ProjectID int       `json:"project_id"`
	At        time.Time `json:"occurred_at"`
}

var consumptionStatisticFields = map[string][]string{
	"Request":          {"status", "channel_id", "result_execution_id", "failure"},
	"RequestExecution": {"status", "content_batches", "metrics_latency_ms", "metrics_first_token_latency_ms", "metrics_first_byte_latency_ms", "metrics_first_byte_recorded", "response_status_code", "error_message", "selection"},
	"UsageLog":         nil, "UsageArchive": nil, "CacheEffectivenessWindow": nil,
	"BudgetCharge": {"usage_log_id"}, "APIKey": {"name", "group_ref", "deleted_at"}, "APIKeyGroup": {"name"},
	"User": {"first_name", "last_name", "email", "deleted_at"}, "Channel": {"deleted_at"}, "Project": {"name"},
}
var consumptionSourceTables = map[string]string{"Request": "requests", "RequestExecution": "request_executions", "UsageLog": "usage_logs", "UsageArchive": "usage_archives", "BudgetCharge": "budget_charges", "CacheEffectivenessWindow": "cache_effectiveness_windows"}

func consumptionAffectsStatistics(m ent.Mutation) bool {
	fields, ok := consumptionStatisticFields[m.Type()]
	if !ok {
		return false
	}
	if m.Op().Is(ent.OpCreate) && consumptionSourceTables[m.Type()] == "" {
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
	// SQL modifiers update immutable routing/dispatch evidence without adding a
	// mutation field. Such updates still carry the automatic updated_at field.
	return (m.Type() == "Request" || m.Type() == "RequestExecution") && len(m.Fields()) == 1 && m.Fields()[0] == "updated_at"
}

func (s *ConsumptionService) snapshotInvalidation(next ent.Mutator) ent.Mutator {
	return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
		if !consumptionAffectsStatistics(mutation) {
			return next.Mutate(ctx, mutation)
		}
		m, ok := mutation.(consumptionMutation)
		if !ok {
			return nil, fmt.Errorf("unsupported consumption mutation %T", mutation)
		}
		if mutation.Type() == "RequestExecution" {
			if err := mutation.SetField("statistics_ready", false); err != nil {
				return nil, err
			}
		}
		if mutation.Type() == "UsageLog" {
			// Derived pricing is recalculated only after the source bill changes.
			if err := mutation.SetField("statistics_standard_ready", false); err != nil {
				return nil, err
			}
		}
		if (mutation.Type() == "RequestExecution" || mutation.Type() == "UsageLog") && mutation.Op().Is(ent.OpUpdate|ent.OpUpdateOne) {
			if err := mutation.AddField("statistics_version", int64(1)); err != nil {
				return nil, err
			}
		}
		return m.Client().WithStatisticsTransaction(ctx, func(ctx context.Context, client *ent.Client) (ent.Value, error) {
			metadata := consumptionSourceTables[m.Type()] == ""
			deleting := m.Op().Is(ent.OpDelete | ent.OpDeleteOne)
			var ids []int
			var dates []consumptionDate
			var owners []statisticsOwner
			reassign := m.Type() == "UsageLog" || m.Type() == "BudgetCharge" || m.Type() == "RequestExecution" && m.Op().Is(ent.OpCreate|ent.OpDelete|ent.OpDeleteOne)
			_, err := authz.RunAsHost(ctx, client, "consumption-source-dates", func(ctx context.Context) (bool, error) {
				if m.Op().Is(ent.OpCreate) {
					return true, nil
				}
				var err error
				ids, err = m.IDs(contexts.WithPrimaryRead(schematype.SkipSoftDelete(ctx)))
				if err == nil && metadata {
					dates, err = consumptionMetadataDates(ctx, client, m.Type(), ids)
				} else if err == nil && deleting {
					dates, err = consumptionChangedDates(ctx, client, m.Type(), ids)
					if err == nil && reassign {
						owners, err = statisticsExecutionOwners(ctx, client, m.Type(), ids)
					}
				}
				return err == nil, err
			})
			if err != nil {
				return nil, err
			}
			value, err := next.Mutate(ctx, mutation)
			if err != nil {
				return value, err
			}
			if m.Op().Is(ent.OpCreate) {
				if id, ok := m.ID(); ok {
					ids = []int{id}
				}
			}
			_, err = authz.RunAsHost(ctx, client, "consumption-revision", func(ctx context.Context) (bool, error) {
				if !metadata && !deleting {
					var err error
					dates, err = consumptionChangedDates(ctx, client, m.Type(), ids)
					if err != nil {
						return false, err
					}
				}
				if reassign {
					if !deleting {
						var err error
						owners, err = statisticsExecutionOwners(ctx, client, m.Type(), ids)
						if err != nil {
							return false, err
						}
					}
					if err := invalidateExecutionStatistics(ctx, client, owners); err != nil {
						return false, err
					}
				}
				return true, writeStatisticsRevisions(ctx, client, dates)
			})
			return value, err
		})
	})
}

// Identity and membership changes only affect the projects which own them.
// Channel deletion is global; names, tags and remarks are hydrated
// separately and do not invalidate numeric snapshots at all.
func consumptionMetadataDates(ctx context.Context, client *ent.Client, kind string, ids []int) ([]consumptionDate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if kind == "Channel" {
		return []consumptionDate{{ProjectID: 0}}, nil
	}
	table := map[string]string{"APIKey": "api_keys", "APIKeyGroup": "api_key_groups", "User": "api_keys", "Project": "projects"}[kind]
	match, project := "id", "project_id"
	if kind == "User" {
		match = "user_id"
	} else if kind == "Project" {
		project = "id"
	}
	t := sql.Table(table)
	t.SetDialect(client.Driver().Dialect())
	query, args := sql.Dialect(client.Driver().Dialect()).Select(sql.As(t.C(project), "project_id")).Distinct().From(t).Where(sql.InInts(t.C(match), ids...)).Query()
	var cursor sql.Rows
	if err := client.Driver().Query(ctx, query, args, &cursor); err != nil {
		return nil, err
	}
	defer cursor.Close()
	var dates []consumptionDate
	err := sql.ScanSlice(&cursor, &dates)
	return dates, err
}

func writeStatisticsRevisions(ctx context.Context, client *ent.Client, dates []consumptionDate) error {
	// UTC hours cover every reporting timezone. Boundary hours may invalidate
	// an adjacent day too, but a changed fact can never escape its revision.
	unique := map[consumptionDate]bool{}
	for _, date := range dates {
		if date.At.IsZero() {
			date.At = time.Unix(0, 0).UTC()
		} else {
			date.At = date.At.UTC().Truncate(time.Hour)
		}
		unique[date] = true
	}
	ordered := make([]consumptionDate, 0, len(unique))
	for date := range unique {
		ordered = append(ordered, date)
	}
	slices.SortFunc(ordered, func(a, b consumptionDate) int {
		if a.ProjectID < b.ProjectID {
			return -1
		}
		if a.ProjectID > b.ProjectID {
			return 1
		}
		return a.At.Compare(b.At)
	})
	for _, date := range ordered {
		if err := client.StatisticsRevision.Create().SetProjectID(date.ProjectID).SetOccurredAt(date.At).SetVersion(uuid.NewString()).
			OnConflictColumns(statisticsrevision.FieldProjectID, statisticsrevision.FieldOccurredAt).UpdateNewValues().Exec(ctx); err != nil {
			return err
		}
	}
	return nil
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
	switch kind {
	case "Request":
		query = base(source.C("created_at"))
		execution := sql.Table("request_executions").As("changed_execution")
		execution.SetDialect(db)
		query.UnionAll(base(execution.C("created_at")).Join(execution).OnP(sql.And(sql.ColumnsEQ(execution.C("request_id"), source.C("id")), sql.ColumnsEQ(execution.C("project_id"), source.C("project_id")))))
	case "CacheEffectivenessWindow":
		query = base(source.C("window_end"))
	case "UsageArchive":
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
	default:
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
