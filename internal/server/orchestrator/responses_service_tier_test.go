package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/provider_quota"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesServiceTierOverrideModesAndScope(t *testing.T) {
	for _, mode := range []objects.ResponsesServiceTierOverride{objects.ResponsesServiceTierInherit, objects.ResponsesServiceTierStandard, objects.ResponsesServiceTierFast, objects.ResponsesServiceTierUltrafast} {
		for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket, llm.APIFormatOpenAIResponseCompact, llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
			for _, body := range []string{`{"model":"test","input":[]}`, `{"model":"test","input":[],"service_tier":"flex"}`} {
				t.Run(fmt.Sprintf("%s/%s/%s", mode, format, body), func(t *testing.T) {
					outbound := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{ResponsesServiceTierOverride: mode}}}}}}
					request := &httpclient.Request{APIFormat: string(format), Body: []byte(body)}
					got, err := applyResponsesServiceTierOverride(outbound).OnOutboundRawRequest(t.Context(), request)
					require.NoError(t, err)
					if mode == objects.ResponsesServiceTierInherit || (format != llm.APIFormatOpenAIResponse && format != llm.APIFormatOpenAIResponseWebSocket) {
						require.Same(t, request, got)
						require.JSONEq(t, body, string(got.Body))
					} else {
						require.Equal(t, string(mode), objects.ServiceTierValue(got.Body))
						require.Equal(t, "test", gjson.GetBytes(got.Body, "model").String())
					}
					require.Equal(t, body, string(request.Body), "the input request must stay reusable for another attempt")
				})
			}
		}
	}
}

func TestResponsesServiceTierOverrideWebSocketFinalization(t *testing.T) {
	ctx := shared.WithResponsesWebSocket(t.Context())
	provider, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: "https://fixture.invalid/v1", APIKeyProvider: auth.NewStaticKeyProvider("fixture"), Transport: responses.TransportWebSocket})
	require.NoError(t, err)
	raw := []byte(`{"model":"test","input":"hello","service_tier":"priority"}`)
	req, err := responses.NewInboundTransformer().TransformRequest(ctx, &httpclient.Request{Body: raw})
	require.NoError(t, err)
	wire, err := provider.TransformRequest(ctx, req)
	require.NoError(t, err)
	require.Equal(t, string(llm.APIFormatOpenAIResponseWebSocket), wire.APIFormat)
	outbound := &PersistentOutboundTransformer{wrapped: provider, state: &PersistenceState{RawRequest: &httpclient.Request{Body: raw}, CurrentCandidate: &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierStandard}}}}}}
	wire, err = applyResponsesServiceTierOverride(outbound).OnOutboundRawRequest(t.Context(), wire)
	require.NoError(t, err)
	wire, err = finalizeTransportRequest(outbound).OnOutboundRawRequest(t.Context(), wire)
	require.NoError(t, err)
	require.Equal(t, "default", objects.ServiceTierValue(wire.Body))
	evidence := requestServiceTier(outbound.state, wire, llm.APIFormatOpenAIResponseWebSocket)
	require.Equal(t, "priority", evidence.Client)
	require.Equal(t, "default", evidence.Outbound)
	require.True(t, evidence.UseOutboundTier)
	outbound.state.LlmRequest = req
	settings := outbound.GetCurrentChannel().Settings
	settings.ResponsesServiceTierOverride = objects.ResponsesServiceTierSupported
	settings.ResponsesTierSupport = &objects.ResponsesTierSupport{AllowDowngrade: true}
	wire, err = provider.TransformRequest(ctx, req)
	require.NoError(t, err)
	wire, err = applyResponsesServiceTierOverride(outbound).OnOutboundRawRequest(ctx, wire)
	require.NoError(t, err)
	wire, err = finalizeTransportRequest(outbound).OnOutboundRawRequest(ctx, wire)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire.Body, "service_tier").Exists())
	evidence = requestServiceTier(outbound.state, wire, llm.APIFormatOpenAIResponseWebSocket)
	evidence.Settings.Enabled = true
	require.Equal(t, "standard", evidence.Decision().Mode)
}

