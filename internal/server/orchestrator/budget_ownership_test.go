package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/stretchr/testify/require"
)

type lostBudgetOwnerExecutor struct {
	mockExecutor
	db               *ent.Client
	service          *biz.BudgetService
	calls            int
	immediateFailure bool
}

func (e *lostBudgetOwnerExecutor) revoke(ctx context.Context) error {
	charge, err := e.db.BudgetCharge.Query().Order(ent.Desc(budgetcharge.FieldID)).First(ctx)
	if err != nil {
		return err
	}
	if err := e.db.BudgetCharge.UpdateOneID(charge.ID).SetOwnerExpiresAt(time.Now().Add(-time.Minute)).Exec(ctx); err != nil {
		return err
	}
	return e.service.RecoverAttempt(ctx, charge.ExecutionID)
}

func (e *lostBudgetOwnerExecutor) Do(ctx context.Context, _ *httpclient.Request) (*httpclient.Response, error) {
	e.calls++
	if err := e.revoke(ctx); err != nil {
		return nil, err
	}
	if e.immediateFailure {
		return nil, &httpclient.Error{StatusCode: 500, Body: []byte(`{"error":{"message":"after owner revoked"}}`)}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(35 * time.Second):
		return nil, fmt.Errorf("budget owner did not cancel transport")
	}
}

func (e *lostBudgetOwnerExecutor) DoStream(ctx context.Context, _ *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	e.calls++
	if e.immediateFailure {
		if err := e.revoke(ctx); err != nil {
			return nil, err
		}
		return nil, &httpclient.Error{StatusCode: 500, Body: []byte(`{"error":{"message":"after owner revoked"}}`)}
	}
	return &lostBudgetOwnerStream{ctx: ctx}, nil
}

func TestBudgetRevokedOwnerDoesNotReplayImmediateFailure(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			ctx, db, ch, requests, system, usage, _ := setupBudgetRequest(t)
			executor := &lostBudgetOwnerExecutor{db: db, service: usage.BudgetService, immediateFailure: true}
			processor := &ChatCompletionOrchestrator{channelSelector: &staticChannelSelector{candidates: channelsToTestCandidates([]*biz.Channel{ch, ch}, "gpt-4")},
				Inbound: openai.NewInboundTransformer(), RequestService: requests, ChannelService: usage.ChannelService, PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage,
				PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(), channelLimiterManager: NewChannelLimiterManager()}
			policy := system.RetryPolicyOrDefault(ctx)
			policy.Enabled = true
			policy.MaxChannelRetries = 1
			policy.MaxSingleChannelRetries = 1
			policy.RetryDelayMs = 0
			require.NoError(t, system.SetRetryPolicy(ctx, policy))
			_, err := processor.Process(ctx, buildTestRequest("gpt-4", "hello", stream))
			require.Error(t, err)
			require.Equal(t, 1, executor.calls, "revocation must stop replay before the renewal timer observes it")
			require.Equal(t, 1, db.RequestExecution.Query().CountX(ctx))
		})
	}
}

type lostBudgetOwnerStream struct {
	ctx   context.Context
	index int
}

func (s *lostBudgetOwnerStream) Next() bool {
	if s.index < 3 {
		s.index++
		return true
	}
	select {
	case <-s.ctx.Done():
	case <-time.After(35 * time.Second):
	}
	return false
}
func (s *lostBudgetOwnerStream) Current() *httpclient.StreamEvent {
	return &httpclient.StreamEvent{Data: []byte(`{"id":"live-budget","choices":[{"index":0,"delta":{"content":"partial"}}]}`)}
}
func (s *lostBudgetOwnerStream) Err() error   { return s.ctx.Err() }
func (s *lostBudgetOwnerStream) Close() error { return nil }

