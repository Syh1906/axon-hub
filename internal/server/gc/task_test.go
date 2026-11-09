package gc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"

	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/stretchr/testify/require"
)

func claimTestTask(t *testing.T, w *Worker, ctx context.Context, task *CleanupTask) *CleanupTask {
	t.Helper()
	task, raw, err := w.readTask(ctx, w.Ent)
	require.NoError(t, err)
	task.Owner = "test-owner"
	task.Status = TaskRunning
	task.LeaseUntil = time.Now().Add(time.Minute)
	ok, err := w.writeTask(ctx, w.Ent, raw, task)
	require.NoError(t, err)
	require.True(t, ok)
	return task
}

func TestCleanupTaskDeduplicatesAndResumesAcrossWorkers(t *testing.T) {
	w, ctx, client, proj := setupBodyCleanupWorker(t)
	policy, err := w.SystemService.StoragePolicy(ctx)
	require.NoError(t, err)
	policy.Maintenance = biz.DefaultMaintenanceSettings()
	policy.Maintenance.BatchSize = 1
	require.NoError(t, w.SystemService.SetStoragePolicy(ctx, policy))
	for range 3 {
		createBodyTestRequest(t, ctx, client, proj.ID, time.Now().AddDate(0, 0, -10), objects.JSONRawMessage(`{"prompt":"old"}`), objects.JSONRawMessage(`{}`))
	}
	input := TriggerGcCleanupInput{RequestBodiesCleanupDays: 7, SubmissionKey: "submit-once"}
	first, err := w.StartCleanup(ctx, input, false)
	require.NoError(t, err)
	other := &Worker{Ent: client, SystemService: w.SystemService, DataStorageService: w.DataStorageService}
	duplicate, err := other.StartCleanup(ctx, TriggerGcCleanupInput{UsageLogsCleanupDays: 60}, true)
	require.NoError(t, err)
	require.Equal(t, first.ID, duplicate.ID)
	task := claimTestTask(t, w, ctx, first)
	task, err = w.runTaskBatch(ctx, task.ID, task.Owner)
	require.NoError(t, err)
	require.Equal(t, 1, task.Stages[0].ProcessedCount)
	paused, err := other.SetCleanupPaused(ctx, task.ID, true)
	require.NoError(t, err)
	require.True(t, paused.PauseRequested)
	task, err = w.runTaskBatch(ctx, task.ID, task.Owner)
	require.NoError(t, err)
	require.Equal(t, TaskPaused, task.Status)
	task, err = other.SetCleanupPaused(ctx, task.ID, false)
	require.NoError(t, err)
	task = claimTestTask(t, other, ctx, task)
	for range 8 {
		if task.Status == TaskCompleted {
			break
		}
		task, err = other.runTaskBatch(ctx, task.ID, task.Owner)
		require.NoError(t, err)
	}
	require.Equal(t, TaskCompleted, task.Status)
	require.Equal(t, 3, task.Stages[0].ProcessedCount)
	replayed, err := w.StartCleanup(ctx, input, false)
	require.NoError(t, err)
	require.Equal(t, first.ID, replayed.ID)
	require.Equal(t, TaskCompleted, replayed.Status)
	stale, err := w.runTaskBatch(ctx, first.ID, "old-owner")
	require.NoError(t, err)
	require.Nil(t, stale)
}

