package biz

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/channelmodelpriceversion"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
)

type selfUsageFixture struct {
	t            *testing.T
	client       *ent.Client
	service      *SelfUsageService
	ctx          context.Context
	key          *ent.APIKey
	other        *ent.APIKey
	project      *ent.Project
	otherProject *ent.Project
}

func TestSelfUsageKeepsImageOutputsSeparateFromTokenUnits(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	req := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "images")
	f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetModelID("images").SetCostItems([]objects.CostItem{
		{ItemCode: objects.PriceItemCodeUsage, Quantity: 1, Subtotal: decimal.RequireFromString("0.04"), Image: &objects.ImageOutput{Width: 2048, Height: 2048, Tier: "2K", Source: "file"}, ImageRule: "2K"},
		{ItemCode: objects.PriceItemCodeUsage, Quantity: 1, Image: &objects.ImageOutput{Source: "unknown"}, Unpriced: true},
	}).SaveX(authz.WithTestBypass(f.ctx))
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].CostItems, 2)
	require.Equal(t, 2048, page.Items[0].CostItems[0].Image.Width)
	require.Equal(t, "2K", page.Items[0].CostItems[0].ImageRule)
	require.True(t, page.Items[0].CostItems[1].Unpriced)
}

func newSelfUsageFixture(t *testing.T, timezone string, opts ...enttest.Option) *selfUsageFixture {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, fmt.Sprintf("file:self_usage_%d?mode=memory&cache=shared&_fk=0", time.Now().UnixNano()), opts...)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	p := client.Project.Create().SetName("self-usage").SetStatus(project.StatusActive).SaveX(ctx)
	otherProject := client.Project.Create().SetName("other-project").SetStatus(project.StatusActive).SaveX(ctx)
	key := client.APIKey.Create().SetName("owner").SetKey("self-usage-owner").SetProjectID(p.ID).SaveX(ctx)
	other := client.APIKey.Create().SetName("other").SetKey("self-usage-other").SetProjectID(p.ID).SaveX(ctx)
	client.APIKey.Create().SetName("different-project").SetKey("self-usage-different-project").SetProjectID(otherProject.ID).SaveX(ctx)
	system := NewSystemService(SystemServiceParams{Ent: client})
	require.NoError(t, system.SetGeneralSettings(ctx, SystemGeneralSettings{CurrencyCode: "CNY", Timezone: timezone}))
	return &selfUsageFixture{t: t, client: client, service: NewSelfUsageService(client, system), ctx: contexts.WithAPIKey(context.Background(), key), key: key, other: other, project: p, otherProject: otherProject}
}

func (f *selfUsageFixture) request(at time.Time, keyID, projectID int, source request.Source, status request.Status, model string, bodies ...[]byte) *ent.Request {
	f.t.Helper()
	body := []byte(`{}`)
	if len(bodies) != 0 {
		body = bodies[0]
	}
	builder := f.client.Request.Create().SetProjectID(projectID).SetSource(source).SetModelID(model).
		SetStatus(status).SetRequestBody(body).SetCreatedAt(at)
	if keyID != 0 {
		builder.SetAPIKeyID(keyID)
	}
	req, err := builder.Save(authz.WithTestBypass(f.ctx))
	require.NoError(f.t, err)
	return req
}

func (f *selfUsageFixture) usage(req *ent.Request, tokens int64, cost *float64) {
	f.t.Helper()
	// Deliberately assign misleading usage-log ownership, model and time: the
	// request is the source of truth for scope, model and calendar day.
	builder := f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.otherProject.ID).
		SetAPIKeyID(f.other.ID).SetModelID("upstream-model").SetCreatedAt(req.CreatedAt.Add(48 * time.Hour)).
		SetPromptTokens(tokens).SetCompletionTokens(tokens * 2).SetPromptCachedTokens(tokens * 3).
		SetPromptWriteCachedTokens(tokens * 4).SetCompletionReasoningTokens(tokens * 5).SetTotalTokens(tokens * 6)
	if cost != nil {
		builder.SetTotalCost(*cost)
	}
	_, err := builder.Save(authz.WithTestBypass(f.ctx))
	require.NoError(f.t, err)
}

