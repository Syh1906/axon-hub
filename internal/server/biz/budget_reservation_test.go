package biz

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestBudgetFinishedAttemptReleasesHoldAndSettlesLateCost(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "35")
	f.configure(t, "group", f.group.ID, "35")
	estimate := budgetAmount(t, "34")
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 1, 1, estimate, nil))
	require.NoError(t, f.service.MarkSent(f.ctx, 1))
	require.NoError(t, f.service.EndAttempt(f.ctx, 1))
	require.NoError(t, f.service.EndAttempt(f.ctx, 1))
	charge := f.db.BudgetCharge.Query().OnlyX(f.ctx)
	require.Equal(t, budgetcharge.StatusUnknown, charge.Status)
	require.Nil(t, charge.Actual, "releasing a hold must not invent zero cost")
	require.NotNil(t, charge.ReservationReleasedAt)
	for _, account := range f.db.BudgetAccount.Query().AllX(f.ctx) {
		require.Zero(t, account.Reserved)
	}
	// A later request is admitted; settling the old request cannot subtract the
	// later request's independent hold, even if callbacks are replayed.
	require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, 2, 2, estimate, nil))
	cost := budgetAmount(t, "2")
	for range 2 {
		_, handled, err := f.service.Settle(f.ctx, 1, &cost, nil)
		require.NoError(t, err)
		require.True(t, handled)
	}
	require.NoError(t, f.service.EndAttempt(f.ctx, 1))
	for _, account := range f.db.BudgetAccount.Query().AllX(f.ctx) {
		require.EqualValues(t, estimate, account.Reserved)
	}
	require.Equal(t, 2, f.db.BudgetPosting.Query().CountX(f.ctx), "one posting per owner")
	require.Equal(t, budgetcharge.StatusSettled, f.db.BudgetCharge.GetX(f.ctx, charge.ID).Status)
}

