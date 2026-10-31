package gc

import (
	"context"
	"fmt"
	"github.com/looplj/axonhub/internal/ent/cacheeffectivenesswindow"
	"github.com/looplj/axonhub/internal/ent/channelprobe"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/server/biz"
	"time"
)

const previewLimit = 10000

// PreviewCleanup bounds work even on a large installation. A capped count is
// explicitly a lower bound. Payload counts are candidate rows; already-cleared
// JSON is never read during preview.
func (w *Worker) PreviewCleanup(ctx context.Context, input TriggerGcCleanupInput) ([]GcCleanupPreviewItem, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	now := time.Now().UTC()
	items := []GcCleanupPreviewItem{}
	bodyCounts := make(map[int]GcCleanupPreviewItem)
	for _, resource := range []string{biz.CleanupResourceRequestBodies, biz.CleanupResourceResponseBodies,
		biz.CleanupResourceResponseChunks, biz.CleanupResourceRequests, biz.CleanupResourceUsageLogs,
		biz.CleanupResourceCacheWindows, biz.CleanupResourceUsageArchive, biz.CleanupResourceChannelProbes} {
		days := input.days()[resource]
		if days < 0 || days > 36500 {
			return nil, fmt.Errorf("invalid cleanup retention")
		}
		if days == 0 {
			continue
		}
		item := GcCleanupPreviewItem{ResourceType: resource, RetentionDays: days, CutoffTime: now.AddDate(0, 0, -days), CountComplete: true}
		var ids []int
		var err error
		switch resource {
		case biz.CleanupResourceRequestBodies, biz.CleanupResourceResponseBodies, biz.CleanupResourceResponseChunks:
			if cached, ok := bodyCounts[days]; ok {
				item = cached
				item.ResourceType = resource
				break
			}
			ids, err = w.Ent.Request.Query().Where(request.CreatedAtLT(item.CutoffTime)).Where(safeRequests()...).Limit(previewLimit + 1).IDs(ctx)
			if err != nil {
				return nil, err
			}
			item.EstimatedCount = min(len(ids), previewLimit)
			item.CountComplete = len(ids) <= previewLimit
			if item.CountComplete {
				ids, err = w.Ent.RequestExecution.Query().Where(requestexecution.CreatedAtLT(item.CutoffTime)).Where(safeExecutions()...).Limit(previewLimit + 1 - item.EstimatedCount).IDs(ctx)
				item.EstimatedCount += len(ids)
				item.CountComplete = item.EstimatedCount <= previewLimit
				item.EstimatedCount = min(item.EstimatedCount, previewLimit)
			}
			bodyCounts[days] = item
			ids = nil
		case biz.CleanupResourceRequests:
			ids, err = w.Ent.Request.Query().Where(request.CreatedAtLT(item.CutoffTime)).Where(safeRequests()...).Limit(previewLimit + 1).IDs(ctx)
		case biz.CleanupResourceUsageLogs:
			ids, err = w.Ent.UsageLog.Query().Where(usagelog.CreatedAtLT(item.CutoffTime)).Where(safeUsageLogs()...).Limit(previewLimit + 1).IDs(ctx)
		case biz.CleanupResourceCacheWindows:
			ids, err = w.Ent.CacheEffectivenessWindow.Query().Where(cacheeffectivenesswindow.WindowEndLT(item.CutoffTime)).Limit(previewLimit + 1).IDs(ctx)
		case biz.CleanupResourceUsageArchive:
			ids, err = w.Ent.UsageArchive.Query().Where(usagearchive.OccurredAtLT(item.CutoffTime)).Modify(protectReferencedArchive).Limit(previewLimit + 1).IDs(ctx)
		case biz.CleanupResourceChannelProbes:
			ids, err = w.Ent.ChannelProbe.Query().Where(channelprobe.TimestampLT(item.CutoffTime.Unix())).Limit(previewLimit + 1).IDs(ctx)
		}
		if err != nil {
			return nil, err
		}
		if ids != nil {
			item.EstimatedCount, item.CountComplete = min(len(ids), previewLimit), len(ids) <= previewLimit
		}
		items = append(items, item)
	}
	return items, nil
}