func TestCleanupTaskProtectsActiveAndUnsettledRequests(t *testing.T) {
	w, ctx, client, proj := setupBodyCleanupWorker(t)
	old := time.Now().AddDate(0, 0, -100)
	active := createBodyTestRequest(t, ctx, client, proj.ID, old, objects.JSONRawMessage(`{"keep":"active"}`), objects.JSONRawMessage(`{}`))
	client.Request.UpdateOneID(active.ID).SetStatus(request.StatusProcessing).SaveX(ctx)
	unsettled := createBodyTestRequest(t, ctx, client, proj.ID, old, objects.JSONRawMessage(`{"keep":"unsettled"}`), objects.JSONRawMessage(`{}`))
	client.BudgetCharge.Create().SetProjectID(proj.ID).SetRequestID(unsettled.ID).SetExecutionID(900).SetKeyID(1).SetKeyAccountID(1).SetEstimate(1).
		SetKeyVersions(map[budget.Period]int64{}).SetGroupVersions(map[budget.Period]int64{}).SetStatus(budgetcharge.StatusUnknown).SetCheckAfter(old).SaveX(ctx)
	safe := createBodyTestRequest(t, ctx, client, proj.ID, old, objects.JSONRawMessage(`{"remove":true}`), objects.JSONRawMessage(`{}`))
	task, err := w.StartCleanup(ctx, TriggerGcCleanupInput{RequestBodiesCleanupDays: 7}, false)
	require.NoError(t, err)
	task = claimTestTask(t, w, ctx, task)
	for range 5 {
		if task.Status == TaskCompleted {
			break
		}
		task, err = w.runTaskBatch(ctx, task.ID, task.Owner)
		require.NoError(t, err)
	}
	require.Equal(t, TaskCompleted, task.Status)
	require.Equal(t, 1, task.Stages[0].ProcessedCount)
	require.JSONEq(t, `{"keep":"active"}`, string(client.Request.GetX(ctx, active.ID).RequestBody))
	require.JSONEq(t, `{"keep":"unsettled"}`, string(client.Request.GetX(ctx, unsettled.ID).RequestBody))
	require.JSONEq(t, `{}`, string(client.Request.GetX(ctx, safe.ID).RequestBody))
}

func TestCleanupTaskBatchFailureKeepsCheckpointAndRows(t *testing.T) {
	w, ctx, client, proj := setupBodyCleanupWorker(t)
	row := createBodyTestRequest(t, ctx, client, proj.ID, time.Now().AddDate(0, 0, -10), objects.JSONRawMessage(`{"keep":true}`), objects.JSONRawMessage(`{}`))
	task, err := w.StartCleanup(ctx, TriggerGcCleanupInput{RequestBodiesCleanupDays: 7}, false)
	require.NoError(t, err)
	task = claimTestTask(t, w, ctx, task)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = w.runTaskBatch(canceled, task.ID, task.Owner)
	require.Error(t, err)
	saved, err := w.CleanupStatus(ctx)
	require.NoError(t, err)
	require.Zero(t, saved.Stages[0].ProcessedCount)
	require.JSONEq(t, `{"keep":true}`, string(client.Request.GetX(ctx, row.ID).RequestBody))
}

func TestCleanupTasksKeepUsageSelectableAfterRequestDeletion(t *testing.T) {
	w, ctx, client, proj := setupBodyCleanupWorker(t)
	old := time.Now().AddDate(0, 0, -400)
	req := createBodyTestRequest(t, ctx, client, proj.ID, old, objects.JSONRawMessage(`{}`), objects.JSONRawMessage(`{}`))
	client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(proj.ID).SetModelID("test").SetCreatedAt(old).SetTotalCost(2).SaveX(ctx)
	run := func(input TriggerGcCleanupInput) {
		task, err := w.StartCleanup(ctx, input, false)
		require.NoError(t, err)
		task = claimTestTask(t, w, ctx, task)
		for range 12 {
			if task.Status == TaskCompleted {
				break
			}
			task, err = w.runTaskBatch(ctx, task.ID, task.Owner)
			require.NoError(t, err)
		}
		require.Equal(t, TaskCompleted, task.Status)
	}
	run(TriggerGcCleanupInput{RequestsCleanupDays: 60})
	require.Zero(t, client.Request.Query().CountX(ctx))
	require.Equal(t, 1, client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).CountX(ctx))
	run(TriggerGcCleanupInput{UsageArchiveCleanupDays: 365})
	require.Equal(t, 1, client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindRequest)).CountX(ctx))
	run(TriggerGcCleanupInput{UsageLogsCleanupDays: 60})
	require.Zero(t, client.UsageLog.Query().CountX(ctx))
	require.Equal(t, 1, client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage)).CountX(ctx))
	run(TriggerGcCleanupInput{UsageArchiveCleanupDays: 365})
	require.Zero(t, client.UsageArchive.Query().CountX(ctx))
}

