package biz

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cacheeffectivenesswindow"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"github.com/looplj/axonhub/internal/server/scheduler"
)

const cacheWindowInterval = 5 * time.Minute
const cacheWindowSafetyLag = 15 * time.Minute

type consumptionCacheGroup struct {
	ChannelID   int    `json:"channel_id"`
	Model       string `json:"model"`
	TTL         string `json:"ttl"`
	Samples     int64  `json:"samples"`
	Eligible    int64  `json:"eligible"`
	Theoretical int64  `json:"theoretical"`
	Observed    int64  `json:"observed"`
}

func (g *consumptionCacheGroup) add(other consumptionCacheGroup) {
	g.Samples += other.Samples
	g.Eligible += other.Eligible
	g.Theoretical += other.Theoretical
	g.Observed += other.Observed
}

func (g consumptionCacheGroup) coefficient() *int {
	if g.Samples == 0 {
		return nil
	}
	value := usageanalytics.CacheCoefficient(g.Samples, g.Eligible, g.Theoretical, g.Observed)
	return &value
}

func (s *ConsumptionService) attachCacheCoefficients(ctx context.Context, projectID int, start *time.Time, end time.Time, entries []*ConsumptionEntry) error {
	query := s.client.CacheEffectivenessWindow.Query().Where(cacheeffectivenesswindow.WindowEndLTE(end))
	if projectID > 0 {
		query.Where(cacheeffectivenesswindow.ProjectIDEQ(projectID))
	}
	if start != nil {
		query.Where(cacheeffectivenesswindow.WindowEndGT(*start))
	}
	var groups []consumptionCacheGroup
	err := query.Modify(func(q *sql.Selector) {
		model := "TRIM(" + q.C("model_id") + ")"
		q.Select(q.C("channel_id"), sql.As(model, "model"),
			sql.As(sql.Sum(q.C("sample_count")), "samples"),
			sql.As(sql.Sum(q.C("eligible_count")), "eligible"),
			sql.As(sql.Sum(q.C("theoretical_tokens")), "theoretical"),
			sql.As(sql.Sum(q.C("observed_tokens")), "observed"),
		).GroupBy(q.C("channel_id"), model)
	}).Scan(ctx, &groups)
	if err != nil {
		return fmt.Errorf("query cache effectiveness: %w", err)
	}
	byChannel := make(map[int]*consumptionCacheGroup)
	byModel := make(map[int]map[string]consumptionCacheGroup)
	for _, group := range groups {
		if byChannel[group.ChannelID] == nil {
			byChannel[group.ChannelID] = &consumptionCacheGroup{}
			byModel[group.ChannelID] = make(map[string]consumptionCacheGroup)
		}
		byChannel[group.ChannelID].add(group)
		byModel[group.ChannelID][group.Model] = group
	}
	for _, entry := range entries {
		if group := byChannel[entry.ID.ID]; group != nil {
			entry.Metrics.CacheCoefficient = group.coefficient()
		}
		for _, model := range entry.Models {
			if group, ok := byModel[entry.ID.ID][model.Model]; ok {
				model.Metrics.CacheCoefficient = group.coefficient()
			}
		}
	}
	return nil
}

func (s *ConsumptionService) RegisterScheduledTasks(ctx context.Context, schedulerService *scheduler.Scheduler) error {
	return schedulerService.Register(ctx, scheduler.TaskSpec{
		Name: "consumption-cache-effectiveness", Description: "汇总渠道缓存效果", CronExpr: "*/5 * * * *", Timezone: "UTC",
	}, func(ctx context.Context) {
		if err := s.aggregateCacheWindows(ctx, time.Now()); err != nil {
			log.Error(ctx, "aggregate consumption cache effectiveness", log.Cause(err))
		}
	})
}

