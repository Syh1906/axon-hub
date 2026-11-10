package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type titleTestExecutor struct {
	mu           sync.Mutex
	models       []string
	started      []time.Time
	closed       int
	maxActive    int
	delays       map[string]time.Duration
	fail         map[string]bool
	startErrors  map[string]error
	streamErrors map[string]error
}

func (e *titleTestExecutor) Do(context.Context, *httpclient.Request) (*httpclient.Response, error) {
	return nil, errors.New("expected streaming title request")
}

func (e *titleTestExecutor) DoStream(ctx context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	model := gjson.GetBytes(request.Body, "model").String()
	e.mu.Lock()
	e.models = append(e.models, model)
	e.started = append(e.started, time.Now())
	e.maxActive = max(e.maxActive, len(e.models)-e.closed)
	if err := e.startErrors[model]; err != nil {
		e.closed++
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()
	return &titleTestStream{ctx: ctx, executor: e, model: model, delay: e.delays[model], fail: e.fail[model]}, nil
}

type titleTestStream struct {
	ctx      context.Context
	executor *titleTestExecutor
	model    string
	delay    time.Duration
	fail     bool
	index    int
	event    *httpclient.StreamEvent
	err      error
	once     sync.Once
}

func (s *titleTestStream) Next() bool {
	if s.index == 1 {
		timer := time.NewTimer(s.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			s.err = s.ctx.Err()
			return false
		}
		if s.fail {
			s.err = errors.New("title upstream failed")
			return false
		}
		if err := s.executor.streamErrors[s.model]; err != nil {
			s.err = err
			return false
		}
	}
	var data string
	switch s.index {
	case 0:
		data = fmt.Sprintf(`{"type":"response.created","response":{"id":"resp_%s","object":"response","status":"in_progress","model":%q,"output":[]}}`, s.model, s.model)
	case 1:
		data = fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg_title","output_index":0,"content_index":0,"delta":%q}`, s.model)
	case 2:
		data = fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%s","object":"response","status":"completed","model":%q,"output":[{"type":"message","id":"msg_title","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q,"annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`, s.model, s.model, s.model)
	default:
		return false
	}
	s.index++
	s.event = &httpclient.StreamEvent{Data: []byte(data)}
	return true
}

func titleGatewayWithBackup(t *testing.T, executor pipeline.Executor) (context.Context, *ChatCompletionOrchestrator, *ent.Client, *ChannelModelsCandidate, *ChannelModelsCandidate) {
	t.Helper()
	ctx, processor, client, first := responsesOriginGateway(t, executor)
	first.Models = []biz.ChannelModelEntry{{RequestModel: "gpt-6-luna", ActualModel: "mapped-primary"}}
	first.Priority = 0
	second := client.Channel.Create().SetType(channel.TypeOpenai).SetName("Title Backup").SetStatus("enabled").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture-backup"}).SetSupportedModels([]string{"entirely-different-model"}).SetDefaultTestModel("entirely-different-model").SaveX(ctx)
	outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: second.BaseURL, APIKeyProvider: biz.NewChannelAPIKeyContextProvider(auth.NewStaticKeyProvider("fixture-backup"))})
	require.NoError(t, err)
	backup := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: second, Outbound: outbound}, Priority: 1, APIFormat: string(llm.APIFormatOpenAIResponse), Models: []biz.ChannelModelEntry{{RequestModel: "gpt-6-luna", ActualModel: "entirely-different-model"}}}
	processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{first, backup}}
	processor.ChannelService.SetEnabledChannelsForTest([]*biz.Channel{first.Channel, backup.Channel})
	policy := processor.SystemService.RetryPolicyOrDefault(ctx)
	policy.MaxSingleChannelRetries, policy.MaxChannelRetries, policy.RetryDelayMs = 0, 1, 0
	policy.TraceStickyMode = biz.TraceStickyDisabled
	require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
	return ctx, processor, client, first, backup
}

func (s *titleTestStream) Current() *httpclient.StreamEvent { return s.event }
func (s *titleTestStream) Err() error                       { return s.err }
func (s *titleTestStream) Close() error {
	s.once.Do(func() {
		s.executor.mu.Lock()
		defer s.executor.mu.Unlock()
		s.executor.closed++
	})
	return nil
}