func TestSelfUsageIsolationAndAggregation(t *testing.T) {
	f := newSelfUsageFixture(t, "Asia/Kolkata")
	at := time.Date(2026, 5, 1, 18, 40, 0, 0, time.UTC) // May 2 locally.
	completed := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "public-model")
	f.usage(completed, 2, nil)
	price := 1.25
	f.usage(completed, 3, &price)
	failed := f.request(at.Add(time.Minute), f.key.ID, f.project.ID, request.SourceAPI, request.StatusFailed, "failed-model")
	f.usage(failed, 4, &price)
	canceled := f.request(at.Add(2*time.Minute), f.key.ID, f.project.ID, request.SourceAPI, request.StatusCanceled, "failed-model")
	f.usage(canceled, 1, nil)
	processing := f.request(at.Add(3*time.Minute), f.key.ID, f.project.ID, request.SourceAPI, request.StatusProcessing, "pending-model")
	f.usage(processing, 1, nil)
	f.request(at.Add(4*time.Minute), f.key.ID, f.project.ID, request.SourceAPI, request.StatusPending, "pending-model")
	for _, tc := range []struct {
		keyID, projectID int
		source           request.Source
	}{
		{f.other.ID, f.project.ID, request.SourceAPI},
		{f.key.ID, f.project.ID + 1, request.SourceAPI},
		{0, f.project.ID, request.SourceAPI},
		{f.key.ID, f.project.ID, request.SourcePlayground},
		{f.key.ID, f.project.ID, request.SourceTest},
	} {
		r := f.request(at, tc.keyID, tc.projectID, tc.source, request.StatusCompleted, "private-model")
		f.usage(r, 100, &price)
	}

	stats, err := f.service.Stats(f.ctx, "2026-05-01", "2026-05-03")
	require.NoError(t, err)
	require.Equal(t, 1, stats.Totals.SuccessRequests)
	require.Equal(t, 2, stats.Totals.FailedRequests)
	require.Equal(t, 5, stats.Totals.UsageRecords)
	require.Equal(t, 3, stats.Totals.UnpricedRecords)
	require.Equal(t, 11, stats.Totals.InputTokens)
	require.Equal(t, 22, stats.Totals.OutputTokens)
	require.Equal(t, 33, stats.Totals.CacheReadTokens)
	require.Equal(t, 44, stats.Totals.CacheWriteTokens)
	require.Equal(t, 55, stats.Totals.ReasoningTokens)
	require.Equal(t, 66, stats.Totals.TotalTokens)
	require.NotNil(t, stats.Totals.Cost)
	require.InDelta(t, 2.5, *stats.Totals.Cost, 1e-9)
	require.Len(t, stats.Daily, 3)
	require.Equal(t, "2026-05-02", stats.Daily[1].Date)
	require.Equal(t, 3, stats.Daily[1].Usage.SuccessRequests+stats.Daily[1].Usage.FailedRequests)
	require.Equal(t, 5, stats.Daily[1].Usage.UsageRecords)
	for _, zero := range []int{0, 2} {
		require.Equal(t, 0, stats.Daily[zero].Usage.UsageRecords)
		require.Equal(t, 0.0, *stats.Daily[zero].Usage.Cost)
	}
	require.Len(t, stats.Models, 3)
	require.Equal(t, "pending-model", stats.Models[2].Model)
	require.Equal(t, 0, stats.Models[2].Usage.SuccessRequests+stats.Models[2].Usage.FailedRequests)
	require.Nil(t, stats.Models[2].Usage.Cost)

	page, err := f.service.Requests(f.ctx, "2026-05-02", "2026-05-02", nil, "", 1, 20)
	require.NoError(t, err)
	require.Equal(t, 5, page.Total)
	require.Len(t, page.Items, 5)
	require.Equal(t, "PROCESSING", page.Items[0].Status)
	require.Equal(t, processing.ID, page.Items[1].ID)
	require.Equal(t, completed.ID, page.Items[4].ID)
	require.Equal(t, 2, page.Items[4].UsageRecords)
	require.Equal(t, 1, page.Items[4].UnpricedRecords)
	require.InDelta(t, 1.25, *page.Items[4].Cost, 1e-9)
	for _, item := range page.Items {
		require.NotEqual(t, "private-model", item.Model)
	}
	for _, tc := range []struct {
		status string
		want   int
	}{{"SUCCESS", 1}, {"FAILED", 2}, {"PROCESSING", 2}} {
		filtered, err := f.service.Requests(f.ctx, "2026-05-02", "2026-05-02", nil, tc.status, 1, 20)
		require.NoError(t, err)
		require.Equal(t, tc.want, filtered.Total)
	}
	model := "public-model"
	filtered, err := f.service.Requests(f.ctx, "2026-05-02", "2026-05-02", &model, "", 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, filtered.Total)
}

