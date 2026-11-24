package biz

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cacheeffectivenesswindow"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
)

// 旧账目可能早于版本记录。必须查实际来源，才能把没有数据的一天直接计为零。
func (s *ConsumptionService) emptyConsumptionDay(ctx context.Context, q consumptionDayQuery) (*ConsumptionResult, string, error) {
	var version string
	result, err := readStatisticsSnapshot(ctx, s.client, true, func(client *ent.Client) (*ConsumptionResult, error) {
		at := time.Now().UTC()
		live := client.Request.Query().Where(request.CreatedAtGTE(q.Start), request.CreatedAtLT(q.End))
		archive := client.UsageArchive.Query().Where(usagearchive.OccurredAtGTE(q.Start), usagearchive.OccurredAtLT(q.End))
		if q.Project > 0 {
			live.Where(request.ProjectIDEQ(q.Project))
			archive.Where(usagearchive.ProjectIDEQ(q.Project))
		}
		checks := []func(context.Context) (bool, error){live.Exist, archive.Exist}
		if q.Filter.Dimension == ConsumptionDimensionChannel {
			attempts := client.RequestExecution.Query().Where(requestexecution.CreatedAtGTE(q.Start), requestexecution.CreatedAtLT(q.End))
			windows := client.CacheEffectivenessWindow.Query().Where(cacheeffectivenesswindow.WindowEndGT(q.Start), cacheeffectivenesswindow.WindowEndLTE(q.End))
			if q.Project > 0 {
				attempts.Where(requestexecution.ProjectIDEQ(q.Project))
				windows.Where(cacheeffectivenesswindow.ProjectIDEQ(q.Project))
			}
			checks = append(checks, attempts.Exist, windows.Exist)
		}
		for _, exists := range checks {
			found, err := exists(ctx)
			if err != nil || found {
				return nil, err
			}
		}
		reader := *s
		reader.client = client
		var err error
		version, err = reader.consumptionFingerprint(ctx, q)
		if err != nil {
			return nil, err
		}
		return &ConsumptionResult{RangeStart: &q.Start, RangeEnd: q.End, Timezone: q.Timezone, CurrencyCode: q.Currency, GeneratedAt: at, ResolvedAt: at}, nil
	})
	return result, version, err
}