func TestTitleHedgeKeepsPrimaryAndHonorsModelMappings(t *testing.T) {
	for _, test := range []struct {
		name                                    string
		primary, backup                         time.Duration
		backupFails, single, ordinary, disabled bool
		primaryFails, canceled                  bool
		stop                                    bool
		bothReady                               bool
		startError                              error
		backupError                             error
		retryDelay                              int
		winner                                  string
		calls                                   int
	}{
		{name: "fast primary launches nothing", primary: time.Millisecond, backup: time.Millisecond, winner: "mapped-primary", calls: 1},
		{name: "backup wins", primary: 200 * time.Millisecond, backup: time.Millisecond, winner: "entirely-different-model", calls: 2},
		{name: "original wins after backup starts", primary: 70 * time.Millisecond, backup: 200 * time.Millisecond, winner: "mapped-primary", calls: 2},
		{name: "backup fails while original finishes", primary: 100 * time.Millisecond, backup: time.Millisecond, backupFails: true, winner: "mapped-primary", calls: 2},
		{name: "single channel keeps waiting", primary: 70 * time.Millisecond, single: true, winner: "mapped-primary", calls: 1},
		{name: "ordinary luna stays sequential", primary: 70 * time.Millisecond, ordinary: true, winner: "mapped-primary", calls: 1},
		{name: "disabled retries stay single", primary: 70 * time.Millisecond, disabled: true, winner: "mapped-primary", calls: 1},
		{name: "both fail without extra attempts", primary: time.Millisecond, backup: time.Millisecond, primaryFails: true, backupFails: true, calls: 2},
		{name: "client cancellation closes all attempts", primary: time.Second, backup: time.Second, canceled: true},
		{name: "primary HTTP failure respects retry delay and health", startError: healthGateServerError(), retryDelay: 80, backup: time.Millisecond, winner: "entirely-different-model", calls: 2},
		{name: "backup budget refusal leaves admitted primary running", primary: 100 * time.Millisecond, backupError: &biz.BudgetDenied{Scope: "key", Reason: "insufficient_budget"}, winner: "mapped-primary", calls: 2},
		{name: "client cancellation during backoff", startError: healthGateServerError(), retryDelay: 500, canceled: true, calls: 1},
		{name: "unsupported conversion never launches fallback", startError: transformer.ErrUnsupportedConversion, stop: true, calls: 1},
		{name: "buffer overflow never launches fallback", startError: pipeline.ErrPreCommitBufferExceeded, stop: true, calls: 1},
		{name: "oversized stream event never launches fallback", startError: httpclient.ErrStreamEventTooLarge, stop: true, calls: 1},
		{name: "both streams ready before winner selection", primary: time.Millisecond, backup: time.Millisecond, bothReady: true, calls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &titleTestExecutor{delays: map[string]time.Duration{"mapped-primary": test.primary, "entirely-different-model": test.backup}, fail: map[string]bool{"entirely-different-model": test.backupFails, "mapped-primary": test.primaryFails}, startErrors: map[string]error{"mapped-primary": test.startError, "entirely-different-model": test.backupError}}
			ctx, processor, client, first, backup := titleGatewayWithBackup(t, executor)
			candidates := []*ChannelModelsCandidate{first}
			if !test.single {
				candidates = append(candidates, backup)
			}
			processor.channelSelector = &staticChannelSelector{candidates: candidates}
			processor.ChannelService.SetEnabledChannelsForTest([]*biz.Channel{first.Channel, backup.Channel})
			if test.bothReady {
				var ready atomic.Int32
				barrier := make(chan struct{})
				processor.Middlewares = append(processor.Middlewares, pipeline.OnInboundRawStream("title-test-barrier", func(ctx context.Context, source streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
					if ready.Add(1) == 2 {
						close(barrier)
					}
					select {
					case <-barrier:
						return source, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}))
			}
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.MaxSingleChannelRetries, policy.MaxChannelRetries, policy.RetryDelayMs = 0, 1, 0
			policy.RetryDelayMs = test.retryDelay
			policy.TraceStickyMode = biz.TraceStickyDisabled
			policy.Enabled = !test.disabled
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			metadata := `{"thread_source":"thread_title"}`
			if test.ordinary {
				metadata = `{"thread_source":"user"}`
			}
			req := &httpclient.Request{Method: "POST", Headers: http.Header{"X-Codex-Turn-Metadata": {metadata}}, Body: []byte(`{"model":"gpt-6-luna","stream":true,"input":"Give this chat a title"}`)}
			processCtx := ctx
			if test.canceled {
				var cancel context.CancelFunc
				processCtx, cancel = context.WithTimeout(ctx, 65*time.Millisecond)
				defer cancel()
			}
			result, err := processor.processWithTitleHedgeDelay(processCtx, req, 25*time.Millisecond)
			if test.canceled || test.stop || test.primaryFails && test.backupFails {
				require.Error(t, err)
				require.Eventually(t, func() bool {
					executor.mu.Lock()
					defer executor.mu.Unlock()
					return len(executor.models) > 0 && executor.closed == len(executor.models)
				}, time.Second, time.Millisecond)
				require.Equal(t, 1, client.Request.Query().CountX(ctx))
				if test.canceled {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				} else if test.stop {
					require.ErrorIs(t, err, test.startError)
					require.Equal(t, 1, client.RequestExecution.Query().CountX(ctx))
				} else {
					require.Equal(t, 2, client.RequestExecution.Query().CountX(ctx))
				}
				for _, execution := range client.RequestExecution.Query().AllX(ctx) {
					require.NotEqual(t, "hedge_lost", execution.RetryDecision, "failure or user cancellation is not a speculative loss")
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result.ChatCompletionStream)
			var output strings.Builder
			for result.ChatCompletionStream.Next() {
				output.Write(result.ChatCompletionStream.Current().Data)
			}
			require.NoError(t, result.ChatCompletionStream.Err())
			if ack, ok := result.ChatCompletionStream.(streams.DeliveryAcknowledger); ok {
				ack.AcknowledgeDelivery()
			}
			require.NoError(t, result.ChatCompletionStream.Close())
			winner := test.winner
			if test.bothReady {
				winner = result.Route.Model
			}
			require.Contains(t, output.String(), winner)
			loser := "mapped-primary"
			if winner == loser {
				loser = "entirely-different-model"
			}
			require.NotContains(t, output.String(), loser, "a losing attempt must never leak into the client stream")
			require.Eventually(t, func() bool { executor.mu.Lock(); defer executor.mu.Unlock(); return executor.closed == test.calls }, time.Second, time.Millisecond)
			executor.mu.Lock()
			require.Len(t, executor.models, test.calls)
			require.LessOrEqual(t, executor.maxActive, 2)
			if test.retryDelay > 0 {
				require.GreaterOrEqual(t, executor.started[1].Sub(executor.started[0]), time.Duration(test.retryDelay)*time.Millisecond)
			}
			executor.mu.Unlock()
			require.Equal(t, 1, client.Request.Query().CountX(ctx), "hedges share one logical request")
			require.Equal(t, test.calls, client.RequestExecution.Query().CountX(ctx))
			stored := client.Request.GetX(ctx, result.RequestID)
			require.Equal(t, "completed", string(stored.Status), "loser cleanup cannot replace the winner")
			if !test.single && !test.ordinary && !test.disabled {
				require.NotNil(t, stored.ResultExecutionID)
				adopted := client.RequestExecution.GetX(ctx, *stored.ResultExecutionID)
				require.Equal(t, stored.ChannelID, adopted.ChannelID)
				require.Equal(t, result.Route.Model, adopted.ModelID)
			}
			require.Equal(t, result.Route.ChannelID, stored.ChannelID)
			require.Equal(t, winner, result.Route.Model)
			if test.startError != nil {
				cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), first.Channel)
				require.Eventually(t, func() bool {
					snapshots := healthGateSnapshot(t, processor.ChannelService.HealthGate(), first.Channel.ID, healthGateResolver(cfg))
					return len(snapshots) == 1 && snapshots[0].ConsecutiveFailures == 1
				}, time.Second, time.Millisecond, "real upstream failure must not become neutral cancellation")
			}
			if test.calls == 2 && !test.backupFails && test.startError == nil && test.backupError == nil {
				require.Eventually(t, func() bool {
					for _, execution := range client.RequestExecution.Query().AllX(ctx) {
						if execution.RetryDecision == "hedge_lost" {
							return string(execution.Status) == "completed" || execution.Failure != nil && execution.Failure.Source == "gateway"
						}
					}
					return false
				}, time.Second, time.Millisecond)
			}
		})
	}
}