func TestSelfUsageCostBoundariesAndStablePages(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	unpriced := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "unpriced")
	f.usage(unpriced, 1, nil)
	zero := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusFailed, "zero")
	cost := 0.0
	f.usage(zero, 2, &cost)
	empty := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "empty")
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 2)
	require.NoError(t, err)
	require.Equal(t, 3, page.Total)
	require.Equal(t, empty.ID, page.Items[0].ID) // equal timestamp: ID descending.
	require.Equal(t, 0.0, *page.Items[0].Cost)
	require.Equal(t, zero.ID, page.Items[1].ID)
	require.Equal(t, 0.0, *page.Items[1].Cost)
	second, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 2, 2)
	require.NoError(t, err)
	require.Equal(t, unpriced.ID, second.Items[0].ID)
	require.Nil(t, second.Items[0].Cost)
	stats, err := f.service.Stats(f.ctx, "2026-01-01", "2026-01-01")
	require.NoError(t, err)
	require.Equal(t, 0.0, *stats.Totals.Cost) // known zero remains zero with a missing price.
	require.Equal(t, 1, stats.Totals.UnpricedRecords)
	for _, model := range stats.Models {
		if model.Model == "unpriced" {
			require.Nil(t, model.Usage.Cost)
		}
	}
}

func TestSelfUsageRequestPageSelectsOnlyVisibleColumns(t *testing.T) {
	var queries []string
	capture := false
	f := newSelfUsageFixture(t, "UTC", enttest.WithOptions(ent.Debug(), ent.Log(func(v ...any) {
		if capture {
			queries = append(queries, fmt.Sprint(v...))
		}
	})))
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	largeBody := []byte(`{"input":"` + strings.Repeat("x", 1<<20) + `"}`)
	req := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "large-model", largeBody)
	capture = true
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 20)
	capture = false
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	require.Equal(t, req.ID, page.Items[0].ID)
	require.Equal(t, "large-model", page.Items[0].Model)
	require.Equal(t, "SUCCESS", page.Items[0].Status)

	var projection string
	for _, query := range queries {
		if start := strings.Index(query, "SELECT "); start >= 0 && strings.Contains(query, "FROM `requests`") {
			if end := strings.Index(query[start:], " FROM "); end > 0 {
				selected := query[start+len("SELECT ") : start+end]
				if strings.Contains(selected, "`model_id`") {
					projection = selected
					break
				}
			}
		}
	}
	require.NotEmpty(t, projection, "expected a request page SELECT in SQL debug output: %v", queries)
	for _, heavy := range []string{"request_body", "response_body", "response_chunks", "request_headers", "routing_decision"} {
		require.NotContains(t, projection, "`"+heavy+"`", "request page must not load %s", heavy)
	}
}

func TestSelfUsageCalendarDSTAndValidation(t *testing.T) {
	f := newSelfUsageFixture(t, "America/Los_Angeles")
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	for _, day := range []string{"2026-03-07", "2026-03-08", "2026-03-09"} {
		local, err := time.ParseInLocation("2006-01-02", day, loc)
		require.NoError(t, err)
		f.request(local.Add(30*time.Minute).UTC(), f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, day)
	}
	stats, err := f.service.Stats(f.ctx, "2026-03-07", "2026-03-09")
	require.NoError(t, err)
	require.Len(t, stats.Daily, 3)
	for i, day := range []string{"2026-03-07", "2026-03-08", "2026-03-09"} {
		require.Equal(t, day, stats.Daily[i].Date)
		require.Equal(t, 1, stats.Daily[i].Usage.SuccessRequests)
	}
	meta, err := f.service.Meta(f.ctx)
	require.NoError(t, err)
	require.Equal(t, "CNY", meta.CurrencyCode)
	require.Equal(t, "America/Los_Angeles", meta.Timezone)
	require.Equal(t, f.key.Name, meta.APIKeyName)
	require.Equal(t, 90, meta.MaxRangeDays)
	for _, dates := range [][2]string{{"2026-01-01", "2026-04-01"}, {"2026-02-01", "2026-01-01"}, {"2026-02-30", "2026-03-01"}, {"2026-01-01T00:00", "2026-01-01"}} {
		_, err := f.service.Stats(f.ctx, dates[0], dates[1])
		require.ErrorContains(t, err, "invalid_range")
		_, err = f.service.Requests(f.ctx, dates[0], dates[1], nil, "", 1, 20)
		require.ErrorContains(t, err, "invalid_range")
	}
	_, err = f.service.Stats(f.ctx, "2026-01-01", "2026-03-31") // 90 calendar dates.
	require.NoError(t, err)
	for _, pagination := range [][2]int{{1, 101}, {0, 20}, {1, 0}} {
		_, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", pagination[0], pagination[1])
		require.ErrorContains(t, err, "invalid_page")
	}
}

