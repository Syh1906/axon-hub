package orchestrator

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

func setupBudgetRequest(t *testing.T) (context.Context, *ent.Client, *biz.Channel, *biz.RequestService, *biz.SystemService, *biz.UsageLogService, *ent.APIKey) {
	t.Helper()
	ctx, db := setupTest(t)
	project := createTestProject(t, ctx, db)
	ch := createTestChannel(t, ctx, db)
	key := db.APIKey.Create().SetProjectID(project.ID).SetUserID(1).SetName("budget").SetKey("fixture-budget").SaveX(ctx)
	ctx = contexts.WithAPIKey(contexts.WithProjectID(ctx, project.ID), key)
	fee := decimal.RequireFromString("0.6")
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &fee}}}}
	db.ChannelModelPrice.Create().SetChannelID(ch.ID).SetModelID("gpt-4").SetPrice(price).SetReferenceID("budget-price").SaveX(ctx)
	channels, requests, system, usage := setupTestServices(t, db)
	out, err := openai.NewOutboundTransformer(ch.BaseURL, ch.Credentials.APIKey)
	require.NoError(t, err)
	loaded := &biz.Channel{Channel: ch, Outbound: out}
	channels.PreloadModelPricesForTest(ctx, loaded)
	state := budget.NewState()
	state.Policy.StartedAt = time.Now().Add(-time.Second)
	state.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 100000000}}
	db.BudgetAccount.Create().SetProjectID(project.ID).SetOwnerKind(budgetaccount.OwnerKindKey).SetOwnerID(key.ID).SetState(state).SaveX(ctx)
	return ctx, db, loaded, requests, system, usage, key
}

func TestBudgetPipelineRejectsBeforeTransportAndSettlesActualCost(t *testing.T) {
	ctx, db, ch, requests, system, usage, key := setupBudgetRequest(t)
	executor := &mockExecutor{response: &httpclient.Response{StatusCode: 200, Body: buildMockOpenAIResponse("budget-response", "gpt-4", "hello", 10, 20), Headers: http.Header{"Content-Type": []string{"application/json"}}}}
	processor := &ChatCompletionOrchestrator{channelSelector: &staticChannelSelector{candidates: channelsToTestCandidates([]*biz.Channel{ch}, "gpt-4")}, Inbound: openai.NewInboundTransformer(), RequestService: requests, ChannelService: usage.ChannelService, PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage, PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(), channelLimiterManager: NewChannelLimiterManager()}
	_, err := processor.Process(ctx, buildTestRequest("gpt-4", "hello", false))
	require.NoError(t, err)
	require.True(t, executor.requestCalled)
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.Equal(t, budgetcharge.StatusSettled, charge.Status)
	require.Equal(t, int64(60000000), *charge.Actual)
	require.Equal(t, 1, db.UsageLog.Query().CountX(ctx))
	require.Equal(t, 1, db.BudgetPosting.Query().CountX(ctx))
	executor.requestCalled = false
	_, err = processor.Process(ctx, buildTestRequest("gpt-4", "hello again", false))
	require.Error(t, err)
	require.False(t, executor.requestCalled)
	view, err := usage.BudgetService.View(ctx, key.ProjectID, "key", key.ID, "UTC")
	require.NoError(t, err)
	require.Equal(t, "0.60000000", view.Windows[0].Settled)
	require.Equal(t, "0.00000000", view.Windows[0].Reserved)
}

func TestBudgetMissingOutputLimitIsLocalFailure(t *testing.T) {
	ctx, db, ch, requests, system, usage, _ := setupBudgetRequest(t)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(1))}}}}
	db.ChannelModelPrice.Update().SetPrice(price).ExecX(ctx)
	usage.ChannelService.PreloadModelPricesForTest(ctx, ch)
	executor := &mockExecutor{}
	processor := &ChatCompletionOrchestrator{channelSelector: &staticChannelSelector{candidates: channelsToTestCandidates([]*biz.Channel{ch}, "gpt-4")}, Inbound: openai.NewInboundTransformer(), RequestService: requests, ChannelService: usage.ChannelService, PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage, PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(), channelLimiterManager: NewChannelLimiterManager()}
	_, err := processor.Process(ctx, &httpclient.Request{Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)})
	require.Error(t, err)
	require.True(t, biz.IsBudgetDenied(err))
	require.False(t, executor.requestCalled)
	require.Zero(t, db.BudgetCharge.Query().CountX(ctx))
	execution := db.RequestExecution.Query().OnlyX(ctx)
	require.NotNil(t, execution.Failure)
	require.Equal(t, "gateway", execution.Failure.Source)
	require.Equal(t, "pricing_unavailable", execution.Failure.Code)
	require.Equal(t, "budget_error", execution.Failure.Type)
	require.Nil(t, execution.ResponseStatusCode)
}