func TestNonRacingTitlesKeepOrdinaryReasoningStreamBehavior(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		t.Run(fmt.Sprintf("websocket_%v", websocket), func(t *testing.T) {
			executor := &mockExecutor{streamEvents: []*httpclient.StreamEvent{
				{Data: []byte(`{"type":"response.created","response":{"id":"reasoning_title","object":"response","status":"in_progress","model":"gpt-4","output":[]}}`)},
				{Data: []byte(`{"type":"response.reasoning_summary_text.delta","item_id":"rs","output_index":0,"summary_index":0,"delta":"thinking"}`)},
				{Data: []byte(`{"type":"response.completed","response":{"id":"reasoning_title","object":"response","status":"completed","model":"gpt-4","output":[]}}`)},
			}}
			ctx, processor, _, candidate := responsesOriginGateway(t, executor)
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.MaxChannelRetries = 1
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			if websocket {
				ctx = shared.WithResponsesWebSocket(ctx)
				processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{candidate, candidate}}
			}
			result, err := processor.Process(ctx, &httpclient.Request{Headers: http.Header{"X-Codex-Turn-Metadata": {`{"thread_source":"thread_title"}`}}, Body: []byte(`{"model":"gpt-4","stream":true,"input":"title"}`)})
			require.NoError(t, err, "non-racing requests must not treat reasoning-only output as an empty title race")
			require.NotNil(t, result.ChatCompletionStream)
			for result.ChatCompletionStream.Next() {
				result.ChatCompletionStream.Current()
			}
			if ack, ok := result.ChatCompletionStream.(streams.DeliveryAcknowledger); ok {
				ack.AcknowledgeDelivery()
			}
			require.NoError(t, result.ChatCompletionStream.Close())
			require.EqualValues(t, 1, executor.requestCalls.Load())
		})
	}
}

