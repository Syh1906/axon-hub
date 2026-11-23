package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/statisticsrevision"
	"github.com/stretchr/testify/require"
)

func TestStatisticsRevisionFailureRollsBackSource(t *testing.T) {
	f := newConsumptionFixture(t)
	want := errors.New("revision write failed")
	f.client.StatisticsRevision.Use(func(ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, want })
	})
	_, err := f.client.Request.Create().SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(f.channel.ID).
		SetModelID("model").SetStatus(request.StatusPending).SetRequestBody([]byte(`{}`)).Save(f.ctx)
	require.ErrorIs(t, err, want)
	require.Zero(t, f.client.Request.Query().CountX(f.ctx), "a committed bill must never lose its revision")
}

func TestStatisticsRevisionBulkAndRollback(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC()
	builders := make([]*ent.RequestCreate, 3)
	for i := range builders {
		builders[i] = f.client.Request.Create().SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(f.channel.ID).
			SetModelID("model").SetStatus(request.StatusPending).SetRequestBody([]byte(`{}`)).SetCreatedAt(at)
	}
	rows, err := f.client.Request.CreateBulk(builders...).Save(f.ctx)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	revision := f.client.StatisticsRevision.Query().Where(statisticsrevision.ProjectIDEQ(f.project.ID)).OnlyX(f.ctx)
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	_, err = tx.Request.UpdateOneID(rows[0].ID).SetStatus(request.StatusCompleted).Save(f.ctx)
	require.NoError(t, err)
	_, err = tx.Client().Debug().Request.UpdateOneID(rows[1].ID).SetStatus(request.StatusFailed).Save(f.ctx)
	require.NoError(t, err, "debug wrappers must preserve the caller's transaction")
	require.NoError(t, tx.Rollback())
	require.Equal(t, revision.Version, f.client.StatisticsRevision.GetX(f.ctx, revision.ID).Version)
	require.NotEqual(t, request.StatusCompleted, f.client.Request.GetX(f.ctx, rows[0].ID).Status)
	require.NotEqual(t, request.StatusFailed, f.client.Request.GetX(f.ctx, rows[1].ID).Status)
	// Returned bulk entities keep the original client after the implicit commit.
	_, err = rows[0].Update().SetStatus(request.StatusFailed).Save(f.ctx)
	require.NoError(t, err)
	require.NotEqual(t, revision.Version, f.client.StatisticsRevision.GetX(f.ctx, revision.ID).Version)
	_, err = f.client.Debug().Request.UpdateOneID(rows[0].ID).SetStatus(request.StatusCompleted).Save(f.ctx)
	require.NoError(t, err, "debug clients preserve the same source transaction")
}

func TestStatisticsProjectionReusesAndInvalidatesNumericFacts(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC().Add(-time.Hour)
	r, e := f.addRequest(t, at, "model", request.StatusCompleted, 100, 100, 0, 0, 2, nil)
	firstByte := int64(200)
	e.Update().SetMetricsFirstByteLatencyMs(firstByte).SaveX(f.ctx)
	start, end := at.Truncate(24*time.Hour), at.Truncate(24*time.Hour).Add(24*time.Hour)
	first, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, end)
	require.NoError(t, err)
	require.InDelta(t, 100, *first[0].AvgTokensPerSecond, 1e-9)
	require.False(t, f.client.RequestExecution.GetX(f.ctx, e.ID).StatisticsReady, "channel usage reads the sample without persisting it")
	// The attempt reader still uses stored projections. Exercise that caller
	// before each source change so the invalidation checks start from ready.
	readProjected := func() {
		t.Helper()
		_, err := queryChannelAttemptGroups(f.ctx, f.client, f.project.ID, ConsumptionFilter{}, []int{f.channel.ID}, &start, end)
		require.NoError(t, err)
		require.True(t, f.client.RequestExecution.GetX(f.ctx, e.ID).StatisticsReady)
	}
	readProjected()
	bill := f.client.UsageLog.Query().OnlyX(f.ctx)
	require.True(t, bill.StatisticsStandardReady)
	_, err = bill.Update().SetCompletionTokens(200).Save(f.ctx)
	require.NoError(t, err)
	require.False(t, f.client.RequestExecution.GetX(f.ctx, e.ID).StatisticsReady)
	require.False(t, f.client.UsageLog.GetX(f.ctx, bill.ID).StatisticsStandardReady)
	second, err := queryChannelTodayUsageModels(f.ctx, f.client, f.channel.ID, start, end)
	require.NoError(t, err)
	require.InDelta(t, 200, *second[0].AvgTokensPerSecond, 1e-9)
	require.Equal(t, int64(300), second[0].TotalTokens)
	require.Equal(t, 1, second[0].RequestCount)
	readProjected()
	// A new retry changes legacy unique-usage attribution for the older attempt.
	_, err = f.client.RequestExecution.Create().SetRequestID(r.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).
		SetModelID("model").SetStatus("completed").SetRequestBody([]byte(`{}`)).Save(f.ctx)
	require.NoError(t, err)
	require.False(t, f.client.RequestExecution.GetX(f.ctx, e.ID).StatisticsReady)
}