func TestBudgetAsyncRecoveryUsesProviderOutcome(t *testing.T) {
	for _, status := range []string{"failed", "running", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			var polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				polls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"task","object":"video","status":%q}`, status)
			}))
			t.Cleanup(server.Close)
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			ch := f.db.Channel.Create().SetName("video").SetType("zenmux").SetBaseURL(server.URL).
				SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SaveX(f.ctx)
			req := f.db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetChannelID(ch.ID).
				SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus(request.StatusProcessing).SaveX(f.ctx)
			snapshot := &objects.RequestCostSnapshot{
				ModelPrice:       &objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 3)}},
				PricedAt:         time.Now().UTC(),
				ManualMultiplier: decimal.NewFromInt(1),
				Applied:          objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)},
			}
			execution := f.db.RequestExecution.Create().SetProjectID(req.ProjectID).SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("model").
				SetFormat(llm.APIFormatZenmuxVideo.String()).SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusCompleted).SetCostSnapshot(snapshot).SaveX(f.ctx)
			require.NoError(t, f.service.Reserve(f.ctx, req.ProjectID, f.key.ID, req.ID, execution.ID, budgetAmount(t, "3"), nil))
			require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			require.NoError(t, f.service.RecordAsyncTask(f.ctx, execution, "task"))
			channels := NewChannelServiceForTest(f.db)
			channels.httpClient = httpclient.NewHttpClientWithClient(server.Client())
			service := NewUsageLogService(f.db, f.admin.SystemService, channels)
			video := NewVideoService(channels, nil)
			for range 2 {
				f.db.BudgetCharge.Update().SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(f.ctx)
				require.NoError(t, service.RecoverBudgets(f.ctx, video))
			}
			charge := f.db.BudgetCharge.Query().OnlyX(f.ctx)
			account := f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey)).OnlyX(f.ctx)
			switch status {
			case "succeeded":
				require.Equal(t, budgetcharge.StatusSettled, charge.Status)
				require.EqualValues(t, budgetAmount(t, "3"), *charge.Actual)
				require.Zero(t, account.Reserved)
				require.EqualValues(t, 1, polls.Load())
			case "failed":
				require.Equal(t, budgetcharge.StatusUnknown, charge.Status)
				require.Nil(t, charge.Actual)
				require.Zero(t, account.Reserved)
				require.NotNil(t, charge.ReservationReleasedAt)
				require.Zero(t, f.db.UsageLog.Query().CountX(f.ctx))
				require.EqualValues(t, 1, polls.Load(), "terminal tasks need no repeated provider polling")
			case "running":
				require.Nil(t, charge.Actual)
				require.Nil(t, charge.ReservationReleasedAt)
				require.EqualValues(t, budgetAmount(t, "3"), account.Reserved)
			}
		})
	}
}

func TestBudgetRecoveryRestoresMissingAsyncTaskBeforeReleasingHold(t *testing.T) {
	for _, status := range []string{"failed", "in_progress", "completed"} {
		t.Run(status, func(t *testing.T) {
			var polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				polls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"legacy-task","object":"video","status":%q}`, status)
			}))
			t.Cleanup(server.Close)
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			ch := f.db.Channel.Create().SetName("legacy-video").SetType("openai").SetBaseURL(server.URL).
				SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetCredentials(objects.ChannelCredentials{APIKey: "fixture"}).SaveX(f.ctx)
			req := f.db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetChannelID(ch.ID).
				SetModelID("model").SetExternalID("legacy-task").SetRequestBody([]byte(`{}`)).SetStatus(request.StatusProcessing).SaveX(f.ctx)
			snapshot := &objects.RequestCostSnapshot{
				ModelPrice:       &objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 3)}},
				PricedAt:         time.Now().UTC(),
				ManualMultiplier: decimal.NewFromInt(1),
				Applied:          objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)},
			}
			execution := f.db.RequestExecution.Create().SetProjectID(req.ProjectID).SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("model").
				SetExternalID("legacy-task").SetResponseStatusCode(200).SetFormat(llm.APIFormatOpenAIVideo.String()).
				SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusCompleted).SetCostSnapshot(snapshot).SaveX(f.ctx)
			require.NoError(t, f.service.Reserve(f.ctx, req.ProjectID, f.key.ID, req.ID, execution.ID, budgetAmount(t, "3"), nil))
			require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			var statements []string
			debug := ent.NewClient(ent.Driver(f.db.Driver()), ent.Debug(), ent.Log(func(args ...any) { statements = append(statements, fmt.Sprint(args...)) }))
			channels := NewChannelServiceForTest(debug)
			channels.httpClient = httpclient.NewHttpClientWithClient(server.Client())
			service := NewUsageLogService(debug, f.admin.SystemService, channels)
			requests := newResponsesSessionRequestService(t, debug)
			requests.UsageLogService = service
			video := NewVideoService(channels, requests)
			for range 2 {
				f.db.BudgetCharge.Update().SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(f.ctx)
				require.NoError(t, service.RecoverBudgets(ent.NewContext(f.ctx, debug), video))
			}
			var requestReads int
			for _, statement := range statements {
				if strings.Contains(statement, "SELECT") && (strings.Contains(statement, "FROM `requests`") || strings.Contains(statement, "FROM `request_executions`")) {
					requestReads++
					for _, column := range []string{"request_body", "response_body", "response_chunks", "request_headers"} {
						require.NotContains(t, statement, column, "legacy recovery must not read stored payloads")
					}
				}
			}
			require.Positive(t, requestReads)
			charge := f.db.BudgetCharge.Query().OnlyX(f.ctx)
			account := f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey)).OnlyX(f.ctx)
			if status == "in_progress" {
				require.Nil(t, charge.Actual)
				require.Nil(t, charge.ReservationReleasedAt)
				require.EqualValues(t, budgetAmount(t, "3"), account.Reserved)
			} else {
				require.Zero(t, account.Reserved)
				require.NotNil(t, charge.ReservationReleasedAt)
				require.EqualValues(t, 1, polls.Load())
				if status == "failed" {
					require.Nil(t, charge.Actual)
					require.NotNil(t, charge.AsyncTask)
					require.True(t, charge.AsyncTask.Finished())
					require.Zero(t, f.db.BudgetPosting.Query().CountX(f.ctx))
				} else {
					require.EqualValues(t, budgetAmount(t, "3"), *charge.Actual)
					require.Equal(t, 1, f.db.BudgetPosting.Query().CountX(f.ctx))
				}
			}
		})
	}
}