type serviceTierCaptureExecutor struct {
	mockExecutor
	requests []*httpclient.Request
}

func TestResponsesSupportedTierKeepsRoutableAlias(t *testing.T) {
	chat, responses := llm.APIFormatOpenAIChatCompletion.String(), llm.APIFormatOpenAIResponse.String()
	channel := &biz.Channel{Channel: &ent.Channel{ID: 1, Type: "openai", Endpoints: []objects.ChannelEndpoint{{APIFormat: responses}}, Settings: &objects.ChannelSettings{
		ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true},
		ModelProtocols: []objects.ModelProtocol{{Model: "alias-chat", APIFormats: []string{chat}}, {Model: "alias-responses", APIFormats: []string{responses}}},
	}}}
	req := &llm.Request{RequestType: llm.RequestTypeChat, Model: "public", APIFormat: llm.APIFormatOpenAIResponse, ServiceTier: lo.ToPtr("priority")}
	var resolved []*resolvedAssociationCandidate
	for i, alias := range []string{"alias-chat", "alias-responses"} {
		resolved = append(resolved, &resolvedAssociationCandidate{channel: channel, priority: i, models: []biz.ChannelModelEntry{{RequestModel: alias, ActualModel: "upstream"}}})
	}
	candidates := aggregateChannelModelCandidates(t.Context(), req, resolved)
	candidates = applyChannelPriorities(candidates, []objects.ChannelPriority{{ChannelID: 1, Priority: 0}})
	filtered, err := rankResponsesTiers(t.Context(), req, candidates)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Len(t, filtered[0].Models, 1)
	require.Equal(t, "alias-responses", filtered[0].Models[0].RequestModel)
	require.Equal(t, responses, filtered[0].APIFormat)
	req.APIFormat = llm.APIFormatOpenAIChatCompletion
	for _, supportsFast := range []bool{true, false} {
		channel.Settings.ResponsesTierSupport.Fast = supportsFast
		candidates = applyChannelPriorities(aggregateChannelModelCandidates(t.Context(), req, resolved), []objects.ChannelPriority{{ChannelID: 1, Priority: 0}})
		filtered, err = rankResponsesTiers(t.Context(), req, candidates)
		require.NoError(t, err)
		if supportsFast {
			require.Len(t, filtered, 2)
			require.Equal(t, responses, filtered[0].APIFormat)
		} else {
			require.Len(t, filtered, 1)
			require.Equal(t, chat, filtered[0].APIFormat)
		}
		slices.Reverse(resolved)
	}
}

func attachTierBudget(t *testing.T, ctx context.Context, processor *ChatCompletionOrchestrator, db *ent.Client, candidates ...*ChannelModelsCandidate) context.Context {
	t.Helper()
	project, _ := contexts.GetProjectID(ctx)
	key := db.APIKey.Create().SetProjectID(project).SetUserID(1).SetName("tier-test").SetKey("fixture-tier-test").SaveX(ctx)
	account := budget.NewState()
	account.Policy.StartedAt = time.Now().Add(-time.Second)
	account.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: 100000000}}
	db.BudgetAccount.Create().SetProjectID(project).SetOwnerKind(budgetaccount.OwnerKindKey).SetOwnerID(key.ID).SetState(account).SaveX(ctx)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(10))}}}, ServiceTiers: &objects.ServiceTierPrices{
		Fast:      &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(2))},
		Ultrafast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Multiplier: lo.ToPtr(decimal.NewFromInt(6))},
	}}
	for _, c := range candidates {
		c.Channel.Settings.ServiceTierBilling = &objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}
		db.ChannelModelPrice.Create().SetChannelID(c.Channel.ID).SetModelID("gpt-4").SetPrice(price).SetReferenceID(fmt.Sprintf("tier-%d", c.Channel.ID)).SaveX(ctx)
		processor.ChannelService.PreloadModelPricesForTest(ctx, c.Channel)
	}
	return contexts.WithAPIKey(ctx, key)
}

