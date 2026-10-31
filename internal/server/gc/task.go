package gc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
)

type CleanupTaskStatus string

const (
	TaskQueued    CleanupTaskStatus = "queued"
	TaskRunning   CleanupTaskStatus = "running"
	TaskWaiting   CleanupTaskStatus = "waiting"
	TaskPaused    CleanupTaskStatus = "paused"
	TaskCompleted CleanupTaskStatus = "completed"
	TaskFailed    CleanupTaskStatus = "failed"
)

// A single persisted task is shared by automatic and manual callers. Checkpoints
// are committed in the same transaction as their database batch.
type CleanupTask struct {
	ID             string                   `json:"id"`
	Status         CleanupTaskStatus        `json:"status"`
	Automatic      bool                     `json:"automatic"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
	FinishedAt     *time.Time               `json:"finished_at,omitempty"`
	PauseRequested bool                     `json:"pause_requested"`
	CurrentStage   int                      `json:"current_stage"`
	Stages         []*CleanupStage          `json:"stages"`
	Error          string                   `json:"error"`
	WaitReason     string                   `json:"wait_reason"`
	Owner          string                   `json:"owner,omitempty"`
	LeaseUntil     time.Time                `json:"lease_until"`
	NextRunAt      time.Time                `json:"next_run_at"`
	Settings       *biz.MaintenanceSettings `json:"settings"`
	SubmissionKey  string                   `json:"submission_key,omitempty"`
}

type CleanupStage struct {
	ResourceType   string    `json:"resource_type"`
	RetentionDays  int       `json:"retention_days"`
	CutoffTime     time.Time `json:"cutoff_time"`
	EstimatedCount *int      `json:"estimated_count,omitempty"`
	ScannedCount   int       `json:"scanned_count"`
	ProcessedCount int       `json:"processed_count"`
	Completed      bool      `json:"completed"`
	Table          string    `json:"table"`
	CursorID       int       `json:"cursor_id"`
	CursorTime     time.Time `json:"cursor_time"`
}

func (t *CleanupTask) active() bool {
	return t.Status == TaskQueued || t.Status == TaskRunning || t.Status == TaskWaiting || t.Status == TaskPaused
}

func (input TriggerGcCleanupInput) days() map[string]int {
	return map[string]int{
		biz.CleanupResourceRequests:       input.RequestsCleanupDays,
		biz.CleanupResourceUsageLogs:      input.UsageLogsCleanupDays,
		biz.CleanupResourceRequestBodies:  input.RequestBodiesCleanupDays,
		biz.CleanupResourceResponseBodies: input.ResponseBodiesCleanupDays,
		biz.CleanupResourceResponseChunks: input.ResponseChunksCleanupDays,
		biz.CleanupResourceCacheWindows:   input.CacheWindowsCleanupDays,
		biz.CleanupResourceUsageArchive:   input.UsageArchiveCleanupDays,
		biz.CleanupResourceChannelProbes:  input.ChannelProbesCleanupDays,
	}
}

func (w *Worker) StartCleanup(ctx context.Context, input TriggerGcCleanupInput, automatic bool) (*CleanupTask, error) {
	days := input.days()
	selected := false
	for _, n := range days {
		if n < 0 || n > 36500 {
			return nil, fmt.Errorf("cleanup retention must be between 1 and 36500 days, or 0 to skip")
		}
		selected = selected || n > 0
	}
	if !selected {
		return nil, fmt.Errorf("select at least one cleanup resource")
	}
	if len(input.SubmissionKey) > 128 {
		return nil, fmt.Errorf("cleanup submission key is too long")
	}
	policy, err := w.SystemService.StoragePolicy(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	task := &CleanupTask{ID: uuid.NewString(), Status: TaskQueued, Automatic: automatic,
		CreatedAt: now, UpdatedAt: now, Stages: []*CleanupStage{}, Settings: policy.Maintenance, SubmissionKey: input.SubmissionKey}
	// Remove payloads before deleting their rows. Usage archival runs before
	// request/execution deletion so attribution and historical metrics survive.
	for _, resource := range []string{biz.CleanupResourceRequestBodies, biz.CleanupResourceResponseBodies,
		biz.CleanupResourceResponseChunks, biz.CleanupResourceUsageLogs, biz.CleanupResourceRequests,
		biz.CleanupResourceCacheWindows, biz.CleanupResourceUsageArchive, biz.CleanupResourceChannelProbes} {
		if days[resource] > 0 {
			task.Stages = append(task.Stages, &CleanupStage{ResourceType: resource,
				RetentionDays: days[resource], CutoffTime: now.AddDate(0, 0, -days[resource])})
		}
	}
	// CAS and the unique system key serialize simultaneous administrators and
	// scheduled calls across processes, including retries after a lost response.
	for attempt := 0; attempt < 3; attempt++ {
		old, raw, err := w.readTask(ctx, w.Ent)
		if err != nil {
			return nil, err
		}
		if old != nil && (old.active() || (input.SubmissionKey != "" && old.SubmissionKey == input.SubmissionKey)) {
			return old, nil
		}
		ok, err := w.writeTask(ctx, w.Ent, raw, task)
		if err != nil {
			return nil, err
		}
		if ok {
			w.wakeCleanup()
			return task, nil
		}
	}
	return w.CleanupStatus(ctx)
}

func (w *Worker) CleanupStatus(ctx context.Context) (*CleanupTask, error) {
	task, _, err := w.readTask(ctx, w.Ent)
	return task, err
}

func (w *Worker) SetCleanupPaused(ctx context.Context, id string, paused bool) (*CleanupTask, error) {
	for attempt := 0; attempt < 3; attempt++ {
		task, raw, err := w.readTask(ctx, w.Ent)
		if err != nil {
			return nil, err
		}
		if task == nil || task.ID != id {
			return nil, fmt.Errorf("cleanup task no longer exists")
		}
		if task.Status == TaskCompleted {
			return task, nil
		}
		if paused {
			task.PauseRequested = true
			if task.Owner == "" || task.LeaseUntil.Before(time.Now()) {
				task.Status = TaskPaused
				task.Owner = ""
			}
		} else {
			if task.Owner != "" && task.LeaseUntil.After(time.Now()) {
				return nil, fmt.Errorf("wait for the current cleanup batch to pause")
			}
			task.Status, task.PauseRequested, task.Error, task.Owner = TaskQueued, false, "", ""
			task.NextRunAt = time.Time{}
		}
		task.UpdatedAt = time.Now().UTC()
		ok, err := w.writeTask(ctx, w.Ent, raw, task)
		if err != nil {
			return nil, err
		}
		if ok {
			if !paused {
				w.wakeCleanup()
			}
			return task, nil
		}
	}
	return nil, fmt.Errorf("cleanup task changed; refresh its status")
}

// Keep state reads on the primary when read replicas are configured. A replica
// may be behind the last committed checkpoint and must never grant ownership.
func (w *Worker) readTask(ctx context.Context, client *ent.Client) (*CleanupTask, string, error) {
	if client == w.Ent {
		tx, err := client.Tx(ctx)
		if err != nil {
			return nil, "", err
		}
		defer tx.Rollback()
		return readCleanupTask(ctx, tx.Client())
	}
	return readCleanupTask(ctx, client)
}
