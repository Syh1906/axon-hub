package gc

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
)

func (w *Worker) runAutomaticCleanup(ctx context.Context) {
	ctx = authz.WithSystemBypass(ctx, "gc-cleanup")
	policy, err := w.SystemService.StoragePolicy(ctx)
	if err != nil {
		log.Error(ctx, "Read automatic cleanup policy failed", log.Cause(err))
		return
	}
	input := TriggerGcCleanupInput{SubmissionKey: "scheduled:" + time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339)}
	for _, option := range policy.CleanupOptions {
		if !option.Enabled {
			continue
		}
		switch option.ResourceType {
		case biz.CleanupResourceRequests:
			input.RequestsCleanupDays = option.CleanupDays
		case biz.CleanupResourceUsageLogs:
			input.UsageLogsCleanupDays = option.CleanupDays
		case biz.CleanupResourceRequestBodies:
			input.RequestBodiesCleanupDays = option.CleanupDays
		case biz.CleanupResourceResponseBodies:
			input.ResponseBodiesCleanupDays = option.CleanupDays
		case biz.CleanupResourceResponseChunks:
			input.ResponseChunksCleanupDays = option.CleanupDays
		case biz.CleanupResourceCacheWindows:
			input.CacheWindowsCleanupDays = option.CleanupDays
		case biz.CleanupResourceUsageArchive:
			input.UsageArchiveCleanupDays = option.CleanupDays
		case biz.CleanupResourceChannelProbes:
			input.ChannelProbesCleanupDays = option.CleanupDays
		}
	}
	selected := false
	for _, days := range input.days() {
		selected = selected || days > 0
	}
	if !selected {
		return
	}
	if _, err := w.StartCleanup(ctx, input, true); err != nil {
		log.Error(ctx, "Start automatic cleanup failed", log.Cause(err))
	}
}

func cleanupSystemContext() context.Context {
	return authz.WithSystemBypass(context.Background(), "maintenance-cleanup-worker")
}