func TestCleanupTasksKeepExecutionStatisticsWhileRequestRetained(t *testing.T) {
	w, ctx, client, proj := setupBodyCleanupWorker(t)
	ctx = contexts.WithUser(ctx, &ent.User{ID: 1, IsOwner: true})
	old := time.Now().UTC().AddDate(0, 0, -400)
	ch := client.Channel.Create().SetType("openai").SetName("retained-statistics").
		SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SetCredentials(objects.ChannelCredentials{}).SaveX(ctx)
	req := createBodyTestRequest(t, ctx, client, proj.ID, old, objects.JSONRawMessage(`{}`), objects.JSONRawMessage(`{}`))
	client.Request.UpdateOne(req).SetChannelID(ch.ID).ExecX(ctx)
	for _, status := range []string{"failed", "completed"} {
		create := client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(proj.ID).
			SetCreatedAt(old).SetModelID("gpt-4").SetRequestBody([]byte(`{}`)).SetStatus("completed").SetChannelID(ch.ID).SetStream(true).
			SetMetricsLatencyMs(1200).SetMetricsFirstByteLatencyMs(200).SetMetricsFirstTokenLatencyMs(700)
		if status == "failed" {
			create.SetStatus("failed").SetResponseStatusCode(500)
		}
		create.SaveX(ctx)
	}
	client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(proj.ID).SetChannelID(ch.ID).
		SetModelID("gpt-4").SetCreatedAt(old).SetCompletionTokens(100).SetTotalTokens(100).SetTotalCost(2).SaveX(ctx)
	statistics := func() biz.ConsumptionMetrics {
		service := biz.NewConsumptionService(client, w.SystemService, xcache.Config{})
		result, err := service.Leaderboard(ctx, biz.ConsumptionFilter{
			Dimension: biz.ConsumptionDimensionChannel, Metric: biz.ConsumptionMetricCost, Period: biz.ConsumptionPeriodAll,
		})
		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		return result.Entries[0].Metrics
	}
	before := statistics()
	require.Equal(t, 0.5, *before.SuccessRate)
	require.Equal(t, 100.0, *before.AvgTokensPerSecond)
	run := func(input TriggerGcCleanupInput) {
		task, err := w.StartCleanup(ctx, input, false)
		require.NoError(t, err)
		task = claimTestTask(t, w, ctx, task)
		for range 12 {
			if task.Status == TaskCompleted {
				break
			}
			task, err = w.runTaskBatch(ctx, task.ID, task.Owner)
			require.NoError(t, err)
		}
		require.Equal(t, TaskCompleted, task.Status)
	}
	run(TriggerGcCleanupInput{UsageLogsCleanupDays: 60})
	require.Zero(t, client.UsageLog.Query().CountX(ctx))
	run(TriggerGcCleanupInput{UsageArchiveCleanupDays: 365})
	require.Equal(t, 1, client.Request.Query().CountX(ctx))
	require.Equal(t, 2, client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindExecution)).CountX(ctx))
	require.Equal(t, 1, client.UsageArchive.Query().Where(usagearchive.KindEQ(usagearchive.KindUsage)).CountX(ctx), "retained requests keep all numeric billing facts")
	require.Equal(t, before, statistics(), "retained requests must not lose archived speed evidence after usage cleanup")
	run(TriggerGcCleanupInput{RequestsCleanupDays: 60})
	run(TriggerGcCleanupInput{UsageArchiveCleanupDays: 365})
	require.Zero(t, client.UsageArchive.Query().CountX(ctx), "expired execution facts become removable once their sources are gone")
}