func TestBudgetLateEvidenceUpdatesUsageAndActualMultiplierStatistics(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "35")
	f.configure(t, "group", f.group.ID, "35")
	ch := f.db.Channel.Create().SetName("late-evidence").SetType("openai").SetBaseURL("https://example.test").
		SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetCredentials(objects.ChannelCredentials{}).SaveX(f.ctx)
	req := f.db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetChannelID(ch.ID).
		SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus(request.StatusCompleted).SaveX(f.ctx)
	execution := f.db.RequestExecution.Create().SetProjectID(req.ProjectID).SetRequestID(req.ID).SetChannelID(ch.ID).
		SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.StatusCompleted).SaveX(f.ctx)
	snapshot := &objects.RequestCostSnapshot{PricedAt: time.Now().UTC(), ManualMultiplier: decimal.NewFromInt(1),
		Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.25")}}
	execution.CostSnapshot = snapshot
	require.NoError(t, f.service.Reserve(f.ctx, req.ProjectID, f.key.ID, req.ID, execution.ID, budgetAmount(t, "3"), nil))
	require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
	service := NewUsageLogService(f.db, f.admin.SystemService, NewChannelServiceForTest(f.db))
	first, err := service.CreateUsageLogFromRequest(f.ctx, req, execution, &llm.Usage{PromptTokens: 1000000, TotalTokens: 1000000})
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Nil(t, first.TotalCost)
	require.NoError(t, f.service.EndAttempt(f.ctx, execution.ID))

	// Recovery receives complete evidence after the initial unpriced log.
	snapshot.ModelPrice = &objects.ModelPrice{Items: []objects.ModelPriceItem{
		{ItemCode: objects.PriceItemCodeUsage, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(2))}},
		{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(10))}},
	}}
	usage := &llm.Usage{PromptTokens: 1000000, CompletionTokens: 100000, TotalTokens: 1100000}
	actual := budgetAmount(t, "0.75")
	require.NoError(t, f.service.RecordUsage(f.ctx, execution.ID, usage, snapshot, &actual))
	f.db.BudgetCharge.Update().Where(budgetcharge.ExecutionIDEQ(execution.ID)).SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(f.ctx)
	require.NoError(t, service.RecoverBudgets(f.ctx, nil))
	for range 2 {
		row, err := service.CreateUsageLogFromRequest(f.ctx, req, execution, usage)
		require.NoError(t, err)
		require.Equal(t, first.ID, row.ID)
		require.NotNil(t, row.TotalCost)
		require.Equal(t, 0.75, *row.TotalCost)
		require.EqualValues(t, 1100000, row.TotalTokens)
		require.Len(t, row.CostItems, 2)
	}
	require.Equal(t, 1, f.db.UsageLog.Query().CountX(f.ctx))
	require.Equal(t, 2, f.db.BudgetPosting.Query().CountX(f.ctx))
	for _, posting := range f.db.BudgetPosting.Query().AllX(f.ctx) {
		require.EqualValues(t, actual, posting.Amount)
	}
	models, err := queryChannelTodayUsageModels(f.ctx, f.db, ch.ID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Zero(t, models[0].UnpricedCount)
	require.InDelta(t, 0.75/1.1, *models[0].AvgCostPerMillionTokens, 1e-9, "use the final 0.25 multiplier, not standard cost 3")
}

func TestBudgetRecoveryRequiresSuccessForFeeWithoutUsage(t *testing.T) {
	for _, status := range []request.Status{request.StatusFailed, request.StatusCanceled, request.StatusProcessing, request.StatusCompleted} {
		t.Run(string(status), func(t *testing.T) {
			f := newBudgetFixture(t)
			f.configure(t, "key", f.key.ID, "35")
			req := f.db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetModelID("model").SetRequestBody([]byte(`{}`)).SetStatus(status).SaveX(f.ctx)
			snapshot := &objects.RequestCostSnapshot{
				ModelPrice:       &objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 3)}},
				PricedAt:         time.Now().UTC(),
				ManualMultiplier: decimal.NewFromInt(1),
				Applied:          objects.AppliedCostMultiplier{Value: decimal.NewFromInt(1)},
			}
			execution := f.db.RequestExecution.Create().SetProjectID(req.ProjectID).SetRequestID(req.ID).SetModelID("model").
				SetRequestBody([]byte(`{}`)).SetStatus(requestexecution.Status(status)).SetCostSnapshot(snapshot).SaveX(f.ctx)
			require.NoError(t, f.service.Reserve(f.ctx, req.ProjectID, f.key.ID, req.ID, execution.ID, budgetAmount(t, "3"), nil))
			f.db.BudgetCharge.Update().SetCostSnapshot(snapshot).ExecX(f.ctx)
			require.NoError(t, f.service.MarkSent(f.ctx, execution.ID))
			f.db.BudgetCharge.Update().SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(f.ctx)
			service := NewUsageLogService(f.db, f.admin.SystemService, NewChannelServiceForTest(f.db))
			require.NoError(t, service.RecoverBudgets(f.ctx, nil))
			charge := f.db.BudgetCharge.Query().OnlyX(f.ctx)
			if status == request.StatusCompleted {
				require.Equal(t, budgetcharge.StatusSettled, charge.Status)
				require.EqualValues(t, budgetAmount(t, "3"), *charge.Actual)
			} else {
				require.Nil(t, charge.Actual, "a price alone is not evidence of a successful billable request")
				require.Zero(t, f.db.UsageLog.Query().CountX(f.ctx))
				require.Zero(t, f.db.BudgetPosting.Query().CountX(f.ctx))
			}
		})
	}
}

