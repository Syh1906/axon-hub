package orchestrator

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

type channelDeadlineExecutor struct {
	deadlines []time.Duration
	wait      bool
}

func (e *channelDeadlineExecutor) Do(ctx context.Context, _ *httpclient.Request) (*httpclient.Response, error) {
	if e.wait {
		start := time.Now()
		<-ctx.Done()
		e.deadlines = append(e.deadlines, time.Since(start))
		return nil, ctx.Err()
	}
	remaining := time.Duration(0)
	if deadline, exists := ctx.Deadline(); exists {
		remaining = time.Until(deadline)
	}
	e.deadlines = append(e.deadlines, remaining)
	return healthGateResponse(), nil
}

func (e *channelDeadlineExecutor) DoStream(ctx context.Context, _ *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestChannelTimeoutChangesWithEachAttempt(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	first.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: lo.ToPtr(1)}
	second := client.Channel.Create().SetType("openai").SetName("backup").SetBaseURL("https://backup.example/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SetStatus("enabled").SaveX(ctx)
	second.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: lo.ToPtr(2)}
	executor := &channelDeadlineExecutor{wait: true}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, first, second)
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	require.ErrorIs(t, err, pipeline.ErrNonStreamResponseTimeout)
	require.Len(t, executor.deadlines, 2)
	require.InDelta(t, float64(time.Second), float64(executor.deadlines[0]), float64(500*time.Millisecond))
	require.InDelta(t, float64(2*time.Second), float64(executor.deadlines[1]), float64(500*time.Millisecond))
}

func TestChannelTimeoutWithoutRetryAndOuterDeadline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
		outer time.Duration
		want  time.Duration
	}{
		{"retry disabled", 1, 0, time.Second},
		{"channel unlimited", 0, 0, 0},
		{"outer deadline still applies", 0, time.Second, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client := setupTest(t)
			ch := createTestChannel(t, ctx, client)
			ch.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: &tc.limit}
			executor := &channelDeadlineExecutor{wait: tc.limit > 0 || tc.outer > 0}
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
			if executor.wait {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
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
	require.Equal(t, "upstream_first_event_timeout", ExtractErrorInfo(err).Failure.Code)
	executions := client.RequestExecution.Query().AllX(ctx)
	require.Len(t, executions, 1)
	require.Equal(t, "upstream_first_event_timeout", executions[0].Failure.Code)
	require.Less(t, time.Since(start), 3*time.Second)
}

func TestChannelAutoAggregateTimeoutKeepsFirstFailureCause(t *testing.T) {
	for _, phase := range []string{"headers", "first event", "completion"} {
		t.Run(phase, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if phase != "headers" {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					if phase == "completion" {
						_, _ = io.WriteString(w, "data: {\"id\":\"timeout-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
					}
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, client := setupTest(t)
			ch := createTestChannel(t, ctx, client)
			ch.BaseURL = server.URL
			ch.Policies.Stream = objects.CapabilityPolicyRequire
			ch.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: new(1)}
			processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, httpclient.NewHttpClient(), ch)
			processCtx, cancel := context.WithTimeout(processCtx, 4*time.Second)
			defer cancel()
			_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
			require.ErrorIs(t, err, pipeline.ErrNonStreamResponseTimeout)
			execution := client.RequestExecution.Query().OnlyX(ctx)
			require.Equal(t, requestexecution.StatusFailed, execution.Status)
			require.NotNil(t, execution.Failure)
			require.Equal(t, "upstream_response_timeout", execution.Failure.Code)
			require.NotEqual(t, "client_canceled", execution.Failure.Source)
			require.Nil(t, execution.ResponseStatusCode)
		})
	}
}

func TestChannelLateResponseTimeoutMarksAttemptBeforeRetry(t *testing.T) {
	ctx, client := setupTest(t)
	first := createTestChannel(t, ctx, client)
	first.Settings = &objects.ChannelSettings{NonStreamResponseTimeoutSeconds: new(1)}
	second := client.Channel.Create().SetType("openai").SetName("late-timeout-backup").SetBaseURL("https://backup.example/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SetStatus("enabled").SaveX(ctx)
	executor := &sequenceExecutor{steps: []executorStep{{resp: healthGateResponse()}, {resp: healthGateResponse()}}}
	processCtx, processor, _ := healthGateOrchestratorFixture(t, ctx, client, executor, first, second)
	calls := 0
	processor.Middlewares = append(processor.Middlewares, pipeline.OnInboundRawResponse("slow-first-response", func(ctx context.Context, response *httpclient.Response) (*httpclient.Response, error) {
		calls++
		if calls == 1 {
			<-ctx.Done()
		}
		return response, nil
	}))
	processCtx, cancel := context.WithTimeout(processCtx, 5*time.Second)
	defer cancel()
	_, err := processor.Process(processCtx, buildTestRequest("gpt-4", "hello", false))
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	executions := client.RequestExecution.Query().Order(ent.Asc(requestexecution.FieldID)).AllX(ctx)
	require.Len(t, executions, 2)
	require.Equal(t, requestexecution.StatusFailed, executions[0].Status)
	require.NotNil(t, executions[0].Failure)
	require.Equal(t, "upstream_response_timeout", executions[0].Failure.Code)
	require.Equal(t, requestexecution.StatusCompleted, executions[1].Status)
}