func TestCleanupTaskExpiredOwnerResumesFromCommittedCheckpoint(t *testing.T) {
	w, ctx, client, proj := setupBodyCleanupWorker(t)
	policy, err := w.SystemService.StoragePolicy(ctx)
	require.NoError(t, err)
	policy.Maintenance.BatchSize = 1
	policy.Maintenance.BatchIntervalMs = 100
	require.NoError(t, w.SystemService.SetStoragePolicy(ctx, policy))
	for range 3 {
		createBodyTestRequest(t, ctx, client, proj.ID, time.Now().AddDate(0, 0, -10), objects.JSONRawMessage(`{"fixture":true}`), objects.JSONRawMessage(`{}`))
	}
	task, err := w.StartCleanup(ctx, TriggerGcCleanupInput{RequestBodiesCleanupDays: 7}, false)
	require.NoError(t, err)
	task = claimTestTask(t, w, ctx, task)
	task, err = w.runTaskBatch(ctx, task.ID, task.Owner)
	require.NoError(t, err)
	require.Equal(t, 1, task.Stages[0].ProcessedCount)
	raw := encodedTask(task)
	task.LeaseUntil = time.Now().Add(-time.Second)
	ok, err := w.writeTask(ctx, client, raw, task)
	require.NoError(t, err)
	require.True(t, ok)
	restarted := &Worker{Ent: client, SystemService: w.SystemService, DataStorageService: w.DataStorageService}
	restarted.runAvailableCleanup(ctx)
	saved, err := restarted.CleanupStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, TaskCompleted, saved.Status)
	require.Equal(t, 3, saved.Stages[0].ProcessedCount)
	stale, err := w.runTaskBatch(ctx, task.ID, task.Owner)
	require.NoError(t, err)
	require.Nil(t, stale)
}

func TestCleanupTaskPostgresLockWaitYieldsAndResumes(t *testing.T) {
	for _, test := range []struct {
		name, code string
		status     CleanupTaskStatus
		reason     string
	}{
		{"lock timeout", "55P03", TaskWaiting, "database_busy"},
		{"constraint violation", "23503", TaskFailed, "batch_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w, ctx, client, proj := setupBodyCleanupWorker(t)
			row := createBodyTestRequest(t, ctx, client, proj.ID, time.Now().AddDate(0, 0, -10), objects.JSONRawMessage(`{"keep":true}`), objects.JSONRawMessage(`{}`))
			task, err := w.StartCleanup(ctx, TriggerGcCleanupInput{RequestBodiesCleanupDays: 7}, false)
			require.NoError(t, err)
			blocked := true
			client.Request.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					if blocked {
						return nil, fmt.Errorf("cleanup query: %w", &pgconn.PgError{Code: test.code})
					}
					return next.Query(ctx, q)
				})
			}))
			w.runAvailableCleanup(ctx)
			blocked = false
			saved, err := w.CleanupStatus(ctx)
			require.NoError(t, err)
			require.Equal(t, task.ID, saved.ID)
			require.Equal(t, test.status, saved.Status)
			require.Equal(t, test.reason, saved.WaitReason)
			require.Empty(t, saved.Owner)
			require.Zero(t, saved.Stages[0].ProcessedCount)
			require.JSONEq(t, `{"keep":true}`, string(client.Request.GetX(ctx, row.ID).RequestBody))
			if test.status == TaskWaiting {
				require.Empty(t, saved.Error)
				require.True(t, saved.NextRunAt.After(time.Now()))
				raw := encodedTask(saved)
				saved.NextRunAt = time.Time{}
				ok, err := w.writeTask(ctx, client, raw, saved)
				require.NoError(t, err)
				require.True(t, ok)
				w.runAvailableCleanup(ctx)
				completed, err := w.CleanupStatus(ctx)
				require.NoError(t, err)
				require.Equal(t, task.ID, completed.ID)
				require.Equal(t, TaskCompleted, completed.Status)
				require.Equal(t, 1, completed.Stages[0].ProcessedCount)
			} else {
				require.Equal(t, "batch_failed", saved.Error)
				w.runAvailableCleanup(ctx)
				saved, err = w.CleanupStatus(ctx)
				require.NoError(t, err)
				require.Equal(t, TaskFailed, saved.Status)
			}
		})
	}
}