func TestBudgetRecoveryReleasesLegacyFinishedHoldAndPreservesActiveTasks(t *testing.T) {
	f := newBudgetFixture(t)
	f.configure(t, "key", f.key.ID, "35")
	create := func(status request.Status, executionStatus requestexecution.Status, format llm.APIFormat, estimate string) *ent.RequestExecution {
		req := f.db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetModelID("fixture").SetRequestBody([]byte(`{}`)).SetStatus(status).SaveX(f.ctx)
		exec := f.db.RequestExecution.Create().SetProjectID(f.key.ProjectID).SetRequestID(req.ID).SetModelID("fixture").SetFormat(format.String()).SetRequestBody([]byte(`{}`)).SetStatus(executionStatus).SaveX(f.ctx)
		require.NoError(t, f.service.Reserve(f.ctx, f.key.ProjectID, f.key.ID, req.ID, exec.ID, budgetAmount(t, estimate), nil))
		require.NoError(t, f.service.MarkSent(f.ctx, exec.ID))
		f.db.BudgetCharge.Update().Where(budgetcharge.ExecutionIDEQ(exec.ID)).SetStatus(budgetcharge.StatusUnknown).ClearOwnerToken().ClearOwnerExpiresAt().SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(f.ctx)
		return exec
	}
	failed := create(request.StatusFailed, requestexecution.StatusFailed, llm.APIFormatOpenAIChatCompletion, "30")
	create(request.StatusProcessing, requestexecution.StatusProcessing, llm.APIFormatOpenAIChatCompletion, "3")
	video := create(request.StatusProcessing, requestexecution.StatusCompleted, llm.APIFormatOpenAIVideo, "1")
	require.NoError(t, f.service.RecordAsyncTask(f.ctx, video, "accepted-video"))
	require.Equal(t, 3, f.db.BudgetCharge.Query().Where(budgetcharge.CheckAfterLTE(time.Now().UTC())).CountX(f.ctx))
	usage := NewUsageLogService(f.db, f.admin.SystemService, NewChannelServiceForTest(f.db))
	require.NoError(t, usage.RecoverBudgets(f.ctx, nil))
	require.EqualValues(t, budgetAmount(t, "4"), f.db.BudgetAccount.Query().Where(budgetaccount.OwnerKindEQ(budgetaccount.OwnerKindKey)).OnlyX(f.ctx).Reserved)
	charge := f.db.BudgetCharge.Query().Where(budgetcharge.ExecutionIDEQ(failed.ID)).OnlyX(f.ctx)
	require.NotNil(t, charge.ReservationReleasedAt)
	require.Equal(t, budgetcharge.StatusUnknown, charge.Status)
	require.Nil(t, charge.Actual)
	// Replaying recovery cannot subtract another active task's reservation.
	nextCheck := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	f.db.BudgetCharge.UpdateOneID(charge.ID).SetCheckAfter(nextCheck).ExecX(f.ctx)
	require.NoError(t, usage.RecoverBudgets(f.ctx, nil))
	require.True(t, f.db.BudgetCharge.GetX(f.ctx, charge.ID).CheckAfter.Equal(nextCheck), "no-evidence records must not occupy the recovery queue repeatedly")
	for _, account := range f.db.BudgetAccount.Query().AllX(f.ctx) {
		if account.OwnerID == f.key.ID && string(account.OwnerKind) == "key" {
			require.EqualValues(t, budgetAmount(t, "4"), account.Reserved)
		}
	}
}
