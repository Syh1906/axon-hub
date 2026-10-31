package gc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/system"
)

const cleanupTaskKey = "maintenance_cleanup_task_v1"

func readCleanupTask(ctx context.Context, client *ent.Client) (*CleanupTask, string, error) {
	row, err := client.System.Query().Where(system.KeyEQ(cleanupTaskKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var task CleanupTask
	if err := json.Unmarshal([]byte(row.Value), &task); err != nil {
		return nil, "", fmt.Errorf("decode cleanup checkpoint: %w", err)
	}
	return &task, row.Value, nil
}

func (w *Worker) writeTask(ctx context.Context, client *ent.Client, previous string, task *CleanupTask) (bool, error) {
	encoded, err := json.Marshal(task)
	if err != nil {
		return false, err
	}
	if previous == "" {
		_, err := client.System.Create().SetKey(cleanupTaskKey).SetValue(string(encoded)).Save(ctx)
		if ent.IsConstraintError(err) {
			return false, nil
		}
		return err == nil, err
	}
	n, err := client.System.Update().Where(system.KeyEQ(cleanupTaskKey), system.ValueEQ(previous)).SetValue(string(encoded)).Save(ctx)
	return n == 1, err
}

func encodedTask(task *CleanupTask) string {
	b, _ := json.Marshal(task) // The state contains only JSON-safe structs and scalars.
	return string(b)
}