// Fixed five-minute windows plus a unique key replace CCH's PostgreSQL advisory
// lock. Concurrent replicas can upsert the same settled window on every dialect.
func (s *ConsumptionService) aggregateCacheWindows(ctx context.Context, now time.Time) error {
	now = now.UTC()
	return authz.RunWithSystemBypassVoid(ctx, "consumption-cache-windows", func(ctx context.Context) error {
		projects, err := s.client.Project.Query().IDs(ctx)
		if err != nil {
			return err
		}
		end := now.Add(-cacheWindowSafetyLag).Truncate(cacheWindowInterval)
		for _, projectID := range projects {
			start := now.Add(-time.Hour).Truncate(cacheWindowInterval)
			last, err := s.client.CacheEffectivenessWindow.Query().Where(cacheeffectivenesswindow.ProjectIDEQ(projectID)).Order(ent.Desc(cacheeffectivenesswindow.FieldWindowEnd)).First(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}
			if last != nil {
				start = last.WindowEnd
			}
			for windowStart := start; windowStart.Before(end); windowStart = windowStart.Add(cacheWindowInterval) {
				if err := ctx.Err(); err != nil {
					return err
				}
				// Skip idle periods in one indexed lookup instead of issuing a query
				// for every empty five-minute bucket after a long shutdown.
				next, err := s.client.Request.Query().Select(request.FieldCreatedAt).
					Where(request.ProjectIDEQ(projectID), request.CreatedAtGTE(windowStart), request.CreatedAtLT(end),
						request.HasExecutionsWith(requestexecution.CacheCompatibilityKeyNotNil())).
					Order(ent.Asc(request.FieldCreatedAt)).First(ctx)
				if ent.IsNotFound(err) {
					break
				}
				if err != nil {
					return err
				}
				windowStart = next.CreatedAt.Truncate(cacheWindowInterval)
				if err := s.writeCacheWindow(ctx, projectID, windowStart, windowStart.Add(cacheWindowInterval)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *ConsumptionService) writeCacheWindow(ctx context.Context, projectID int, start, end time.Time) error {
	start, end = start.UTC(), end.UTC()
	var groups []consumptionCacheGroup
	err := s.client.Request.Query().Where(request.ProjectIDEQ(projectID), request.CreatedAtGTE(start), request.CreatedAtLT(end)).Modify(func(q *sql.Selector) {
		u, execution := joinConsumption(q, projectID, &start, end)
		eligible := q.C("status") + " = 'completed' AND " + execution.C("status") + " = 'completed' AND " + u.C("usage_count") + " > 0"
		q.Where(sql.NotNull(execution.C("cache_compatibility_key"))).Where(sql.GT(q.C("channel_id"), 0))
		q.Select(q.C("channel_id"), sql.As(execution.C("model_id"), "model"), sql.As(execution.C("cache_ttl_bucket"), "ttl"),
			sql.As(sql.Count("*"), "samples"),
			sql.As("SUM(CASE WHEN "+eligible+" THEN 1 ELSE 0 END)", "eligible"),
			sql.As("COALESCE(SUM(CASE WHEN "+eligible+" THEN "+execution.C("theoretical_cache_tokens")+" ELSE 0 END), 0)", "theoretical"),
			sql.As("COALESCE(SUM(CASE WHEN "+eligible+" THEN "+u.C("prompt_cached_tokens")+" ELSE 0 END), 0)", "observed"),
		).GroupBy(q.C("channel_id"), execution.C("model_id"), execution.C("cache_ttl_bucket"))
	}).Scan(ctx, &groups)
	if err != nil {
		return fmt.Errorf("aggregate cache samples: %w", err)
	}
	if len(groups) == 0 {
		return nil
	}
	return s.RunInTransaction(ctx, func(ctx context.Context) error {
		client := s.entFromContext(ctx)
		for _, group := range groups {
			err := client.CacheEffectivenessWindow.Create().SetProjectID(projectID).SetChannelID(group.ChannelID).
				SetModelID(group.Model).SetCacheTTLBucket(group.TTL).SetWindowStart(start).SetWindowEnd(end).
				SetSampleCount(group.Samples).SetEligibleCount(group.Eligible).SetTheoreticalTokens(group.Theoretical).SetObservedTokens(group.Observed).
				OnConflict(sql.ConflictColumns("project_id", "channel_id", "model_id", "cache_ttl_bucket", "window_end")).UpdateNewValues().Exec(ctx)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
