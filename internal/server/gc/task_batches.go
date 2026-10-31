package gc

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cacheeffectivenesswindow"
	"github.com/looplj/axonhub/internal/ent/channelprobe"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/thread"
	"github.com/looplj/axonhub/internal/ent/trace"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/server/biz"
)

func (w *Worker) processTaskStage(ctx context.Context, task *CleanupTask) error {
	if task.CurrentStage >= len(task.Stages) {
		return nil
	}
	stage := task.Stages[task.CurrentStage]
	size := task.Settings.BatchSize
	var scanned, processed int
	var done bool
	var err error
	switch stage.ResourceType {
	case biz.CleanupResourceRequestBodies, biz.CleanupResourceResponseBodies, biz.CleanupResourceResponseChunks:
		scanned, processed, done, err = w.processBodyBatch(ctx, stage, size)
	case biz.CleanupResourceRequests:
		scanned, processed, done, err = w.processRequestBatch(ctx, stage, size)
	case biz.CleanupResourceUsageLogs:
		scanned, processed, done, err = w.processUsageBatch(ctx, stage, size)
	case biz.CleanupResourceCacheWindows:
		ids, queryErr := w.Ent.CacheEffectivenessWindow.Query().Where(cacheeffectivenesswindow.WindowEndLT(stage.CutoffTime)).Order(ent.Asc(cacheeffectivenesswindow.FieldWindowEnd)).Limit(size).IDs(ctx)
		err = queryErr
		if err == nil && len(ids) > 0 {
			processed, err = w.Ent.CacheEffectivenessWindow.Delete().Where(cacheeffectivenesswindow.IDIn(ids...)).Exec(ctx)
		}
		scanned, done = len(ids), len(ids) == 0
	case biz.CleanupResourceUsageArchive:
		ids, queryErr := w.Ent.UsageArchive.Query().Where(usagearchive.OccurredAtLT(stage.CutoffTime)).
			Modify(protectReferencedArchive).Order(ent.Asc(usagearchive.FieldOccurredAt)).Limit(size).IDs(ctx)
		err = queryErr
		if err == nil && len(ids) > 0 {
			processed, err = w.Ent.UsageArchive.Delete().Where(usagearchive.IDIn(ids...)).Exec(ctx)
		}
		scanned, done = len(ids), len(ids) == 0
	case biz.CleanupResourceChannelProbes:
		ids, queryErr := w.Ent.ChannelProbe.Query().Where(channelprobe.TimestampLT(stage.CutoffTime.Unix())).Order(ent.Asc(channelprobe.FieldID)).Limit(size).IDs(ctx)
		err = queryErr
		if err == nil && len(ids) > 0 {
			processed, err = w.Ent.ChannelProbe.Delete().Where(channelprobe.IDIn(ids...)).Exec(ctx)
		}
		scanned, done = len(ids), len(ids) == 0
	default:
		return fmt.Errorf("unsupported cleanup resource %q", stage.ResourceType)
	}
	if err != nil {
		return err
	}
	stage.ScannedCount += scanned
	stage.ProcessedCount += processed
	if done {
		stage.Completed = true
		total := stage.ScannedCount
		stage.EstimatedCount = &total
		task.CurrentStage++
	}
	return nil
}

