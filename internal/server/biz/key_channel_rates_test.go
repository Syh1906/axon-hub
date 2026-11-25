package biz

import (
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestKeyChannelRateAccounting(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	db := f.client
	ctx := authz.WithTestBypass(ent.NewContext(f.ctx, db))
	owner := db.User.Create().SetEmail("rates@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(ctx)
	admin := contexts.WithProjectID(contexts.WithUser(ctx, owner), f.project.ID)
	group := db.APIKeyGroup.Create().SetName("partner").SetProjectID(f.project.ID).SetShowChannelInSelfUsage(true).SaveX(ctx)
	ch := db.Channel.Create().SetName("main").SetType("openai").SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"demo"}).SetDefaultTestModel("demo").SaveX(ctx)
	service := &APIKeyService{AbstractService: &AbstractService{db: db}}
	rates := func(keyID, groupID *int, value string) {
		initial, err := service.KeyChannelRates(admin, keyID, groupID)
		require.NoError(t, err)
		rules := []*objects.KeyChannelRate{{ChannelID: ch.ID, Multiplier: decimal.RequireFromString(value)}}
		require.NoError(t, service.SaveKeyChannelRates(admin, keyID, groupID, initial.Revision, rules, nil))
		require.Error(t, service.SaveKeyChannelRates(admin, keyID, groupID, initial.Revision, rules, nil))
	}
	rates(nil, &group.ID, "0.80")
	serviceLog := &UsageLogService{AbstractService: &AbstractService{db: db}}
	now := time.Now().UTC()
	for i, key := range []*ent.APIKey{f.key, f.other} {
		key.Update().SetGroupRef(group.ID).ExecX(ctx)
		expected := 8.0
		if i == 1 {
			rates(&key.ID, nil, "0.60")
			expected = 6.0
		}
		req := f.request(now, key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "demo")
		b, err := executionBilling(ctx, db, req, ch.ID)
		require.NoError(t, err)
		snap := &objects.RequestCostSnapshot{ModelPrice: &objects.ModelPrice{Items: []objects.ModelPriceItem{createPriceItem(objects.PricingModeFlatFee, 10)}}, PricedAt: now, Billing: b, ManualMultiplier: decimal.RequireFromString("0.65"), Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("0.45"), Source: objects.CostMultiplierEstimated}}
		exec := db.RequestExecution.Create().SetProjectID(f.project.ID).SetRequestID(req.ID).SetChannelID(ch.ID).SetModelID("demo").SetRequestBody([]byte("{}")).SetCostSnapshot(snap).SetStatus("completed").SaveX(ctx)
		req.Update().SetResultExecutionID(exec.ID).SetChannelID(ch.ID).ExecX(ctx)
		budgetService := NewBudgetService(db)
		estimate, err := budgetService.Estimate(ctx, exec, &llm.Request{MaxTokens: lo.ToPtr(int64(5))}, nil, 100)
		require.NoError(t, err)
		require.Equal(t, expected, estimate.Decimal().InexactFloat64())
		held, err := budgetService.ReserveAttempt(ctx, req.ProjectID, key.ID, req.ID, exec.ID, estimate, nil, snap)
		require.NoError(t, err)
		require.False(t, held)
		require.Nil(t, db.Request.GetX(ctx, req.ID).BillingGroupRef)
		require.NoError(t, budgetService.UpdateDispatch(ctx, req, exec, held, true))
		require.Equal(t, group.ID, *db.Request.GetX(ctx, req.ID).BillingGroupRef)
		if i == 1 {
			key.Update().ClearGroupRef().ExecX(ctx)
			retry, err := executionBilling(ctx, db, req, ch.ID)
			require.NoError(t, err)
			require.Equal(t, group.ID, *retry.GroupID)
			key.Update().SetGroupRef(group.ID).ExecX(ctx)
		}
		usage := &llm.Usage{PromptTokens: 100, TotalTokens: 100}
		serviceLog.InjectUsageCost(ctx, ch.ID, "demo", usage, snap)
		require.Equal(t, expected, *usage.Cost)
		actual, err := serviceLog.actualBudgetCost(ctx, usage, snap)
		require.NoError(t, err)
		require.Equal(t, expected, actual.Decimal().InexactFloat64())
		log, err := serviceLog.CreateUsageLog(ctx, CreateUsageLogParams{Source: "api", Format: "openai/chat_completions", ProjectID: f.project.ID, APIKeyID: &key.ID, RequestID: req.ID, ChannelID: ch.ID, ActualModelID: "demo", Usage: usage, CostSnapshot: snap, SettledCost: actual})
		require.NoError(t, err)
		require.Equal(t, 6.5, *log.TotalCost)
		require.Equal(t, expected, *log.BilledCost)
		page, err := f.service.Requests(contexts.WithAPIKey(f.ctx, key), now.Format(time.DateOnly), now.Format(time.DateOnly), nil, "", 1, 20)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, expected, *page.Items[0].Cost)
		require.Equal(t, expected/10, *page.Items[0].Channel.Multiplier)
	}
	report := func() {
		result, err := service.GroupCostComparison(admin, f.service.system, group.ID, now.Format(time.DateOnly), now.Format(time.DateOnly))
		require.NoError(t, err)
		entries, err := service.GroupCostEntries(admin, f.service.system, group.ID, f.other.ID, ch.ID, now.Format(time.DateOnly), now.Format(time.DateOnly), nil)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, 6.0, *entries[0].BilledCost)
		require.Len(t, entries[0].Items, 1)
		item := entries[0].Items[0]
		require.Equal(t, "0.65", item.ChannelRate.String())
		require.Equal(t, "MANUAL", item.ChannelSource)
		require.Equal(t, "6.5", item.ChannelAmount.String())
		require.Equal(t, "6", item.BilledAmount.String())
		require.Equal(t, "key", entries[0].Source)
		require.Equal(t, "0.6", entries[0].Multiplier.String())
		require.Zero(t, result.Total.Estimated)
		require.Len(t, result.Keys, 2)
		require.Equal(t, 2, result.Total.Requests)
		require.Equal(t, 13.0, lo.FromPtr(result.Total.ChannelCost))
		require.Equal(t, 14.0, lo.FromPtr(result.Total.BilledCost))
	}
	// Explicit free pricing is distinct from removing the override.
	rates(&f.other.ID, nil, "0")
	next := f.request(now, f.other.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "demo")
	free, err := executionBilling(ctx, db, next, ch.ID)
	require.NoError(t, err)
	require.True(t, free.Rate.IsZero())
	freeQuote := &objects.RequestCostSnapshot{ModelPrice: &objects.ModelPrice{Items: []objects.ModelPriceItem{{ItemCode: objects.PriceItemCodeCompletion, Pricing: objects.Pricing{Mode: objects.PricingModeUsagePerUnit, UsagePerUnit: lo.ToPtr(decimal.NewFromInt(10))}}}}, Billing: free}
	amount, err := NewBudgetService(db).Estimate(ctx, &ent.RequestExecution{CostSnapshot: freeQuote}, &llm.Request{}, nil, 100)
	require.NoError(t, err)
	require.Zero(t, int64(amount))
	current, err := service.KeyChannelRates(admin, &f.other.ID, nil)
	require.NoError(t, err)
	require.NoError(t, service.SaveKeyChannelRates(admin, &f.other.ID, nil, current.Revision, nil, nil))
	inherited, err := executionBilling(ctx, db, next, ch.ID)
	require.NoError(t, err)
	require.Equal(t, "0.8", inherited.Rate.String())
	report()
	_, err = service.GroupCostComparison(f.ctx, f.service.system, group.ID, now.Format(time.DateOnly), now.Format(time.DateOnly))
	require.Error(t, err)
	logs := db.UsageLog.Query().AllX(ctx)
	require.NoError(t, ArchiveUsageStatistics(ctx, db, logs))
	report()
	db.UsageLog.Delete().ExecX(ctx)
	report()
}

