package orchestrator

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

func TestTitleHedgeBudgetAdmissionAndSettlement(t *testing.T) {
	for _, limit := range []int64{60000000, 200000000} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			executor := &titleTestExecutor{delays: map[string]time.Duration{"mapped-primary": 150 * time.Millisecond, "entirely-different-model": time.Millisecond}}
			ctx, processor, db, first, backup := titleGatewayWithBackup(t, executor)
			projectID, _ := contexts.GetProjectID(ctx)
			key := db.APIKey.Create().SetProjectID(projectID).SetUserID(1).SetName("hedge-budget").SetKey("fixture-hedge-budget").SaveX(ctx)
			ctx = contexts.WithAPIKey(ctx, key)
			fee := decimal.RequireFromString("0.6")
			price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeFlatFee, FlatFee: &fee}}}}
			for _, candidate := range []*ChannelModelsCandidate{first, backup} {
				db.ChannelModelPrice.Create().SetChannelID(candidate.Channel.ID).SetModelID(candidate.Models[0].ActualModel).SetPrice(price).SetReferenceID(fmt.Sprintf("title-price-%d", candidate.Channel.ID)).SaveX(ctx)
				processor.ChannelService.PreloadModelPricesForTest(ctx, candidate.Channel)
			}
			account := budget.NewState()
			account.Policy.StartedAt = time.Now().Add(-time.Second)
			account.Policy.Rules = []budget.Rule{{Period: budget.Total, Mode: budget.Fixed, Limit: budget.Amount(limit)}}
			db.BudgetAccount.Create().SetProjectID(projectID).SetOwnerKind(budgetaccount.OwnerKindKey).SetOwnerID(key.ID).SetState(account).SaveX(ctx)
			result, err := processor.processWithTitleHedgeDelay(ctx, &httpclient.Request{Method: "POST", Headers: http.Header{"X-Codex-Turn-Metadata": {`{"thread_source":"thread_title"}`}}, Body: []byte(`{"model":"gpt-6-luna","stream":true,"input":"title"}`)}, 25*time.Millisecond)
			require.NoError(t, err)
			for result.ChatCompletionStream.Next() {
				result.ChatCompletionStream.Current()
			}
			require.NoError(t, result.ChatCompletionStream.Err())
			result.ChatCompletionStream.(streams.DeliveryAcknowledger).AcknowledgeDelivery()
			require.NoError(t, result.ChatCompletionStream.Close())
			require.Eventually(t, func() bool { return db.BudgetAccount.Query().OnlyX(ctx).Reserved == 0 }, time.Second, time.Millisecond)
			charges := db.BudgetCharge.Query().AllX(ctx)
			if limit == 60000000 {
				require.Equal(t, first.Channel.ID, result.Route.ChannelID, "optional admission denial must not cancel the admitted primary")
				require.Len(t, charges, 1)
				executor.mu.Lock()
				require.Len(t, executor.models, 1)
				executor.mu.Unlock()
			} else {
				require.Equal(t, backup.Channel.ID, result.Route.ChannelID)
				require.Len(t, charges, 2)
				require.NotEqual(t, charges[0].ExecutionID, charges[1].ExecutionID)
			}
			settled := 0
			for _, charge := range charges {
				require.Equal(t, result.RequestID, charge.RequestID)
				if charge.Status == budgetcharge.StatusSettled {
					settled++
					require.EqualValues(t, 60000000, *charge.Actual)
				}
			}
			require.Equal(t, 1, settled)
			require.Equal(t, 1, db.UsageLog.Query().CountX(ctx))
			require.Equal(t, 1, db.BudgetPosting.Query().CountX(ctx))
		})
	}
}

func TestTitleHedgeUncertainOutcomeStopsNewDispatch(t *testing.T) {
	executor := &titleTestExecutor{
		delays:       map[string]time.Duration{"mapped-primary": 70 * time.Millisecond, "entirely-different-model": 100 * time.Millisecond},
		streamErrors: map[string]error{"mapped-primary": httpclient.ErrRequestMayHaveExecuted, "entirely-different-model": healthGateServerError()},
	}
	ctx, processor, db, first, backup := titleGatewayWithBackup(t, executor)
	// A later configured mapping would run after backup failure without the
	// shared dispatch guard. Its zero delay would make the unsafe replay succeed.
	third := *backup
	third.Priority = 2
	third.Models = append(backup.Models[:0:0], backup.Models...)
	third.Models[0].ActualModel = "never-dispatch"
	processor.channelSelector = &staticChannelSelector{candidates: []*ChannelModelsCandidate{first, backup, &third}}
	policy := processor.SystemService.RetryPolicyOrDefault(ctx)
	policy.MaxChannelRetries = 2
	require.NoError(t, processor.SystemService.SetRetryPolicy(ctx, policy))
	_, err := processor.processWithTitleHedgeDelay(ctx, &httpclient.Request{Method: "POST", Headers: http.Header{"X-Codex-Turn-Metadata": {`{"thread_source":"thread_title"}`}}, Body: []byte(`{"model":"gpt-6-luna","stream":true,"input":"title"}`)}, 25*time.Millisecond)
	require.ErrorIs(t, err, httpclient.ErrRequestMayHaveExecuted)
	require.Eventually(t, func() bool { executor.mu.Lock(); defer executor.mu.Unlock(); return executor.closed == 2 }, time.Second, time.Millisecond)
	executor.mu.Lock()
	require.Equal(t, []string{"mapped-primary", "entirely-different-model"}, executor.models)
	executor.mu.Unlock()
	require.Equal(t, 2, db.RequestExecution.Query().CountX(ctx))
	cfg := biz.ResolveHealthGateConfig(policy.HealthGateOrDefault(), backup.Channel)
	require.Eventually(t, func() bool {
		snapshots := healthGateSnapshot(t, processor.ChannelService.HealthGate(), backup.Channel.ID, healthGateResolver(cfg))
		return len(snapshots) == 1 && snapshots[0].ConsecutiveFailures == 1
	}, time.Second, time.Millisecond, "a shared stop must preserve the backup's actual HTTP failure")
}