// The first query reads only a bounded page of IDs. Payload predicates are then
// evaluated for those IDs, never across the entire multi-GB JSON/TOAST table.
func (w *Worker) processBodyBatch(ctx context.Context, stage *CleanupStage, size int) (int, int, bool, error) {
	kind := bodyPayloadKind(stage.ResourceType)
	primaryID, err := w.primaryStorageID(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	cache := make(map[int]*ent.DataStorage)
	if stage.Table == "" {
		stage.Table = request.Table
	}
	if stage.Table == request.Table {
		rows, err := w.Ent.Request.Query().Select(request.FieldID, request.FieldCreatedAt).
			Where(request.CreatedAtLT(stage.CutoffTime)).Where(safeRequests()...).
			Modify(func(s *sql.Selector) { afterCheckpoint(s, stage, request.FieldCreatedAt) }).
			Order(ent.Asc(request.FieldCreatedAt), ent.Asc(request.FieldID)).Limit(size).All(ctx)
		if err != nil {
			return 0, 0, false, err
		}
		if len(rows) == 0 {
			stage.Table, stage.CursorID, stage.CursorTime = requestexecution.Table, 0, time.Time{}
			return 0, 0, false, nil
		}
		ids := make([]int, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		matches, err := w.Ent.Request.Query().Select(request.FieldID, request.FieldProjectID, request.FieldDataStorageID).
			Where(request.IDIn(ids...)).Where(safeRequests()...).Modify(func(s *sql.Selector) {
			applyBodyPayloadPredicate(s, kind, primaryID, request.FieldRequestBody, request.FieldRequestHeaders, request.FieldResponseBody, request.FieldResponseChunks, request.FieldDataStorageID)
		}).All(ctx)
		if err != nil {
			return 0, 0, false, err
		}
		ids = ids[:0]
		for _, row := range matches {
			if err := w.deleteRequestExternalPayload(ctx, row, kind, cache); err != nil {
				return 0, 0, false, err
			}
			ids = append(ids, row.ID)
		}
		if len(ids) > 0 {
			if err := w.clearRequestPayloadColumns(ctx, kind, ids); err != nil {
				return 0, 0, false, err
			}
		}
		last := rows[len(rows)-1]
		stage.CursorID, stage.CursorTime = last.ID, last.CreatedAt
		return len(rows), len(ids), false, nil
	}
	rows, err := w.Ent.RequestExecution.Query().Select(requestexecution.FieldID, requestexecution.FieldCreatedAt).
		Where(requestexecution.CreatedAtLT(stage.CutoffTime)).Where(safeExecutions()...).
		Modify(func(s *sql.Selector) { afterCheckpoint(s, stage, requestexecution.FieldCreatedAt) }).
		Order(ent.Asc(requestexecution.FieldCreatedAt), ent.Asc(requestexecution.FieldID)).Limit(size).All(ctx)
	if err != nil || len(rows) == 0 {
		return 0, 0, len(rows) == 0, err
	}
	ids := make([]int, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	matches, err := w.Ent.RequestExecution.Query().Select(requestexecution.FieldID, requestexecution.FieldProjectID, requestexecution.FieldRequestID, requestexecution.FieldDataStorageID).
		Where(requestexecution.IDIn(ids...)).Where(safeExecutions()...).Modify(func(s *sql.Selector) {
		applyBodyPayloadPredicate(s, kind, primaryID, requestexecution.FieldRequestBody, requestexecution.FieldRequestHeaders, requestexecution.FieldResponseBody, requestexecution.FieldResponseChunks, requestexecution.FieldDataStorageID)
	}).All(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	ids = ids[:0]
	for _, row := range matches {
		if err := w.deleteExecutionExternalPayload(ctx, row, kind, cache); err != nil {
			return 0, 0, false, err
		}
		ids = append(ids, row.ID)
	}
	if len(ids) > 0 {
		if err := w.clearExecutionPayloadColumns(ctx, kind, ids); err != nil {
			return 0, 0, false, err
		}
	}
	last := rows[len(rows)-1]
	stage.CursorID, stage.CursorTime = last.ID, last.CreatedAt
	return len(rows), len(ids), false, nil
}

func (w *Worker) processUsageBatch(ctx context.Context, stage *CleanupStage, size int) (int, int, bool, error) {
	rows, err := w.Ent.UsageLog.Query().Where(usagelog.CreatedAtLT(stage.CutoffTime)).Where(safeUsageLogs()...).
		Order(ent.Asc(usagelog.FieldCreatedAt), ent.Asc(usagelog.FieldID)).Limit(size).
		Select(biz.ArchivedUsageFields...).All(ctx)
	if err != nil || len(rows) == 0 {
		return 0, 0, len(rows) == 0, err
	}
	ids, requestIDs := make([]int, len(rows)), make([]int, len(rows))
	for i, row := range rows {
		ids[i], requestIDs[i] = row.ID, row.RequestID
	}
	if err := biz.ArchiveRequestStatistics(ctx, w.Ent, requestIDs); err != nil {
		return 0, 0, false, err
	}
	if err := biz.ArchiveUsageStatistics(ctx, w.Ent, rows); err != nil {
		return 0, 0, false, err
	}
	n, err := w.Ent.UsageLog.Delete().Where(usagelog.IDIn(ids...)).Exec(ctx)
	return len(rows), n, false, err
}

func (w *Worker) processRequestBatch(ctx context.Context, stage *CleanupStage, size int) (int, int, bool, error) {
	if stage.Table == trace.Table {
		ids, err := w.Ent.Trace.Query().Where(trace.CreatedAtLT(stage.CutoffTime), trace.StatusNEQ(trace.StatusRetained), trace.Not(trace.HasRequests()), trace.Not(trace.HasThreadWith(thread.StatusEQ(thread.StatusRetained)))).Limit(size).IDs(ctx)
		if err != nil {
			return 0, 0, false, err
		}
		if len(ids) == 0 {
			stage.Table = thread.Table
			return 0, 0, false, nil
		}
		n, err := w.Ent.Trace.Delete().Where(trace.IDIn(ids...)).Exec(ctx)
		return len(ids), n, false, err
	}
	if stage.Table == thread.Table {
		ids, err := w.Ent.Thread.Query().Where(thread.CreatedAtLT(stage.CutoffTime), thread.StatusNEQ(thread.StatusRetained), thread.Not(thread.HasTraces())).Limit(size).IDs(ctx)
		if err != nil || len(ids) == 0 {
			return 0, 0, len(ids) == 0, err
		}
		n, err := w.Ent.Thread.Delete().Where(thread.IDIn(ids...)).Exec(ctx)
		return len(ids), n, false, err
	}
	// One request is finalized at a time; even a historical request with many
	// attempts only removes at most size execution rows per transaction.
	row, err := w.Ent.Request.Query().Where(request.CreatedAtLT(stage.CutoffTime)).Where(safeRequests()...).
		Select(request.FieldID, request.FieldProjectID, request.FieldDataStorageID).
		Order(ent.Asc(request.FieldCreatedAt), ent.Asc(request.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		stage.Table = trace.Table
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	if err := biz.ArchiveRequestStatistics(ctx, w.Ent, []int{row.ID}); err != nil {
		return 0, 0, false, err
	}
	executions, err := w.Ent.RequestExecution.Query().Where(requestexecution.RequestIDEQ(row.ID), requestexecution.ProjectIDEQ(row.ProjectID)).
		Select(requestexecution.FieldID, requestexecution.FieldRequestID, requestexecution.FieldProjectID, requestexecution.FieldDataStorageID).Limit(size).All(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	cache := make(map[int]*ent.DataStorage)
	if len(executions) > 0 {
		ids := make([]int, len(executions))
		for i, execution := range executions {
			if err := w.deleteExecutionObjects(ctx, execution, cache); err != nil {
				return 0, 0, false, err
			}
			ids[i] = execution.ID
		}
		n, err := w.Ent.RequestExecution.Delete().Where(requestexecution.IDIn(ids...)).Exec(ctx)
		return len(ids), n, false, err
	}
	if err := w.deleteRequestObjects(ctx, row, cache); err != nil {
		return 0, 0, false, err
	}
	n, err := w.Ent.Request.Delete().Where(request.IDEQ(row.ID)).Exec(ctx)
	return 1, n, false, err
}

func (w *Worker) deleteExecutionObjects(ctx context.Context, row *ent.RequestExecution, cache map[int]*ent.DataStorage) error {
	for _, kind := range []bodyPayloadKind{bodyPayloadRequest, bodyPayloadResponse, bodyPayloadChunks} {
		if err := w.deleteExecutionExternalPayload(ctx, row, kind, cache); err != nil {
			return err
		}
	}
	return w.deleteEmptyDirectories(ctx, row.DataStorageID, cache, biz.GenerateExecutionRequestDirKey(row.ProjectID, row.RequestID, row.ID))
}

func (w *Worker) deleteRequestObjects(ctx context.Context, row *ent.Request, cache map[int]*ent.DataStorage) error {
	for _, kind := range []bodyPayloadKind{bodyPayloadRequest, bodyPayloadResponse, bodyPayloadChunks} {
		if err := w.deleteRequestExternalPayload(ctx, row, kind, cache); err != nil {
			return err
		}
	}
	return w.deleteEmptyDirectories(ctx, row.DataStorageID, cache, biz.GenerateRequestExecutionsDirKey(row.ProjectID, row.ID), biz.GenerateRequestDirKey(row.ProjectID, row.ID))
}

func (w *Worker) deleteEmptyDirectories(ctx context.Context, storageID int, cache map[int]*ent.DataStorage, keys ...string) error {
	if storageID == 0 {
		return nil
	}
	storage, err := w.getDataStorageCached(ctx, storageID, cache)
	if err != nil {
		return err
	}
	if storage.Primary || !hasRealDirectories(storage.Type) {
		return nil
	}
	for _, key := range keys {
		if err := w.deletePayloadData(ctx, storage, key); err != nil {
			return err
		}
	}
	return nil
}