func TestTitleHedgePreservesLivePreview(t *testing.T) {
	executor := &titleTestExecutor{delays: map[string]time.Duration{
		"mapped-primary": 70 * time.Millisecond, "entirely-different-model": 200 * time.Millisecond,
	}}
	ctx, processor, client, _, _ := titleGatewayWithBackup(t, executor)
	registry := biz.NewLiveStreamRegistry()
	processor.LiveStreamRegistry = registry
	policy := processor.SystemService.StoragePolicyOrDefault(ctx)
	policy.LivePreview = true
	require.NoError(t, processor.SystemService.SetStoragePolicy(ctx, policy))
	result, err := processor.processWithTitleHedgeDelay(ctx, &httpclient.Request{
		Method:  "POST",
		Headers: http.Header{"X-Codex-Turn-Metadata": {`{"thread_source":"thread_title"}`}},
		Body:    []byte(`{"model":"gpt-6-luna","stream":true,"input":"title"}`),
	}, 25*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = result.ChatCompletionStream.Close() })
	stored := client.Request.GetX(ctx, result.RequestID)
	require.NotNil(t, stored.ResultExecutionID)
	buffer := registry.GetRequestBuffer(result.RequestID)
	require.NotNil(t, buffer, "the adopted title stream must remain visible in the request preview")
	require.NotNil(t, registry.GetExecutionBuffer(*stored.ResultExecutionID), "attempt previews must not be globally disabled by a title race")
	for result.ChatCompletionStream.Next() {
		_ = result.ChatCompletionStream.Current()
	}
	require.NoError(t, result.ChatCompletionStream.Err())
	require.Positive(t, buffer.Len())
	result.ChatCompletionStream.(streams.DeliveryAcknowledger).AcknowledgeDelivery()
	require.NoError(t, result.ChatCompletionStream.Close())
	require.Nil(t, registry.GetRequestBuffer(result.RequestID))
	require.Eventually(t, func() bool {
		for _, execution := range client.RequestExecution.Query().AllX(ctx) {
			if registry.GetExecutionBuffer(execution.ID) != nil {
				return false
			}
		}
		return true
	}, time.Second, time.Millisecond)
}

