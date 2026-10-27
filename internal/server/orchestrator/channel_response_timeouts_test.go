package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

type channelDeadlineExecutor struct {
	deadlines []time.Duration
	failFirst bool
}

func (e *channelDeadlineExecutor) Do(ctx context.Context, _ *httpclient.Request) (*httpclient.Response, error) {
	remaining := time.Duration(0)
	if deadline, exists := ctx.Deadline(); exists {
		remaining = time.Until(deadline)
	}
	e.deadlines = append(e.deadlines, remaining)
	if len(e.deadlines) == 1 && e.failFirst {
		return nil, healthGateServerError()
	}
	return healthGateResponse(), nil
}

func (e *channelDeadlineExecutor) DoStream(ctx context.Context, _ *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestChannelTimeoutChangesWithEachAttempt(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	first.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: lo.ToPtr(2)}
	second := client.Channel.Create().SetType("openai").SetName("backup").SetBaseURL("https://backup.example/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SetStatus("enabled").SaveX(ctx)
	second.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: lo.ToPtr(7)}
	executor := &channelDeadlineExecutor{failFirst: true}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, first, second)
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	require.NoError(t, err)
	require.Len(t, executor.deadlines, 2)
	require.InDelta(t, float64(2*time.Second), float64(executor.deadlines[0]), float64(500*time.Millisecond))
	require.InDelta(t, float64(7*time.Second), float64(executor.deadlines[1]), float64(500*time.Millisecond))
}

func TestChannelTimeoutWithoutRetryAndOuterDeadline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		outer time.Duration
		want  time.Duration
	}{
		{"retry disabled", 3, 0, 3 * time.Second},
		{"channel unlimited", 0, 0, 0},
		{"outer deadline still applies", 0, time.Second, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client := setupTest(t)
			ch := createTestChannel(t, ctx, client)
			ch.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: &tc.limit}
			executor := &channelDeadlineExecutor{}
			processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, ch)
			policy := processor.SystemService.RetryPolicyOrDefault(processCtx)
			policy.Enabled = false
			require.NoError(t, processor.SystemService.SetRetryPolicy(processCtx, policy))
			if tc.outer > 0 {
				var cancel context.CancelFunc
				processCtx, cancel = context.WithTimeout(processCtx, tc.outer)
				defer cancel()
			}
			_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
			require.NoError(t, err)
			require.Len(t, executor.deadlines, 1)
			require.InDelta(t, float64(tc.want), float64(executor.deadlines[0]), float64(500*time.Millisecond))
		})
	}
}

func TestChannelFirstStreamEventTimeoutWithoutRetry(t *testing.T) {
	ctx, client := setupTest(t)
	ch := createTestChannel(t, ctx, client)
	ch.Settings = &objects.ChannelSettings{StreamFirstEventTimeoutSeconds: lo.ToPtr(1)}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, &channelDeadlineExecutor{}, ch)
	policy := processor.SystemService.RetryPolicyOrDefault(processCtx)
	policy.Enabled = false
	require.NoError(t, processor.SystemService.SetRetryPolicy(processCtx, policy))
	processCtx, cancel := context.WithTimeout(processCtx, 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", true))
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
	require.Less(t, time.Since(start), 3*time.Second)
}
