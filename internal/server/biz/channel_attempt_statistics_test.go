package biz

import (
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestChannelAttemptsPreserveFailedProviderAndArchive(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC().Add(-time.Hour)
	failedChannel := f.client.Channel.Create().SetName("failed-provider").SetType("openai").SetSupportedModels([]string{"model-a"}).SetDefaultTestModel("model-a").SetCredentials(objects.ChannelCredentials{}).SaveX(f.ctx)
	req, _ := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 100, 0, 0, 1, lo.ToPtr(int64(200)))
	f.client.RequestExecution.Delete().Where(requestexecution.RequestIDEQ(req.ID)).ExecX(f.ctx)
	f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(failedChannel.ID).
		SetModelID("model-a").SetStatus("failed").SetResponseStatusCode(400).SetRequestBody([]byte(`{}`)).SetCreatedAt(at).
		SetSelection(&objects.RequestExecutionSelection{Dispatch: objects.RequestExecutionDispatchStarted}).SaveX(f.ctx)
	f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).
		SetModelID("model-a").SetStatus("completed").SetRequestBody([]byte(`{}`)).SetCreatedAt(at.Add(time.Second)).
		SetStream(true).SetMetricsLatencyMs(1200).SetMetricsFirstByteLatencyMs(200).SetMetricsFirstTokenLatencyMs(700).SaveX(f.ctx)
	filter := ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost}
	before, err := f.service.queryGroups(f.ctx, f.project.ID, filter, nil, time.Now())
	require.NoError(t, err)
	require.Len(t, before, 2)
	for _, row := range before {
		require.EqualValues(t, 1, row.OutcomeCount)
		if row.EntityID == failedChannel.ID {
			require.Zero(t, row.SuccessCount)
			require.Zero(t, row.Cost)
		} else {
			require.EqualValues(t, 1, row.SuccessCount)
			require.InDelta(t, 100, *row.metrics().AvgTokensPerSecond, 1e-8)
			require.Equal(t, 1.0, row.Cost)
		}
	}
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{req.ID}))
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{req.ID}))
	f.client.RequestExecution.Delete().Where(requestexecution.RequestIDEQ(req.ID)).ExecX(f.ctx)
	after, err := f.service.queryGroups(f.ctx, f.project.ID, filter, nil, time.Now())
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
}

func TestChannelSpeedUsesPersistedExecutionUsageLinks(t *testing.T) {
	f := newConsumptionFixture(t)
	at := time.Now().UTC().Add(-time.Hour)
	req, first := f.addRequest(t, at, "model-a", request.StatusCompleted, 100, 100, 0, 0, 1, lo.ToPtr(int64(200)))
	firstUsage := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).OnlyX(f.ctx)
	f.client.RequestExecution.UpdateOne(first).SetStatus(requestexecution.StatusFailed).ExecX(f.ctx)
	last := f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).
		SetModelID("model-a").SetStatus("completed").SetRequestBody([]byte(`{}`)).SetCreatedAt(at.Add(time.Second)).
		SetStream(true).SetMetricsLatencyMs(2200).SetMetricsFirstByteLatencyMs(200).SetMetricsFirstTokenLatencyMs(800).SaveX(f.ctx)
	lastUsage := f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(f.channel.ID).
		SetModelID("model-a").SetCompletionTokens(1000).SetTotalTokens(1000).SetTotalCost(2).SaveX(f.ctx)
	for _, sample := range [][2]int{{first.ID, firstUsage.ID}, {last.ID, lastUsage.ID}} {
		f.client.BudgetCharge.Create().SetExecutionID(sample[0]).SetUsageLogID(sample[1]).SetProjectID(f.project.ID).
			SetRequestID(req.ID).SetKeyID(f.key.ID).SetKeyAccountID(1).SetEstimate(0).
			SetKeyVersions(map[budget.Period]int64{}).SetGroupVersions(map[budget.Period]int64{}).SetCheckAfter(at).SaveX(f.ctx)
	}
	speeds, err := RequestOutputSpeeds(f.ctx, f.client, []int{req.ID})
	require.NoError(t, err)
	require.Equal(t, 500.0, *speeds[req.ID], "last attempt uses its own 1000 tokens, not both attempts' 1100")
	groups, err := queryChannelAttemptGroups(f.ctx, f.client, f.project.ID, ConsumptionFilter{}, nil, nil, time.Now())
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.EqualValues(t, 2, groups[0].SpeedCount)
	require.Equal(t, 300.0, *groups[0].metrics().AvgTokensPerSecond, "average of actual attempt speeds 100 and 500")
	require.Equal(t, 0.5, *groups[0].metrics().SuccessRate)
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{req.ID}))
	after, err := queryChannelAttemptGroups(f.ctx, f.client, f.project.ID, ConsumptionFilter{}, nil, nil, time.Now())
	require.NoError(t, err)
	require.ElementsMatch(t, groups, after)
}
