package gc

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/looplj/axonhub/internal/ent"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/log"
)

const cleanupLease = 30 * time.Second

func (w *Worker) registerLifecycle(lc fx.Lifecycle) {
	var cancel context.CancelFunc
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			var ctx context.Context
			ctx, cancel = context.WithCancel(cleanupSystemContext())
			go func() {
				defer close(done)
				defer func() {
					if r := recover(); r != nil {
						log.Error(ctx, "Cleanup runner panicked", log.Any("panic", r))
					}
				}()
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					case <-w.wake:
					}
					w.runAvailableCleanup(ctx)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}

func (w *Worker) wakeCleanup() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) runAvailableCleanup(ctx context.Context) {
	if !w.runMu.TryLock() {
		return
	}
	defer w.runMu.Unlock()
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	task, raw, err := w.readTask(claimCtx, w.Ent)
	if err != nil || task == nil {
		cancel()
		if err != nil {
			log.Warn(ctx, "Read cleanup checkpoint failed", log.Cause(err))
		}
		return
	}
	now := time.Now().UTC()
	if !task.active() || task.Status == TaskPaused || task.NextRunAt.After(now) || (task.Owner != "" && task.LeaseUntil.After(now)) {
		cancel()
		return
	}
	if task.PauseRequested {
		task.Status, task.Owner = TaskPaused, ""
		_, err = w.writeTask(claimCtx, w.Ent, raw, task)
		cancel()
		return
	}
	task.Owner, task.Status, task.WaitReason = uuid.NewString(), TaskRunning, ""
	task.LeaseUntil, task.UpdatedAt = now.Add(cleanupLease), now
	ok, err := w.writeTask(claimCtx, w.Ent, raw, task)
	cancel()
	if err != nil || !ok {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error(ctx, "Cleanup task panicked", log.Any("panic", r))
			w.finishSlice(task, TaskFailed, "internal_error")
		}
	}()
	deadline := now.Add(time.Duration(task.Settings.MaxRunSeconds) * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			w.finishSlice(task, TaskWaiting, "service_stopping")
			return
		}
		if w.databaseBusy(task.Settings.BusyConnectionsPercent) {
			w.finishSlice(task, TaskWaiting, "database_busy")
			return
		}
		batchCtx, batchCancel := context.WithTimeout(ctx, time.Duration(task.Settings.BatchTimeoutSeconds)*time.Second)
		next, err := w.runTaskBatch(batchCtx, task.ID, task.Owner)
		batchCancel()
		if err != nil {
			log.Warn(ctx, "Cleanup batch rolled back", log.String("task_id", task.ID), log.Cause(err))
			// Timeouts must release the connection and yield to request traffic.
			// A real error remains visible and requires explicit resume.
			status, reason := TaskFailed, "batch_failed"
			var postgresError *pgconn.PgError
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				status, reason = TaskWaiting, "batch_timeout"
			} else if errors.As(err, &postgresError) && postgresError.Code == "55P03" {
				// Our short lock_timeout yields to request transactions. Retry the
				// rolled-back batch after the normal delay, without user intervention.
				status, reason = TaskWaiting, "database_busy"
			}
			w.finishSlice(task, status, reason)
			return
		}
		if next == nil || next.Status != TaskRunning {
			return
		}
		task = next
		timer := time.NewTimer(time.Duration(task.Settings.BatchIntervalMs) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			w.finishSlice(task, TaskWaiting, "service_stopping")
			return
		case <-timer.C:
		}
	}
	w.finishSlice(task, TaskWaiting, "time_budget")
}

func (w *Worker) runTaskBatch(ctx context.Context, id, owner string) (*CleanupTask, error) {
	tx, err := w.Ent.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	client := tx.Client()
	ctx = ent.NewContext(ctx, client)
	// PostgreSQL lock waits must give way quickly; other drivers are bounded by
	// the per-batch context. No table rewrite/VACUUM runs on this online path.
	if w.Ent.Driver().Dialect() == dialect.Postgres {
		if err := client.Driver().Exec(ctx, "SET LOCAL lock_timeout = '250ms'", []any{}, nil); err != nil {
			return nil, err
		}
	}
	task, raw, err := readCleanupTask(ctx, client)
	if err != nil {
		return nil, err
	}
	if task == nil || task.ID != id || task.Owner != owner || task.Status != TaskRunning {
		return nil, nil
	}
	task.UpdatedAt, task.LeaseUntil = time.Now().UTC(), time.Now().UTC().Add(cleanupLease)
	if task.PauseRequested {
		task.Status, task.Owner = TaskPaused, ""
	}
	// A conditional write obtains the row lock before touching any resource.
	// All processes contend on this same row; a stale owner cannot commit work.
	ok, err := w.writeTask(ctx, client, raw, task)
	if err != nil || !ok {
		return nil, err
	}
	fenced := encodedTask(task)
	if task.Status == TaskRunning {
		dataCleanup := w.DataStorageService.NewDataCleanup(ctx)
		defer dataCleanup.Close()
		batchWorker := &Worker{Ent: client, SystemService: w.SystemService, DataStorageService: w.DataStorageService, Config: w.Config, dataCleanup: dataCleanup}
		if err := batchWorker.processTaskStage(ctx, task); err != nil {
			return nil, err
		}
		if task.CurrentStage >= len(task.Stages) {
			now := time.Now().UTC()
			task.Status, task.Owner, task.FinishedAt = TaskCompleted, "", &now
		}
		ok, err = w.writeTask(ctx, client, fenced, task)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("cleanup checkpoint ownership changed")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

func (w *Worker) finishSlice(owned *CleanupTask, status CleanupTaskStatus, reason string) {
	ctx, cancel := context.WithTimeout(cleanupSystemContext(), 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		task, raw, err := w.readTask(ctx, w.Ent)
		if err != nil || task == nil || task.ID != owned.ID || task.Owner != owned.Owner {
			return
		}
		task.Owner, task.Status, task.UpdatedAt = "", status, time.Now().UTC()
		if task.PauseRequested {
			task.Status = TaskPaused
		}
		task.WaitReason = reason
		if status == TaskFailed {
			task.Error = reason
		}
		task.NextRunAt = time.Now().UTC().Add(30 * time.Second)
		ok, err := w.writeTask(ctx, w.Ent, raw, task)
		if err != nil || ok {
			return
		}
	}
}

func (w *Worker) databaseBusy(threshold int) bool {
	var stats stdsql.DBStats
	if driver, ok := w.Ent.Driver().(*entsql.Driver); ok {
		stats = driver.DB().Stats()
	}
	if driver, ok := w.Ent.Driver().(interface{ PrimaryStats() stdsql.DBStats }); ok {
		stats = driver.PrimaryStats()
	}
	return stats.MaxOpenConnections > 0 && stats.InUse*100 >= stats.MaxOpenConnections*threshold
}