func TestGroupCostReportUsesHistoricalRequestDay(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(f.ctx)
	today := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	tomorrow := today.AddDate(0, 0, 1)
	req := f.request(tomorrow, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "demo")
	group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("group").SaveX(ctx)
	ch := rateTestChannel(ctx, f.client, "test", "enabled")
	bill := f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(ch.ID).
		SetModelID("demo").SetCreatedAt(today).SetTotalCost(1).SetBilledCost(2).SetBillingGroupID(group.ID).SaveX(ctx)
	report := func(requests int, billed float64) {
		rows, err := groupCostReport(ctx, f.client, f.project.ID, group.ID, today, tomorrow)
		require.NoError(t, err)
		require.Equal(t, requests, rows.Total.Requests)
		require.Equal(t, billed, *rows.Total.BilledCost)
	}
	report(0, 0)
	// 归档请求的日期优先于尚未清除的原请求；只有用量时使用账单自己的日期。
	parent := f.client.UsageArchive.Create().SetKind("request").SetSourceID(req.ID).SetRequestID(req.ID).SetProjectID(f.project.ID).
		SetModelID("demo").SetOccurredAt(today).SaveX(ctx)
	require.NoError(t, ArchiveUsageStatistics(ctx, f.client, []*ent.UsageLog{bill}))
	report(1, 2)
	f.client.UsageLog.DeleteOne(bill).ExecX(ctx)
	report(1, 2)
	f.client.UsageArchive.DeleteOne(parent).ExecX(ctx)
	report(0, 0)
	f.client.Request.DeleteOne(req).ExecX(ctx)
	report(1, 2)
}