func TestTitleHedgeScope(t *testing.T) {
	for _, metadata := range []string{"", "{broken", `{"thread_source":"memory"}`, `{"thread_source":"user","model":"gpt-6-luna"}`} {
		require.False(t, isCodexTitleRequest(&httpclient.Request{Headers: http.Header{"X-Codex-Turn-Metadata": {metadata}}}))
	}
	require.True(t, isCodexTitleRequest(&httpclient.Request{Headers: http.Header{"X-Codex-Turn-Metadata": {`{"thread_source":"thread_title","model":"other"}`}}}))
}

func TestTitleHealthPreservesFailureDuringConcurrentWinnerCleanup(t *testing.T) {
	logical, cancelClient := context.WithCancel(t.Context())
	defer cancelClient()
	attempt, cancelAttempt := context.WithCancel(context.WithValue(logical, titleRequestContextKey{}, logical))
	cancelAttempt()
	require.Equal(t, biz.HealthGateOutcomeFailure, classifyHealthGateOutcome(attempt, healthGateServerError()))
	require.Equal(t, biz.HealthGateOutcomeNeutral, classifyHealthGateOutcome(attempt, context.Canceled))
	cancelClient()
	require.Equal(t, biz.HealthGateOutcomeNeutral, classifyHealthGateOutcome(attempt, healthGateServerError()))
}

// Request hook sessions are released when each lane finishes pipeline setup;
// the optional lane owns a separate session even if it never starts.
type titleHookProbe struct {
	pipeline.DummyMiddleware

	requests atomic.Int32
	attempts atomic.Int32
	releases atomic.Int32
}

func (p *titleHookProbe) OnInboundLlmRequest(_ context.Context, req *llm.Request) (*llm.Request, error) {
	p.requests.Add(1)
	return req, nil
}
func (p *titleHookProbe) OnOutboundRawRequest(_ context.Context, req *httpclient.Request) (*httpclient.Request, error) {
	p.attempts.Add(1)
	return req, nil
}
func (p *titleHookProbe) OnProcessEnd() { p.releases.Add(1) }

type titleHookProvider struct{ primary, backup *titleHookProbe }

func (p *titleHookProvider) PassThroughApplied(context.Context, int) {}
func (p *titleHookProvider) Middleware(context.Context) (RequestHooks, error) {
	return RequestHooks{
		Request: p.primary,
		Attempt: pipeline.OnRawRequest("title-primary-hook", p.primary.OnOutboundRawRequest),
		ForkForHedge: func() (RequestHooks, error) {
			return RequestHooks{Request: p.backup, Attempt: pipeline.OnRawRequest("title-backup-hook", p.backup.OnOutboundRawRequest)}, nil
		},
	}, nil
}
func TestTitleHedgeOwnsIndependentHookSessions(t *testing.T) {
	for _, primaryFast := range []bool{true, false} {
		t.Run(fmt.Sprint("primaryFast=", primaryFast), func(t *testing.T) {
			primaryDelay := 200 * time.Millisecond
			if primaryFast {
				primaryDelay = time.Millisecond
			}
			executor := &titleTestExecutor{delays: map[string]time.Duration{"mapped-primary": primaryDelay, "entirely-different-model": time.Millisecond}}
			ctx, processor, client, _, _ := titleGatewayWithBackup(t, executor)
			hooks := &titleHookProvider{primary: &titleHookProbe{}, backup: &titleHookProbe{}}
			processor.Plugins = PluginIntegration{Hooks: hooks}
			req := &httpclient.Request{Method: "POST", Headers: http.Header{"X-Codex-Turn-Metadata": {`{"thread_source":"thread_title"}`}}, Body: []byte(`{"model":"gpt-6-luna","stream":true,"input":"Give this chat a title"}`)}
			result, err := processor.processWithTitleHedgeDelay(ctx, req, 25*time.Millisecond)
			require.NoError(t, err)
			for result.ChatCompletionStream.Next() {
				_ = result.ChatCompletionStream.Current()
			}
			if ack, ok := result.ChatCompletionStream.(streams.DeliveryAcknowledger); ok {
				ack.AcknowledgeDelivery()
			}
			require.NoError(t, result.ChatCompletionStream.Close())
			require.Equal(t, int32(1), hooks.primary.requests.Load())
			require.Equal(t, int32(1), hooks.primary.releases.Load())
			require.Eventually(t, func() bool { return hooks.backup.releases.Load() == 1 }, time.Second, time.Millisecond)
			wantBackup := int32(1)
			if primaryFast {
				wantBackup = 0
			}
			require.Equal(t, wantBackup, hooks.backup.requests.Load())
			// Each hook participates in its request stage and its attempt stage.
			require.Equal(t, int32(2), hooks.primary.attempts.Load())
			require.Equal(t, wantBackup*2, hooks.backup.attempts.Load())
			require.Equal(t, 1, client.Request.Query().CountX(ctx))
		})
	}
}