func TestResponsesTierConvertedChatDowngrade(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
		t.Run(format.String(), func(t *testing.T) {
			response := `{"id":"chat-1","model":"gpt-4","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
			factory := openai.NewOutboundTransformer
			if format == llm.APIFormatAnthropicMessage {
				factory = anthropic.NewOutboundTransformer
				response = `{"id":"msg-1","type":"message","role":"assistant","model":"gpt-4","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
			}
			executor := &mockExecutor{response: &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(response)}}
			ctx, processor, client, candidate := responsesOriginGateway(t, executor)
			chat, err := factory("https://fixture.invalid/v1", "fixture")
			require.NoError(t, err)
			settings := &objects.ChannelSettings{ResponsesTierRoutingEnabled: true, ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{AllowDowngrade: true}}
			candidate.APIFormat = string(chat.APIFormat())
			candidate.Channel.Outbound = chat
			candidate.Channel.Settings = settings
			ctx = attachTierBudget(t, ctx, processor, client, candidate)
			body := []byte(`{"model":"gpt-4","input":"hello","service_tier":"priority","max_output_tokens":10}`)
			run := func() error {
				_, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: body, JSONBody: body})
				return err
			}
			require.NoError(t, run())
			require.Equal(t, string(chat.APIFormat()), executor.lastRequest.APIFormat)
			if format == llm.APIFormatAnthropicMessage {
				require.Equal(t, "standard_only", objects.ServiceTierValue(executor.lastRequest.Body))
			} else {
				require.False(t, gjson.GetBytes(executor.lastRequest.Body, "service_tier").Exists())
			}
			evidence := client.RequestExecution.Query().OnlyX(ctx).CostSnapshot.ServiceTier
			require.Equal(t, "priority", evidence.Client)
			require.Equal(t, "standard", evidence.Decision().Mode)
			require.Equal(t, "outbound", evidence.Decision().Basis)
			charge := client.BudgetCharge.Query().OnlyX(ctx)
			require.EqualValues(t, 10000, charge.Estimate)
			require.EqualValues(t, 1000, *charge.Actual)
			settings.ResponsesTierSupport.AllowDowngrade = false
			require.ErrorContains(t, run(), "fast")
			require.EqualValues(t, 1, executor.requestCalls.Load())
		})
	}
}

func TestResponsesTierForcedExactBeforeForcedStandard(t *testing.T) {
	req := &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, ServiceTier: lo.ToPtr("priority")}
	var candidates []*ChannelModelsCandidate
	for _, mode := range []objects.ResponsesServiceTierOverride{objects.ResponsesServiceTierStandard, objects.ResponsesServiceTierFast} {
		candidates = append(candidates, &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{ResponsesServiceTierOverride: mode}}}, APIFormat: string(req.APIFormat), Models: []biz.ChannelModelEntry{{ActualModel: "m"}}})
	}
	require.False(t, usesTierRouting(req, candidates), "historical forced channels must keep their priority order")
	// An ineligible new channel must not change the relative order of older channels.
	third := *candidates[0]
	third.APIFormat = string(llm.APIFormatAnthropicMessage)
	third.Channel = &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{ResponsesTierRoutingEnabled: true, ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{}}}}
	mixed, err := rankResponsesTiers(t.Context(), req, append(slices.Clone(candidates), &third))
	require.NoError(t, err)
	require.Len(t, mixed, 2)
	require.Equal(t, mixed[0].tierPreference, mixed[1].tierPreference)
	require.Equal(t, objects.ResponsesServiceTierStandard, mixed[0].Channel.Settings.ResponsesServiceTierOverride)
	for _, c := range candidates {
		c.Channel.Settings.ResponsesTierRoutingEnabled = true
	}
	require.True(t, usesTierRouting(req, candidates))
	ranked, err := rankResponsesTiers(t.Context(), req, candidates)
	require.NoError(t, err)
	require.Equal(t, objects.ResponsesServiceTierFast, ranked[0].Channel.Settings.ResponsesServiceTierOverride)
}