func TestBudgetRegexModelLimitWithExplicitChannelPrice(t *testing.T) {
	ctx, db, ch, requests, system, usage, _ := setupBudgetRequest(t)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(1))}}}}
	db.ChannelModelPrice.Update().SetPrice(price).ExecX(ctx)
	usage.ChannelService.PreloadModelPricesForTest(ctx, ch)
	usage.ChannelService.SetEnabledChannelsForTest([]*biz.Channel{ch})
	db.Model.Create().SetDeveloper("test").SetModelID(`^gpt-.*-sol$`).SetMatchType(objects.ModelMatchRegex).
		SetName("Sol rule").SetIcon("test").SetGroup("test").SetStatus(model.StatusEnabled).
		SetModelCard(&objects.ModelCard{Limit: objects.ModelCardLimit{Output: 128000}}).
		SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "gpt-4"}}}}).SaveX(ctx)
	executor := &mockExecutor{response: &httpclient.Response{StatusCode: 200, Body: buildMockOpenAIResponse("regex-budget", "gpt-4", "hello", 10, 20)}}
	selector := NewDefaultSelector(usage.ChannelService, biz.NewModelService(biz.ModelServiceParams{Ent: db}), system)
	processor := &ChatCompletionOrchestrator{channelSelector: selector, Inbound: openai.NewInboundTransformer(), RequestService: requests, ChannelService: usage.ChannelService, PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage, PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(), channelLimiterManager: NewChannelLimiterManager()}
	_, err := processor.Process(ctx, &httpclient.Request{Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"hello"}]}`)})
	require.NoError(t, err)
	require.True(t, executor.requestCalled)
	execution := db.RequestExecution.Query().OnlyX(ctx)
	require.EqualValues(t, 128000, execution.CostSnapshot.ModelOutputLimit)
	require.NotEmpty(t, execution.CostSnapshot.PriceReferenceID)
	require.Nil(t, execution.CostSnapshot.ModelPrice, "explicit channel price stays selected")
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.EqualValues(t, 12800000, charge.Estimate)
	require.Equal(t, budgetcharge.StatusSettled, charge.Status)
	require.Zero(t, db.BudgetAccount.Query().OnlyX(ctx).Reserved)
}

func TestBudgetFailedStreamSettlesKnownUsageAndKeepsFailure(t *testing.T) {
	ctx, db, ch, requests, _, usage, key := setupBudgetRequest(t)
	req := db.Request.Create().SetProjectID(key.ProjectID).SetAPIKeyID(key.ID).SetChannelID(ch.ID).SetModelID("gpt-4").SetStatus(request.StatusPending).SetRequestBody([]byte("{}")).SaveX(ctx)
	fee := decimal.RequireFromString("0.6")
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &fee}}}}
	snap := &objects.RequestCostSnapshot{Price: &price, PriceReferenceID: "budget-price", PricedAt: time.Now(), ManualMultiplier: decimal.NewFromInt(1), Applied: objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)}}
	exec := db.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(key.ProjectID).SetChannelID(ch.ID).SetModelID("gpt-4").SetRequestBody([]byte("{}")).SetFormat(string(llm.APIFormatOpenAIChatCompletion)).SetCostSnapshot(snap).SetStatus(requestexecution.StatusPending).SetStream(true).SaveX(ctx)
	require.NoError(t, usage.BudgetService.Reserve(ctx, key.ProjectID, key.ID, req.ID, exec.ID, 70000000, nil))
	require.NoError(t, usage.BudgetService.MarkSent(ctx, exec.ID))
	source := &sliceEventStream{events: []*httpclient.StreamEvent{{Data: []byte(`{"id":"partial","choices":[{"index":0,"delta":{"content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`)}}, err: io.ErrUnexpectedEOF}
	state := &PersistenceState{APIKey: key, Request: req, RequestExec: exec, UsageLogService: usage}
	stream := NewOutboundPersistentStream(ctx, source, req, exec, requests, usage, ch.Outbound, &biz.PerformanceRecord{StartTime: time.Now(), Stream: true}, state)
	for stream.Next() {
		stream.Current()
	}
	require.NoError(t, stream.Close())
	require.NoError(t, stream.Close())
	require.Equal(t, requestexecution.StatusFailed, db.RequestExecution.GetX(ctx, exec.ID).Status)
	require.Equal(t, budgetcharge.StatusSettled, db.BudgetCharge.Query().OnlyX(ctx).Status)
	require.Equal(t, 1, db.UsageLog.Query().CountX(ctx))
	require.Zero(t, db.BudgetAccount.Query().OnlyX(ctx).Reserved)
}

type budgetCountingExecutor struct {
	*mockExecutor
	calls int
}

func (e *budgetCountingExecutor) Do(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	e.calls++
	e.response.Request = req
	return e.mockExecutor.Do(ctx, req)
}

func TestBudgetAcceptedVideoPersistenceFailureDoesNotRetry(t *testing.T) {
	ctx, db, ch, requests, system, usage, _ := setupBudgetRequest(t)
	failOnce := true
	db.BudgetCharge.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			change, ok := m.(*ent.BudgetChargeMutation)
			if ok && failOnce {
				if _, set := change.AsyncTask(); set {
					failOnce = false
					return nil, errors.New("injected task persistence failure")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	executor := &budgetCountingExecutor{mockExecutor: &mockExecutor{response: &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"id":"task-accepted","object":"video","status":"queued","model":"gpt-4"}`)}}}
	processor := &ChatCompletionOrchestrator{channelSelector: &staticChannelSelector{candidates: channelsToTestCandidates([]*biz.Channel{ch, ch}, "gpt-4")}, Inbound: openai.NewVideoInboundTransformer(), RequestService: requests, ChannelService: usage.ChannelService, PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage, PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(), channelLimiterManager: NewChannelLimiterManager()}
	_, err := processor.Process(ctx, &httpclient.Request{Method: http.MethodPost, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"model":"gpt-4","prompt":"fixture video"}`)})
	require.Error(t, err)
	require.True(t, biz.IsBudgetDenied(err))
	require.False(t, failOnce)
	require.Equal(t, 1, executor.calls)
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.Equal(t, budgetcharge.StatusUnknown, charge.Status)
	require.Positive(t, db.BudgetAccount.Query().OnlyX(ctx).Reserved)
}