func TestTitleHedgePluginOverload(t *testing.T) {
	for _, tc := range []struct {
		name         string
		overloadAt   int32
		primaryFails bool
	}{
		{name: "primary refuses before backup", overloadAt: 1},
		{name: "backup refuses while primary succeeds", overloadAt: 2},
		{name: "backup refusal stops further primary dispatch", overloadAt: 2, primaryFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &titleTestExecutor{
				delays:       map[string]time.Duration{"mapped-primary": 100 * time.Millisecond},
				streamErrors: map[string]error{},
			}
			if tc.primaryFails {
				executor.streamErrors["mapped-primary"] = healthGateServerError()
			}
			ctx, processor, client, first, _ := titleGatewayWithBackup(t, executor)
			first.Models = append(first.Models, biz.ChannelModelEntry{RequestModel: "gpt-6-luna", ActualModel: "never-dispatch"})
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.MaxSingleChannelRetries = 1
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			var hookCalls atomic.Int32
			overloaded := NewPluginOverloadedError(&llm.ResponseError{
				StatusCode: http.StatusTooManyRequests,
				Detail:     llm.ErrorDetail{Code: "plugin_overloaded", Type: "rate_limit_error", Message: "fixture runtime pool is busy"},
			})
			processor.Plugins = PluginIntegration{Hooks: hookProvider{middleware: pipeline.OnRawRequest("title-plugin-capacity", func(_ context.Context, req *httpclient.Request) (*httpclient.Request, error) {
				if hookCalls.Add(1) == tc.overloadAt {
					return nil, overloaded
				}
				return req, nil
			})}}
			delay := time.Second
			if tc.overloadAt == 2 {
				delay = 25 * time.Millisecond
			}
			result, err := processor.processWithTitleHedgeDelay(ctx, &httpclient.Request{
				Method: "POST", Headers: http.Header{"X-Codex-Turn-Metadata": {`{"thread_source":"thread_title"}`}},
				Body: []byte(`{"model":"gpt-6-luna","stream":true,"input":"Give this chat a title"}`),
			}, delay)
			if result.ChatCompletionStream != nil {
				for result.ChatCompletionStream.Next() {
					_ = result.ChatCompletionStream.Current()
				}
				result.ChatCompletionStream.(streams.DeliveryAcknowledger).AcknowledgeDelivery()
				require.NoError(t, result.ChatCompletionStream.Close())
			}
			if tc.overloadAt == 2 && !tc.primaryFails {
				require.NoError(t, err, "optional backup refusal must not interrupt the already dispatched primary")
				require.Equal(t, first.Channel.ID, result.Route.ChannelID)
			} else {
				require.ErrorIs(t, err, overloaded)
			}
			require.Equal(t, tc.overloadAt, hookCalls.Load(), "no hook retry after overload")
			executor.mu.Lock()
			models := append([]string(nil), executor.models...)
			executor.mu.Unlock()
			if tc.overloadAt == 1 {
				require.Empty(t, models)
				require.Zero(t, client.RequestExecution.Query().CountX(ctx))
			} else {
				require.Equal(t, []string{"mapped-primary"}, models)
				require.Equal(t, 1, client.RequestExecution.Query().CountX(ctx))
			}
		})
	}
}