func TestResponsesSupportedTierRetryAndSkip(t *testing.T) {
	executor := &serviceTierCaptureExecutor{}
	ctx, processor, client, exact := responsesOriginGateway(t, executor)
	exact.Channel.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Ultrafast: true}}
	row := client.Channel.Create().SetStatus("enabled").SetName("fast-backup").SetType("openai_responses").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
	out, err := responses.NewOutboundTransformer(row.BaseURL, "fixture")
	require.NoError(t, err)
	backup := *exact
	backup.Channel = &biz.Channel{Channel: row, Outbound: out}
	backup.Channel.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true, AllowDowngrade: true}, PassThroughBody: lo.ToPtr(true), BodyOverrideOperations: []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: "service_tier", Value: "ultrafast"}}, ServiceTierBilling: &objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisUpstream}}
	backup.Priority = -10
	ctx = attachTierBudget(t, ctx, processor, client, exact, &backup)
	processor.ChannelService.SetEnabledChannelsForTest([]*biz.Channel{exact.Channel, backup.Channel})
	processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{&backup, exact}}
	policy := processor.SystemService.RetryPolicyOrDefault(ctx)
	policy.Enabled, policy.MaxChannelRetries, policy.MaxSingleChannelRetries, policy.RetryDelayMs = true, 1, 0, 0
	require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
	run := func(input string) error {
		body := []byte(input)
		_, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: body, JSONBody: body})
		return err
	}
	ultra := `{"model":"gpt-4","input":"test","service_tier":"ultrafast","max_output_tokens":10}`
	require.NoError(t, run(ultra))
	require.Len(t, executor.requests, 2)
	require.Equal(t, "ultrafast", objects.ServiceTierValue(executor.requests[0].Body))
	require.Equal(t, "priority", objects.ServiceTierValue(executor.requests[1].Body))
	executions := client.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
	require.Equal(t, "fast", executions[1].CostSnapshot.ServiceTier.Decision().Mode)
	charges := client.BudgetCharge.Query().Order(ent.Asc("execution_id")).AllX(ctx)
	require.Len(t, charges, 2)
	require.Equal(t, charges[0].RequestID, charges[1].RequestID)
	require.Equal(t, executions[0].ID, charges[0].ExecutionID)
	require.Equal(t, executions[1].ID, charges[1].ExecutionID)
	require.EqualValues(t, 60000, charges[0].Estimate)
	require.EqualValues(t, 20000, charges[1].Estimate)
	require.Equal(t, budgetcharge.StatusSettled, charges[1].Status)
	require.EqualValues(t, 2000, *charges[1].Actual)
	usage := client.UsageLog.Query().OnlyX(ctx)
	require.InDelta(t, 0.00002, *usage.TotalCost, 1e-12)
	require.Equal(t, "outbound", usage.CostItems[0].PricingEvidence.ServiceTier.Basis)
	require.Zero(t, client.BudgetAccount.Query().OnlyX(ctx).Reserved)
	processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{&backup}}
	backup.Channel.Settings.ResponsesTierSupport.AllowDowngrade = false
	require.ErrorContains(t, run(ultra), "ultrafast")
	require.Len(t, executor.requests, 2)
	require.NoError(t, run(`{"model":"gpt-4","input":"test","max_output_tokens":10}`))
	require.False(t, gjson.GetBytes(executor.requests[2].Body, "service_tier").Exists())
	// Both eligible channels only support standard. The higher client tier must
	// neither reach the provider nor be charged when the provider omits its tier.
	exact.Channel.Settings.ResponsesTierSupport = &objects.ResponsesTierSupport{AllowDowngrade: true}
	backup.Channel.Settings.ResponsesTierSupport = &objects.ResponsesTierSupport{AllowDowngrade: true}
	processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{&backup, exact}}
	require.NoError(t, run(`{"model":"gpt-4","input":"test","service_tier":"priority","max_output_tokens":10}`))
	require.False(t, gjson.GetBytes(executor.requests[len(executor.requests)-1].Body, "service_tier").Exists())
	last := client.RequestExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
	require.Equal(t, "priority", last.CostSnapshot.ServiceTier.Client)
	require.Equal(t, "standard", last.CostSnapshot.ServiceTier.Decision().Mode)
	require.Equal(t, "outbound", last.CostSnapshot.ServiceTier.Decision().Basis)
}