func TestSelfUsageRequestCostMergeAndMultiplier(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	setupCtx := authz.WithTestBypass(f.ctx)
	ch := f.client.Channel.Create().SetType(channel.TypeOpenai).SetName("self-usage-price").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).SetSupportedModels([]string{"model"}).
		SetDefaultTestModel("model").SaveX(setupCtx)
	price := f.client.ChannelModelPrice.Create().SetChannelID(ch.ID).SetModelID("model").
		SetReferenceID("current").SetPrice(objects.ModelPrice{}).SaveX(setupCtx)
	for _, tc := range []struct {
		ref        string
		multiplier *decimal.Decimal
	}{
		{ref: "double-a", multiplier: lo.ToPtr(decimal.NewFromInt(2))},
		{ref: "double-b", multiplier: lo.ToPtr(decimal.NewFromInt(2))},
		{ref: "triple", multiplier: lo.ToPtr(decimal.NewFromInt(3))},
	} {
		f.client.ChannelModelPriceVersion.Create().SetChannelID(ch.ID).SetChannelModelPriceID(price.ID).
			SetModelID("model").SetReferenceID(tc.ref).SetEffectiveStartAt(time.Now()).
			SetStatus(channelmodelpriceversion.StatusArchived).
			SetPrice(objects.ModelPrice{Multiplier: tc.multiplier}).SaveX(setupCtx)
	}
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	uniform := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "uniform")
	different := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusFailed, "different")
	missing := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCanceled, "missing")
	other := f.request(at, f.other.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "private")
	cost := 1.0
	unreferenced := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "unreferenced")
	tierLimit := int64(100)
	priced := func(req *ent.Request, ref string, items ...objects.CostItem) {
		f.client.UsageLog.Create().SetRequestID(req.ID).SetAPIKeyID(f.other.ID).
			SetProjectID(f.otherProject.ID).SetModelID("upstream").SetTotalCost(cost).
			SetCostPriceReferenceID(ref).SetCostItems(items).SaveX(setupCtx)
	}
	write := func(variant objects.PromptWriteCacheVariantCode, quantity int64, subtotal string) objects.CostItem {
		amount := decimal.RequireFromString(subtotal)
		return objects.CostItem{ItemCode: objects.PriceItemCodeWriteCachedTokens,
			PromptWriteCacheVariantCode: variant, Quantity: quantity, Subtotal: amount,
			TierBreakdown: []objects.TierCost{{UpTo: &tierLimit, Units: quantity, Subtotal: amount}}}
	}
	priced(uniform, "double-a", write(objects.PromptWriteCacheVariantCode5Min, 10, "0.25"),
		objects.CostItem{ItemCode: objects.PriceItemCodeCompletion, Quantity: 4, Subtotal: decimal.RequireFromString("0.4")})
	priced(uniform, "double-b", write(objects.PromptWriteCacheVariantCode5Min, 20, "0.5"),
		write(objects.PromptWriteCacheVariantCode1Hour, 3, "0.3"),
		objects.CostItem{ItemCode: objects.PriceItemCodeUsage, Quantity: 5, Subtotal: decimal.RequireFromString("0.1")})
	// An unpriced record cannot veto a multiplier resolved for all priced records.
	f.client.UsageLog.Create().SetRequestID(uniform.ID).SetAPIKeyID(f.other.ID).
		SetProjectID(f.otherProject.ID).SetModelID("upstream").
		SetCostItems([]objects.CostItem{write("", 7, "0.07")}).SaveX(setupCtx)
	priced(different, "double-a", write("", 1, "0.1"))
	priced(different, "triple", write("", 2, "0.2"))
	priced(missing, "double-a", write("", 1, "0.1"))
	priced(missing, "missing-version", write("", 2, "0.2"))
	f.client.UsageLog.Create().SetRequestID(unreferenced.ID).SetAPIKeyID(f.other.ID).
		SetProjectID(f.otherProject.ID).SetModelID("upstream").SetTotalCost(cost).
		SetCostItems([]objects.CostItem{write("", 1, "0.1")}).SaveX(setupCtx)
	priced(other, "triple", write("", 1000, "100"))
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 20)
	require.NoError(t, err)
	require.Equal(t, 4, page.Total)
	rows := make(map[int]SelfUsageRequest, len(page.Items))
	for _, row := range page.Items {
		rows[row.ID] = row
		require.NotEqual(t, "private", row.Model)
	}
	require.Equal(t, "completed", rows[uniform.ID].RequestStatus)
	require.NotNil(t, rows[uniform.ID].CostMultiplier)
	require.Equal(t, 2.0, *rows[uniform.ID].CostMultiplier)
	require.False(t, rows[uniform.ID].CostMultiplierMixed)
	require.Nil(t, rows[different.ID].CostMultiplier)
	require.True(t, rows[different.ID].CostMultiplierMixed)
	// A missing version counts as 1, which differs from the other record's 2.
	require.Nil(t, rows[missing.ID].CostMultiplier)
	require.True(t, rows[missing.ID].CostMultiplierMixed)
	require.NotNil(t, rows[unreferenced.ID].CostMultiplier)
	require.Equal(t, 1.0, *rows[unreferenced.ID].CostMultiplier)
	require.False(t, rows[unreferenced.ID].CostMultiplierMixed)
	items := rows[uniform.ID].CostItems
	require.Len(t, items, 5)
	require.Equal(t, []string{"prompt_tokens", "completion_tokens", "prompt_write_cached_tokens", "prompt_write_cached_tokens", "prompt_write_cached_tokens"},
		[]string{items[0].ItemCode, items[1].ItemCode, items[2].ItemCode, items[3].ItemCode, items[4].ItemCode})
	require.Equal(t, "five_min", *items[2].PromptWriteCacheVariantCode)
	require.Equal(t, 30, items[2].Quantity)
	require.InDelta(t, 0.75, items[2].Subtotal, 1e-9)
	require.Len(t, items[2].TierBreakdown, 1)
	require.Equal(t, 30, items[2].TierBreakdown[0].Units)
	require.InDelta(t, 0.75, items[2].TierBreakdown[0].Subtotal, 1e-9)
	require.Equal(t, "one_hour", *items[3].PromptWriteCacheVariantCode)
	require.Nil(t, items[4].PromptWriteCacheVariantCode)
	require.Equal(t, 7, items[4].Quantity)
	require.NotContains(t, rows, other.ID)
}