func TestGlobalChannelRatePriority(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	db := f.client
	ctx := authz.WithTestBypass(ent.NewContext(f.ctx, db))
	owner := db.User.Create().SetEmail("global-rate@example.test").SetPassword("fixture").SetIsOwner(true).SaveX(ctx)
	admin := contexts.WithProjectID(contexts.WithUser(ctx, owner), f.project.ID)
	group := db.APIKeyGroup.Create().SetName("partner").SetProjectID(f.project.ID).SaveX(ctx)
	f.key.Update().SetGroupRef(group.ID).ExecX(ctx)
	channel := rateTestChannel(ctx, db, "test", "enabled")
	req := f.request(time.Now(), f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "test")
	svc := &APIKeyService{AbstractService: &AbstractService{db: db}}
	routing := &RequestService{AbstractService: svc.AbstractService}
	save := func(keyID, groupID *int, value string, global *objects.KeyGlobalRateInput) {
		initial, err := svc.KeyChannelRates(admin, keyID, groupID)
		require.NoError(t, err)
		var rules []*objects.KeyChannelRate
		if value != "" {
			rules = []*objects.KeyChannelRate{{ChannelID: channel.ID, Multiplier: decimal.RequireFromString(value)}}
		}
		require.NoError(t, svc.SaveKeyChannelRates(admin, keyID, groupID, initial.Revision, rules, global))
	}
	check := func(want, source string) {
		billing, err := executionBilling(ctx, db, req, channel.ID)
		require.NoError(t, err)
		require.Equal(t, source, billing.Source)
		rates, err := routing.CallerRatesForRouting(ctx, f.key, []int{channel.ID})
		require.NoError(t, err)
		require.Equal(t, want, rates[channel.ID])
		if want == "" {
			require.Nil(t, billing.Rate)
		} else {
			require.Equal(t, want, billing.Rate.String())
		}
	}
	global := func(v string) *objects.KeyGlobalRateInput {
		return &objects.KeyGlobalRateInput{Multiplier: lo.ToPtr(decimal.RequireFromString(v))}
	}
	save(nil, &group.ID, "", global("0.8"))
	check("0.8", "group")
	save(nil, &group.ID, "0.7", nil)
	check("0.7", "group")
	save(&f.key.ID, nil, "", global("0"))
	check("0", "key")
	save(&f.key.ID, nil, "1.1", nil)
	check("1.1", "key")
	save(&f.key.ID, nil, "", &objects.KeyGlobalRateInput{})
	check("0.7", "group")
	save(nil, &group.ID, "", &objects.KeyGlobalRateInput{})
	check("", "")
}

func TestBillingDispatchOwnsGroup(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-budget", true: "with-budget"}[active], func(t *testing.T) {
			f := newBudgetFixture(t)
			if active {
				f.configure(t, "key", f.key.ID, "50")
			}
			db, ctx := f.db, f.ctx
			next := db.APIKeyGroup.Create().SetProjectID(f.key.ProjectID).SetName("next").SaveX(ctx)
			req := db.Request.Create().SetProjectID(f.key.ProjectID).SetAPIKeyID(f.key.ID).SetModelID("demo").SetRequestBody([]byte("{}")).SetStatus("processing").SaveX(ctx)
			attempt := func(group int) *ent.RequestExecution {
				b, err := executionBilling(ctx, db, req, 1)
				require.NoError(t, err)
				require.Equal(t, group, lo.FromPtr(b.GroupID))
				snap := &objects.RequestCostSnapshot{Billing: b}
				e := db.RequestExecution.Create().SetProjectID(req.ProjectID).SetRequestID(req.ID).SetModelID("demo").SetRequestBody([]byte("{}")).SetStatus("processing").SetCostSnapshot(snap).SaveX(ctx)
				held, err := f.service.ReserveAttempt(ctx, req.ProjectID, f.key.ID, req.ID, e.ID, budgetAmount(t, "1"), nil, snap)
				require.NoError(t, err)
				require.Equal(t, active, held)
				return e
			}
			first := attempt(f.group.ID)
			f.key.Update().SetGroupRef(next.ID).ExecX(ctx)
			require.ErrorIs(t, f.service.UpdateDispatch(ctx, req, first, active, true), ErrBillingGroupChanged)
			require.NoError(t, f.service.EndAttempt(ctx, first.ID))
			second, third := attempt(next.ID), attempt(next.ID)
			require.NoError(t, f.service.UpdateDispatch(ctx, req, second, active, true))
			require.NoError(t, f.service.UpdateDispatch(ctx, req, third, active, true))
			require.NoError(t, f.service.UpdateDispatch(ctx, req, second, active, false))
			require.Equal(t, next.ID, *db.Request.GetX(ctx, req.ID).BillingGroupRef)
			require.NoError(t, f.service.UpdateDispatch(ctx, req, third, active, false))
			require.Nil(t, db.Request.GetX(ctx, req.ID).BillingGroupRef)
			f.key.Update().SetGroupRef(f.group.ID).ExecX(ctx)
			sent := attempt(f.group.ID)
			require.NoError(t, f.service.UpdateDispatch(ctx, req, sent, active, true))
			f.key.Update().SetGroupRef(next.ID).ExecX(ctx)
			db.APIKeyGroup.DeleteOneID(f.group.ID).ExecX(ctx)
			attempt(f.group.ID)
		})
	}
}