func TestResponsesTierWebSocketSettlement(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		received <- objects.ServiceTierValue(message)
		err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp_ws_tier","object":"response","status":"completed","model":"gpt-4","output":[],"service_tier":"default","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
		if err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	ctx, processor, db, c := responsesOriginGateway(t, &mockExecutor{})
	c.Channel.BaseURL = server.URL
	ws, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: server.URL, APIKeyProvider: auth.NewStaticKeyProvider("fixture"), Transport: responses.TransportWebSocket})
	require.NoError(t, err)
	c.Channel.Outbound, c.APIFormat = ws, ws.APIFormat().String()
	c.Channel.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true, AllowDowngrade: true}}
	ctx = shared.WithResponsesWebSocket(attachTierBudget(t, ctx, processor, db, c))
	result, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":"test","stream":true,"service_tier":"ultrafast","max_output_tokens":10}`)})
	require.NoError(t, err)
	for result.ChatCompletionStream.Next() {
		result.ChatCompletionStream.Current()
	}
	require.NoError(t, result.ChatCompletionStream.Err())
	result.ChatCompletionStream.(streams.DeliveryAcknowledger).AcknowledgeDelivery()
	require.NoError(t, result.ChatCompletionStream.Close())
	require.Equal(t, "priority", <-received)
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.EqualValues(t, 20000, charge.Estimate)
	require.Equal(t, budgetcharge.StatusSettled, charge.Status)
	require.EqualValues(t, 1000, *charge.Actual)
	usage := db.UsageLog.Query().OnlyX(ctx)
	require.InDelta(t, 0.00001, *usage.TotalCost, 1e-12)
	tier := usage.CostItems[0].PricingEvidence.ServiceTier
	require.Equal(t, "ultrafast", tier.Client)
	require.Equal(t, "priority", tier.Outbound)
	require.Equal(t, "default", tier.Upstream)
	require.Equal(t, "upstream", tier.Basis)
	require.True(t, tier.Confirmed)
}