func TestSelfUsagePreservesMixedServiceTierEvidence(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(f.ctx)
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	req := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "mixed-tier")
	for _, mode := range []string{"fast", "ultrafast"} {
		evidence := &objects.CostPricingEvidence{ServiceTier: objects.ServiceTierDecision{Mode: mode}, BaseSubtotal: decimal.NewFromInt(1), BaseTierSubtotals: []decimal.Decimal{decimal.NewFromInt(1)}, ModeMultiplier: lo.ToPtr(decimal.Zero), CostMultiplier: decimal.NewFromInt(1)}
		f.client.UsageLog.Create().SetRequestID(req.ID).SetAPIKeyID(f.key.ID).SetProjectID(f.project.ID).SetModelID("model").SetTotalCost(0).SetCostItems([]objects.CostItem{{ItemCode: objects.PriceItemCodeWriteCachedTokens, PromptWriteCacheVariantCode: objects.PromptWriteCacheVariantCode1Hour, Quantity: 10, PricingEvidence: evidence, TierBreakdown: []objects.TierCost{{Units: 10}}}}).SaveX(ctx)
	}
	page, err := f.service.Requests(f.ctx, "2026-01-01", "2026-01-01", nil, "", 1, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	items := page.Items[0].CostItems
	require.Len(t, items, 2)
	modes := []string{}
	for _, item := range items {
		require.NotNil(t, item.PricingEvidence)
		modes = append(modes, item.PricingEvidence.ServiceTier.Mode)
		require.Equal(t, "one_hour", *item.PromptWriteCacheVariantCode)
		require.Len(t, item.TierBreakdown, 1)
		require.Equal(t, "1", item.PricingEvidence.BaseTierSubtotals[0].String())
	}
	require.ElementsMatch(t, []string{"fast", "ultrafast"}, modes)
}
