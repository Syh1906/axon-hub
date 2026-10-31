package selfusage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
	"github.com/looplj/axonhub/internal/ent/budgetcharge"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/budget"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestServiceTierItemsRecoveryAndSelfUsageGraphQL(t *testing.T) {
	db := enttest.NewEntClient(t, "sqlite3", "file:item_recovery?mode=memory&_fk=0")
	defer db.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), db))
	project := db.Project.Create().SetName("item-test").SaveX(ctx)
	key := db.APIKey.Create().SetProjectID(project.ID).SetName("item-test").SetKey("synthetic-item-test").SetStatus("enabled").SaveX(ctx)
	channel := db.Channel.Create().SetName("item-test").SetType("openai").SetBaseURL("http://127.0.0.1").SetSupportedModels([]string{"test"}).SetDefaultTestModel("test").SetCredentials(objects.ChannelCredentials{APIKey: "synthetic"}).SaveX(ctx)
	system := biz.NewSystemService(biz.SystemServiceParams{Ent: db})
	require.NoError(t, system.SetGeneralSettings(ctx, biz.SystemGeneralSettings{Timezone: "UTC", CurrencyCode: "USD"}))
	channels := biz.NewChannelServiceForTest(db)
	defer channels.Stop()
	usageService := biz.NewUsageLogService(db, system, channels)
	account, err := biz.LockBudgetAccount(ctx, db, project.ID, budgetaccount.OwnerKindKey, key.ID)
	require.NoError(t, err)
	state := account.State
	state.Policy.StartedAt = time.Now().Add(-time.Hour)
	account.Update().SetState(state).ExecX(ctx)
	price := objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.Zero)}}}, ServiceTiers: &objects.ServiceTierPrices{Fast: &objects.ServiceTierPrice{Source: objects.ServiceTierPriceCustom, Items: []objects.ServiceTierItemRule{{ItemCode: objects.PriceItemCodeCompletion, Method: objects.ServiceTierItemUnitPrice, Value: lo.ToPtr(decimal.NewFromInt(25))}}}}}
	model := db.Model.Create().SetModelID("test").SetDeveloper("test").SetName("test").SetIcon("test").SetGroup("test").SetSettings(&objects.ModelSettings{}).SetModelCard(&objects.ModelCard{Price: &price}).SaveX(ctx)
	snapshot := &objects.RequestCostSnapshot{ModelPrice: &price, PricedAt: time.Now(), Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.8")}, ServiceTier: &objects.ServiceTierEvidence{Settings: objects.ServiceTierSettings{Enabled: true, Basis: objects.ServiceTierBasisClient}, Client: "fast"}}
	req := db.Request.Create().SetProjectID(project.ID).SetAPIKeyID(key.ID).SetChannelID(channel.ID).SetModelID("test").SetFormat("openai/responses").SetStatus("completed").SetRequestBody([]byte(`{}`)).SaveX(ctx)
	execution := db.RequestExecution.Create().SetProjectID(project.ID).SetRequestID(req.ID).SetChannelID(channel.ID).SetModelID("test").SetFormat(req.Format).SetRequestBody([]byte(`{}`)).SetStatus("completed").SetCostSnapshot(snapshot).SaveX(ctx)
	estimate, err := budget.ParseAmount("10")
	require.NoError(t, err)
	require.NoError(t, usageService.BudgetService.Reserve(ctx, project.ID, key.ID, req.ID, execution.ID, estimate, nil))
	require.NoError(t, usageService.BudgetService.MarkSent(ctx, execution.ID))
	usage := &llm.Usage{CompletionTokens: 10000, TotalTokens: 10000}
	require.NoError(t, usageService.BudgetService.RecordUsage(ctx, execution.ID, usage, snapshot, nil))
	// Simulate a restart after durable evidence but before settlement, then change current prices.
	price.ServiceTiers.Fast.Items[0].Value = lo.ToPtr(decimal.NewFromInt(999))
	model.Update().SetModelCard(&objects.ModelCard{Price: &price}).ExecX(ctx)
	db.BudgetCharge.Update().SetCheckAfter(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
	restarted := biz.NewUsageLogService(db, system, channels)
	require.NoError(t, restarted.RecoverBudgets(ctx, nil))
	require.NoError(t, restarted.RecoverBudgets(ctx, nil))
	charge := db.BudgetCharge.Query().OnlyX(ctx)
	require.Equal(t, budgetcharge.StatusSettled, charge.Status)
	require.Equal(t, int64(20000000), *charge.Actual)
	require.Equal(t, 1, db.BudgetPosting.Query().CountX(ctx))
	require.Equal(t, int64(20000000), db.BudgetPosting.Query().OnlyX(ctx).Amount)
	require.Zero(t, db.BudgetAccount.GetX(ctx, account.ID).Reserved)
	log := db.UsageLog.Query().OnlyX(ctx)
	require.InDelta(t, 0.2, *log.TotalCost, 1e-10)
	server := handler.NewDefaultServer(NewSchema(biz.NewSelfUsageService(db, system)))
	day := req.CreatedAt.UTC().Format("2006-01-02")
	body, _ := json.Marshal(map[string]string{"query": `{selfUsageRequests(start:"` + day + `",end:"` + day + `"){items{cost costItems{pricingEvidence{itemMethod finalUnitPrice modeSubtotal ruleSource reference{source} serviceTier{mode}}}}}}`})
	request := httptest.NewRequest(http.MethodPost, "/self-service/v1/graphql", strings.NewReader(string(body))).WithContext(contexts.WithAPIKey(context.Background(), key))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var result map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.NotContains(t, result, "errors")
	nodes := result["data"].(map[string]any)["selfUsageRequests"].(map[string]any)["items"].([]any)
	require.Len(t, nodes, 1)
	require.InDelta(t, 0.2, nodes[0].(map[string]any)["cost"], 1e-10)
	require.Contains(t, response.Body.String(), `"mode":"fast"`)
	require.Contains(t, response.Body.String(), `"ruleSource":"mode_item"`)
	require.Contains(t, response.Body.String(), `"itemMethod":"unit_price"`)
	require.Contains(t, response.Body.String(), `"finalUnitPrice":25`)
	require.Contains(t, response.Body.String(), `"modeSubtotal":0.25`)
	require.Contains(t, response.Body.String(), `"source":"custom"`)
}