func TestResponsesTierContinuationEligibilityAndReplay(t *testing.T) {
	ws := llm.APIFormatOpenAIResponseWebSocket
	source, exact := stickyTestCandidate(1, 0), stickyTestCandidate(2, 1)
	for _, c := range []*ChannelModelsCandidate{source, exact} {
		c.APIFormat = ws.String()
		c.Models = []biz.ChannelModelEntry{{RequestModel: "gpt-test", ActualModel: "gpt-test"}}
		c.Channel.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true, Ultrafast: c == exact}}
	}
	provider := &continuationRouteFixture{found: true, route: biz.ResponsesContinuationRoute{ChannelID: 1}}
	selector := newLivePolicyTestSelector(&staticChannelSelector{candidates: []*ChannelModelsCandidate{source, exact}}, NewLoadBalancer(nil, nil), &mockRetryPolicyProvider{policy: &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 1}}, provider)
	lane := shared.NewResponsesLane("tiers", shared.NewResponsesBudget(64<<20))
	t.Cleanup(lane.Close)
	ctx := shared.WithSessionScope(shared.WithResponsesLane(shared.WithResponsesWebSocket(t.Context()), lane), "project:1:key:1")
	body := []byte(`{"model":"gpt-test","previous_response_id":"parent","service_tier":"ultrafast","input":"delta"}`)
	raw := &httpclient.Request{Body: body}
	req, err := responses.NewInboundTransformer().TransformRequest(ctx, raw)
	require.NoError(t, err)
	req.RawRequest = raw
	record := shared.ResponsesRecord{ID: "parent", Origin: shared.ResponsesOrigin{ChannelID: 1, Format: ws.String(), Model: "gpt-test", Generation: 1}}
	lane.Remember(record)
	require.True(t, lane.RegisterLease(record.Origin, func(string) bool { return true }, func() {}))
	_, err = selector.Select(ctx, req)
	var dispatchErr *llm.ResponseError
	require.ErrorAs(t, err, &dispatchErr)
	require.Equal(t, "service_tier_unavailable", dispatchErr.Detail.Code)
	source.Channel.Settings.ResponsesTierSupport.AllowDowngrade = true
	selected, err := selector.Select(ctx, req)
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.True(t, selected[0].continuationStrict)
	record.Snapshot = shared.NewResponsesSnapshot(nil, []json.RawMessage{json.RawMessage(`{"role":"user","content":"history"}`)}, nil)
	lane.Remember(record)
	selected, err = selector.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, exact.Channel.ID, selected[0].Channel.ID)
	c := newResponsesContinuation(nil, body)
	p := &PersistentOutboundTransformer{state: &PersistenceState{ChannelModelsCandidates: selected, CurrentCandidate: selected[0], ResponsesContinuation: c}}
	require.NoError(t, selectResponsesContinuationCandidate(ctx, p, req))
	require.Equal(t, 0, p.state.CurrentCandidateIndex)
	wire, err := prepareResponsesAttemptBody(ctx, p, responsesAttemptTarget{origin: shared.ResponsesOrigin{ChannelID: exact.Channel.ID, Model: "gpt-test", Format: ws.String()}, forwardsID: true})
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "previous_response_id").Exists())
	require.Contains(t, string(wire), "history")
	require.Contains(t, string(wire), "delta")
	// A generic body rule puts the parent ID back after the history was expanded.
	var overridden map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &overridden))
	overridden["previous_response_id"] = json.RawMessage(`"parent"`)
	withID, err := json.Marshal(overridden)
	require.NoError(t, err)
	final, err := verifyResponsesAttempt(p).OnOutboundRawRequest(ctx, &httpclient.Request{Body: withID, APIFormat: ws.String()})
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(final.Body, "previous_response_id").Exists())
	require.Equal(t, gjson.GetBytes(wire, "input").Raw, gjson.GetBytes(final.Body, "input").Raw)
	require.True(t, responsesReplayFits(64<<20, 64<<20))
	require.False(t, responsesReplayFits(64<<20, (64<<20)+1))
	exact.Channel.Settings.ResponsesTierSupport.Ultrafast = false
	exact.Channel.Settings.ResponsesTierSupport.AllowDowngrade = true
	selected, err = selector.Select(ctx, req)
	require.NoError(t, err)
	require.Len(t, selected, 1, "no other route improves the tier; keep the original binding")
	require.True(t, selected[0].continuationStrict)
	provider.route.ChannelID = 3
	_, err = selector.Select(ctx, req)
	require.ErrorAs(t, err, &dispatchErr)
	require.Equal(t, "continuation_candidate_unavailable", dispatchErr.Detail.Code)
}

func TestResponsesTierBoundModelQuota(t *testing.T) {
	first := stickyTestCandidate(1, 0)
	first.APIFormat = llm.APIFormatOpenAIResponse.String()
	first.Channel.Settings = &objects.ChannelSettings{ResponsesServiceTierOverride: objects.ResponsesServiceTierSupported, ResponsesTierSupport: &objects.ResponsesTierSupport{Fast: true}}
	first.Models = []biz.ChannelModelEntry{{RequestModel: "public", ActualModel: "claude-sonnet-5-5"}}
	second := *first
	second.Priority = 5
	second.Models = []biz.ChannelModelEntry{{RequestModel: "public", ActualModel: "gemini-3.8-flash"}}
	status := &biz.QuotaChannelStatus{Status: "available", Limits: []provider_quota.QuotaLimitStatus{
		{Type: provider_quota.QuotaLimitTypeToken, Status: "exhausted", UsageRatio: 1, AvailabilityGroup: "antigravity_pool:claude_gpt"},
		{Type: provider_quota.QuotaLimitTypeToken, Status: "available", UsageRatio: 0.1, AvailabilityGroup: "antigravity_pool:gemini"},
	}}
	selector, _ := newQuotaRoutingSelector([]*ChannelModelsCandidate{first, &second}, map[int]*biz.QuotaChannelStatus{1: status}, biz.QuotaRoutingSettings{DefaultMode: objects.QuotaRoutingModeRemoveOnExhausted})
	selector.previousChannelProvider = &continuationRouteFixture{found: true, route: biz.ResponsesContinuationRoute{ChannelID: 1}}
	selected, err := selector.Select(t.Context(), &llm.Request{Model: "public", APIFormat: llm.APIFormatOpenAIResponse, PreviousResponseID: lo.ToPtr("parent"), ServiceTier: lo.ToPtr("priority")})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, "gemini-3.8-flash", selected[0].Models[0].ActualModel)
	require.True(t, selected[0].continuationStrict)
}