func TestBudgetOwnerLossStopsTransportWithoutReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			ctx, db, ch, requests, system, usage, _ := setupBudgetRequest(t)
			executor := &lostBudgetOwnerExecutor{db: db, service: usage.BudgetService}
			processor := &ChatCompletionOrchestrator{
				channelSelector: &staticChannelSelector{candidates: channelsToTestCandidates([]*biz.Channel{ch, ch}, "gpt-4")},
				Inbound:         openai.NewInboundTransformer(), RequestService: requests, ChannelService: usage.ChannelService,
				PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage,
				PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(), channelLimiterManager: NewChannelLimiterManager(),
			}
			policy := system.RetryPolicyOrDefault(ctx)
			policy.Enabled = true
			policy.MaxChannelRetries = 1
			policy.MaxSingleChannelRetries = 1
			require.NoError(t, system.SetRetryPolicy(ctx, policy))
			result, err := processor.Process(ctx, buildTestRequest("gpt-4", "hello", stream))
			if stream {
				require.NoError(t, err)
				require.NotNil(t, result.ChatCompletionStream)
				require.NoError(t, executor.revoke(ctx), "revoke only after Process hands the live stream to its caller")
			}
			if result.ChatCompletionStream != nil {
				for result.ChatCompletionStream.Next() {
					result.ChatCompletionStream.Current()
				}
				if err == nil {
					err = result.ChatCompletionStream.Err()
				}
				_ = result.ChatCompletionStream.Close()
			}
			require.Error(t, err)
			require.True(t, biz.IsBudgetDenied(err) || errors.Is(err, httpclient.ErrRequestMayHaveExecuted), "owner loss must survive error conversion: %v", err)
			require.Equal(t, 1, executor.calls)
			require.Equal(t, 1, db.RequestExecution.Query().CountX(ctx))
			charge := db.BudgetCharge.Query().OnlyX(ctx)
			require.Equal(t, budgetcharge.StatusUnknown, charge.Status)
			require.Nil(t, charge.Actual)
			require.NotNil(t, charge.ReservationReleasedAt)
			require.Zero(t, db.BudgetAccount.Query().OnlyX(ctx).Reserved)
		})
	}
}

func TestBudgetOwnerLossDoesNotRecordProviderFailure(t *testing.T) {
	ctx, db, ch, _, _, usage, _ := setupBudgetRequest(t)
	ch.Channel = db.Channel.UpdateOneID(ch.ID).SetPolicies(objects.ChannelPolicies{
		APIKeyAutoDisableRules: []objects.APIKeyAutoDisableRule{{StatusCodes: []int{500, 503}, Times: 1, Action: objects.APIKeyAutoDisableActionPermanentDelete}},
	}).SaveX(ctx)
	usage.ChannelService.SetEnabledChannelsForTest([]*biz.Channel{ch})
	perf := &biz.PerformanceRecord{ChannelID: ch.ID, APIKey: ch.Credentials.APIKey, StartTime: time.Now()}
	outbound := &PersistentOutboundTransformer{state: &PersistenceState{Perf: perf, ChannelService: usage.ChannelService}}
	err := fmt.Errorf("%w: %w", httpclient.ErrRequestMayHaveExecuted, &biz.BudgetDenied{Reason: "accounting_unavailable"})
	withPerformanceRecording(outbound).OnOutboundRawError(ctx, err)
	require.False(t, perf.RequestCompleted, "local owner failure must not queue a provider failure record")
	require.Zero(t, perf.ResponseStatusCode)
	// This is the same consumer used by the asynchronous performance worker;
	// an uncompleted record must not activate the destructive 500/503 rule.
	usage.ChannelService.RecordPerformance(ctx, perf)
	updated := db.Channel.GetX(ctx, ch.ID)
	require.Equal(t, ch.Credentials, updated.Credentials)
	require.Empty(t, updated.DisabledAPIKeys)
	withPerformanceRecording(outbound).OnOutboundRawError(ctx, &httpclient.Error{StatusCode: 500})
	require.True(t, perf.RequestCompleted, "real provider failures still enter the existing performance path")
	require.Equal(t, 500, perf.ResponseStatusCode)
}