func (e *serviceTierCaptureExecutor) Do(_ context.Context, req *httpclient.Request) (*httpclient.Response, error) {
	copy := *req
	copy.Body = slices.Clone(req.Body)
	e.requests = append(e.requests, &copy)
	if !strings.Contains(req.URL, "backup.example") {
		return nil, &httpclient.Error{StatusCode: 502, Body: []byte(`{"error":{"message":"fixture unavailable"}}`)}
	}
	return &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp_tier","object":"response","status":"completed","model":"gpt-4","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)}, nil
}

func TestResponsesServiceTierOverrideProductionPipeline(t *testing.T) {
	for _, passThrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%v", passThrough), func(t *testing.T) {
			executor := &serviceTierCaptureExecutor{}
			ctx, processor, client, preferred := responsesOriginGateway(t, executor)
			preferred.Channel.Settings = &objects.ChannelSettings{
				PassThroughBody:              lo.ToPtr(passThrough),
				ResponsesServiceTierOverride: objects.ResponsesServiceTierStandard,
				BodyOverrideOperations:       []objects.OverrideOperation{{Op: objects.OverrideOpSet, Path: "service_tier", Value: "ultrafast"}},
			}
			backupRow := client.Channel.Create().SetName("tier-backup").SetType("openai_responses").SetBaseURL("https://backup.example/v1").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SetSupportedModels([]string{"gpt-4"}).SetDefaultTestModel("gpt-4").SaveX(ctx)
			backupOut, err := responses.NewOutboundTransformer(backupRow.BaseURL, "fixture")
			require.NoError(t, err)
			backup := *preferred
			backup.Channel = &biz.Channel{Channel: backupRow, Outbound: backupOut}
			backup.Channel.Settings = &objects.ChannelSettings{PassThroughBody: lo.ToPtr(passThrough)}
			processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{preferred, &backup}}
			policy := processor.SystemService.RetryPolicyOrDefault(ctx)
			policy.Enabled, policy.MaxChannelRetries, policy.MaxSingleChannelRetries, policy.RetryDelayMs = true, 1, 0, 0
			policy.LoadBalancerStrategy, policy.TraceStickyMode = biz.LoadBalancerStrategyAdaptive, biz.TraceStickyDisabled
			require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
			require.NoError(t, processor.SystemService.SetStoragePolicy(ctx, &biz.StoragePolicy{StoreRequestBody: true, StoreResponseBody: true}))
			body := []byte(`{"model":"gpt-4","input":"hello","service_tier":"priority"}`)
			_, err = processor.Process(ctx, &httpclient.Request{Method: "POST", Body: body, JSONBody: body})
			require.NoError(t, err)
			require.Len(t, executor.requests, 2)
			require.Equal(t, "default", objects.ServiceTierValue(executor.requests[0].Body))
			require.Equal(t, "priority", objects.ServiceTierValue(executor.requests[1].Body))
			require.Equal(t, "priority", objects.ServiceTierValue(body))
			executions := client.RequestExecution.Query().Order(ent.Asc("id")).AllX(ctx)
			require.Len(t, executions, 2)
			for i, execution := range executions {
				stored, err := processor.RequestService.LoadRequestExecutionRequestBody(ctx, execution)
				require.NoError(t, err)
				require.JSONEq(t, string(executor.requests[i].Body), string(stored))
				require.Equal(t, "priority", execution.CostSnapshot.ServiceTier.Client)
				require.Equal(t, objects.ServiceTierValue(stored), execution.CostSnapshot.ServiceTier.Outbound)
				// The override does not enable tier billing or change its basis.
				require.False(t, execution.CostSnapshot.ServiceTier.Settings.Enabled)
			}
		})
	}
}
