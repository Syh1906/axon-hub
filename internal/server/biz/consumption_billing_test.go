package biz

import (
	"context"
	"strings"

	"entgo.io/ent/dialect/sql"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagearchive"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
)

func addCrossChannelBilling(t *testing.T, f *consumptionFixture, failedCost *float64) (*ent.Request, *ent.Channel) {
	t.Helper()
	at := time.Now().Add(-time.Hour)
	req, winner := f.addRequest(t, at, "model-a", request.StatusCompleted, 1000, 100, 800, 0, 0.03, lo.ToPtr[int64](200))
	f.client.Request.UpdateOne(req).SetResultExecutionID(winner.ID).ExecX(f.ctx)
	other := f.client.Channel.Create().SetName("失败但已计费的渠道").SetType("openai").SetBaseURL("https://example.test").
		SetSupportedModels([]string{"model-a"}).SetDefaultTestModel("model-a").SetCredentials(objects.ChannelCredentials{}).SaveX(f.ctx)
	f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(other.ID).
		SetModelID("model-a").SetStatus(requestexecution.StatusFailed).SetRequestBody([]byte(`{}`)).
		SetCreatedAt(at).SetStream(true).SetResponseStatusCode(500).SetMetricsLatencyMs(1500).
		SetMetricsFirstByteLatencyMs(500).SetMetricsFirstTokenLatencyMs(1000).SaveX(f.ctx)
	f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(other.ID).
		SetAPIKeyID(f.key.ID).SetModelID("model-a").SetCreatedAt(at).SetPromptTokens(500).SetCompletionTokens(50).
		SetTotalTokens(550).SetPromptCachedTokens(100).SetNillableTotalCost(failedCost).SaveX(f.ctx)
	return req, other
}

func TestConsumptionBillingIncludesEveryAttempt(t *testing.T) {
	f := newConsumptionFixture(t)
	req, other := addCrossChannelBilling(t, f, lo.ToPtr(0.02))
	check := func(t *testing.T) {
		for _, dimension := range []ConsumptionDimension{ConsumptionDimensionAPIKey, ConsumptionDimensionUser, ConsumptionDimensionModel} {
			rows, err := f.service.queryGroups(f.ctx, f.project.ID, ConsumptionFilter{Dimension: dimension}, nil, time.Now())
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, 1, rows[0].Requests)
			require.InDelta(t, 0.05, rows[0].Cost, 1e-12)
			require.EqualValues(t, 1650, rows[0].Tokens)
			require.EqualValues(t, 900, rows[0].CacheRead)
			require.Equal(t, 1, rows[0].PricedRequests)
			require.EqualValues(t, 1, rows[0].SpeedCount)
			require.InDelta(t, 100, rows[0].SpeedSum, 1e-12, "request speed must use the adopted execution's 100 output tokens")
		}
		rows, err := f.service.queryGroups(f.ctx, f.project.ID, ConsumptionFilter{Dimension: ConsumptionDimensionChannel}, nil, time.Now())
		require.NoError(t, err)
		byID := lo.SliceToMap(rows, func(row consumptionGroup) (int, consumptionGroup) { return row.EntityID, row })
		require.InDelta(t, 0.02, byID[other.ID].Cost, 1e-12)
		require.InDelta(t, 0.03, byID[f.channel.ID].Cost, 1e-12)
		require.Equal(t, 0, byID[other.ID].Requests, "cost attribution must not move the logical request to a failed channel")
		require.Equal(t, 1, byID[f.channel.ID].Requests)
		require.EqualValues(t, 550, byID[other.ID].Tokens)
		require.EqualValues(t, 1100, byID[f.channel.ID].Tokens)
	}
	t.Run("live", check)
	// Request snapshots and usage archives can coexist with retained rows; the
	// supported transition must never double-charge the same saved usage.
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{req.ID}))
	t.Run("request snapshot with retained usage", check)
	logs := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).Select(ArchivedUsageFields...).AllX(f.ctx)
	require.NoError(t, ArchiveUsageStatistics(f.ctx, f.client, logs))
	f.client.UsageLog.Delete().Where(usagelog.RequestIDEQ(req.ID)).ExecX(f.ctx)
	f.client.RequestExecution.Delete().Where(requestexecution.RequestIDEQ(req.ID)).ExecX(f.ctx)
	f.client.Request.DeleteOneID(req.ID).ExecX(f.ctx)
	t.Run("archived", check)
}

func TestConsumptionBillingKeepsUnknownAttemptCost(t *testing.T) {
	f := newConsumptionFixture(t)
	addCrossChannelBilling(t, f, nil)
	rows, err := f.service.queryGroups(f.ctx, f.project.ID, ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey}, nil, time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.InDelta(t, 0.03, rows[0].Cost, 1e-12, "known costs remain visible")
	require.Equal(t, 1, rows[0].UnpricedRequests)
	require.Zero(t, rows[0].PricedRequests, "one known attempt cannot make the whole request fully priced")
	require.Nil(t, rows[0].metrics().AvgCostPerRequest)
}

func TestChannelRankingBilledAndSupplyAmounts(t *testing.T) {
	f := newConsumptionFixture(t)
	req, retry := addCrossChannelBilling(t, f, lo.ToPtr(4.0))
	logs := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).AllX(f.ctx)
	for _, bill := range logs {
		if bill.ChannelID == retry.ID {
			bill.Update().SetBilledCost(8).SetBillingGroupID(0).ExecX(f.ctx)
		} else {
			bill.Update().SetTotalCost(5).SetBilledCost(6).SetBillingGroupID(0).ExecX(f.ctx)
		}
	}
	input := ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCost}
	check := func() {
		rows, err := f.service.queryGroups(f.ctx, f.project.ID, input, nil, time.Now())
		require.NoError(t, err)
		entries := consumptionEntries(rows, input)
		sortConsumptionEntries(entries, input)
		require.Len(t, entries, 2)
		for i, expected := range []struct {
			channel, requests int
			billed, supply    float64
		}{{retry.ID, 0, 8, 4}, {f.channel.ID, 1, 6, 5}} {
			row := entries[i]
			require.Equal(t, expected.channel, row.ID.ID)
			require.Equal(t, expected.requests, row.Metrics.RequestCount)
			require.Equal(t, expected.supply, row.Metrics.TotalCost)
			require.Equal(t, lo.ToPtr(expected.billed), row.Metrics.BilledCost)
			require.Len(t, row.Models, 1)
			require.Equal(t, row.Metrics.BilledCost, row.Models[0].Metrics.BilledCost)
		}
	}
	check()
	require.NoError(t, ArchiveRequestStatistics(f.ctx, f.client, []int{req.ID}))
	check()
	logs = f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).AllX(f.ctx)
	require.NoError(t, ArchiveUsageStatistics(f.ctx, f.client, logs))
	f.client.UsageLog.Delete().Where(usagelog.RequestIDEQ(req.ID)).ExecX(f.ctx)
	check()
	require.NoError(t, f.client.UsageArchive.Update().Where(usagearchive.KindEQ(usagearchive.KindUsage), usagearchive.ChannelIDEQ(retry.ID)).SetBilledCost(0).Exec(f.ctx))
	rows, err := f.service.queryGroups(f.ctx, f.project.ID, input, nil, time.Now())
	require.NoError(t, err)
	for _, row := range rows {
		if row.EntityID == retry.ID {
			require.Equal(t, lo.ToPtr(0.0), row.metrics().BilledCost)
		}
	}
}

func TestConsumptionBillingLegacySnapshotIsNotInventedComplete(t *testing.T) {
	f := newConsumptionFixture(t)
	f.client.UsageArchive.Create().SetKind(usagearchive.KindRequest).SetSourceID(999).SetRequestID(999).
		SetProjectID(f.project.ID).SetAPIKeyID(f.key.ID).SetChannelID(f.channel.ID).SetModelID("model-a").
		SetOccurredAt(time.Now().Add(-time.Hour)).SetTotalCost(0.03).SetPricingComplete(true).SaveX(f.ctx)
	rows, err := f.service.queryGroups(f.ctx, f.project.ID, ConsumptionFilter{Dimension: ConsumptionDimensionAPIKey}, nil, time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.InDelta(t, 0.03, rows[0].Cost, 1e-12, "keep the known historical amount when individual usage is gone")
	require.Equal(t, 1, rows[0].UnpricedRequests, "legacy final-channel totals cannot prove all-attempt pricing completeness")
}

func TestChannelBillingCountsRequestOnceAcrossModels(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(lo.Ternary(missing, "partial", "complete"), func(t *testing.T) {
			f := newConsumptionFixture(t)
			req, other := addCrossChannelBilling(t, f, lo.ToPtr(0.02))
			bill := f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(other.ID).
				SetModelID("model-b").SetPromptTokens(20).SetCompletionTokens(10).SetTotalTokens(30)
			if !missing {
				bill.SetTotalCost(0.01)
			}
			bill.SaveX(f.ctx)
			start, end := req.CreatedAt.Add(-time.Hour), time.Now().Add(time.Hour)
			summary, err := queryChannelTodayUsage(f.ctx, f.client, []int{other.ID}, start, end)
			require.NoError(t, err)
			require.Zero(t, summary[0].RequestCount)
			require.Equal(t, lo.Ternary(missing, 0, 1), summary[0].PricedCount)
			require.Equal(t, lo.Ternary(missing, 1, 0), summary[0].UnpricedCount)
			require.InDelta(t, lo.Ternary(missing, 0.02, 0.03), summary[0].TotalCost, 1e-12)
			svc := &ChannelService{AbstractService: &AbstractService{db: f.client}}
			combined, err := svc.channelUsageWithModels(f.ctx, f.client, []int{other.ID}, start, end)
			require.NoError(t, err)
			require.Equal(t, summary[0].PricedCount, combined[0].PricedCount)
			require.Equal(t, summary[0].UnpricedCount, combined[0].UnpricedCount)
			require.InDelta(t, summary[0].TotalCost, combined[0].TotalCost, 1e-12)
			models, err := queryChannelTodayUsageModels(f.ctx, f.client, other.ID, start, end)
			require.NoError(t, err)
			require.Len(t, models, 2)
			rows, err := f.service.queryGroups(f.ctx, f.project.ID, ConsumptionFilter{Dimension: ConsumptionDimensionChannel}, nil, end)
			require.NoError(t, err)
			entries := consumptionEntries(rows, ConsumptionFilter{Dimension: ConsumptionDimensionChannel})
			entry, ok := lo.Find(entries, func(item *ConsumptionEntry) bool { return item.ID.ID == other.ID })
			require.True(t, ok)
			require.Equal(t, lo.Ternary(missing, 0, 1), entry.Metrics.PricedRequestCount)
			if missing {
				require.Nil(t, entry.Metrics.AvgCostPerRequest)
			} else {
				require.InDelta(t, 0.03, *entry.Metrics.AvgCostPerRequest, 1e-12)
			}
			cacheFilter := ConsumptionFilter{Dimension: ConsumptionDimensionChannel, Metric: ConsumptionMetricCache}
			cacheRows, err := f.service.queryGroups(f.ctx, f.project.ID, cacheFilter, nil, end)
			require.NoError(t, err)
			cached, ok := lo.Find(consumptionEntries(cacheRows, cacheFilter), func(item *ConsumptionEntry) bool { return item.ID.ID == other.ID })
			require.True(t, ok)
			require.InDelta(t, entry.Metrics.TotalCost, cached.Metrics.TotalCost, 1e-12, "a cached retry keeps costs from the same request/channel's uncached model")
			require.Equal(t, 1, cached.Metrics.CacheRequests)
			require.EqualValues(t, 580, cached.Metrics.TotalTokens)
		})
	}
}

func TestRequestUsageSummariesBillingAndPermissions(t *testing.T) {
	f := newConsumptionFixture(t)
	req, _ := addCrossChannelBilling(t, f, lo.ToPtr(0.02))
	check := func(ctx context.Context, archived int) {
		values, err := RequestUsageSummaries(ctx, f.client, []int{req.ID, -1})
		require.NoError(t, err)
		require.Len(t, values, 1)
		value := values[req.ID]
		require.Equal(t, 2, value.UsageRecords)
		require.Equal(t, archived, value.ArchivedRecords)
		require.Zero(t, value.UnpricedRecords)
		require.InDelta(t, 0.05, *value.TotalCost, 1e-12)
		require.EqualValues(t, 1650, value.TotalTokens)
		require.EqualValues(t, 900, value.PromptCachedTokens)
		if archived > 0 {
			require.Nil(t, value.CostPriceMultiplier)
			require.Nil(t, value.PromptWriteCachedTokens5m)
			require.Empty(t, value.CostItems)
		}
	}
	check(f.viewer(t, f.project.ID, string(scopes.ScopeReadRequests)), 0)
	_, err := RequestUsageSummaries(f.viewer(t, f.project.ID), f.client, []int{req.ID})
	require.Error(t, err)
	values, err := RequestUsageSummaries(f.viewer(t, f.project.ID+1, string(scopes.ScopeReadRequests)), f.client, []int{req.ID})
	require.NoError(t, err)
	require.Empty(t, values)
	logs := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID)).Select(ArchivedUsageFields...).AllX(f.ctx)
	require.NoError(t, ArchiveUsageStatistics(f.ctx, f.client, logs))
	f.client.UsageLog.Delete().Where(usagelog.RequestIDEQ(req.ID)).ExecX(f.ctx)
	check(f.viewer(t, f.project.ID, string(scopes.ScopeReadRequests)), 2)
}

func TestRequestUsageSummariesUnknownAndZero(t *testing.T) {
	for _, amount := range []*float64{nil, lo.ToPtr(0.0)} {
		f := newConsumptionFixture(t)
		req, _ := addCrossChannelBilling(t, f, amount)
		values, err := RequestUsageSummaries(f.ctx, f.client, []int{req.ID})
		require.NoError(t, err)
		require.Equal(t, lo.Ternary(amount == nil, 1, 0), values[req.ID].UnpricedRecords)
		require.InDelta(t, 0.03, *values[req.ID].TotalCost, 1e-12)
		f.client.UsageLog.Update().Where(usagelog.RequestIDEQ(req.ID)).ClearTotalCost().ExecX(f.ctx)
		values, err = RequestUsageSummaries(f.ctx, f.client, []int{req.ID})
		require.NoError(t, err)
		require.Nil(t, values[req.ID].TotalCost)
		require.Equal(t, 2, values[req.ID].UnpricedRecords)
	}
}

func TestChannelTodayArchivedBillsPostgres(t *testing.T) {
	f := newStatisticsPostgresFixture(t)
	req, other := addCrossChannelBilling(t, f, lo.ToPtr(0.02))
	// Only the retry bill is archived. Both channels must report their own costs.
	bill := f.client.UsageLog.Query().Where(usagelog.RequestIDEQ(req.ID), usagelog.ChannelIDEQ(other.ID)).
		Select(ArchivedUsageFields...).OnlyX(f.ctx)
	require.NoError(t, ArchiveUsageStatistics(f.ctx, f.client, []*ent.UsageLog{bill}))
	f.client.UsageLog.DeleteOneID(bill.ID).ExecX(f.ctx)
	start := req.CreatedAt.UTC().Truncate(time.Hour)
	end := start.Add(2 * time.Hour)
	require.NoError(t, f.client.Driver().Exec(f.ctx, `INSERT INTO usage_archives
(kind,source_id,request_id,project_id,model_id,occurred_at) SELECT k,1000000+n,1000000+n,$1,'old',$2
FROM generate_series(1,20000) n CROSS JOIN (VALUES ('request'),('usage')) kinds(k)`,
		[]any{f.project.ID, start.AddDate(0, 0, -7)}, nil))
	require.NoError(t, f.client.Driver().Exec(f.ctx, `INSERT INTO requests
(created_at,updated_at,project_id,channel_id,api_key_id,model_id,status,request_body)
SELECT $1,$1,$2,$3,$4,'pending','pending','{}' FROM generate_series(1,256)`,
		[]any{start, f.project.ID, f.channel.ID, f.key.ID}, nil))
	for _, stmt := range []string{"ANALYZE requests", "ANALYZE usage_archives"} {
		require.NoError(t, f.client.Driver().Exec(f.ctx, stmt, []any{}, nil))
	}
	ids := []int{f.channel.ID, other.ID}
	q, args := channelUsageModelAggregate(f.client.Driver().Dialect(), ids, start, end).Query()
	var cursor sql.Rows
	require.NoError(t, f.client.Driver().Query(f.ctx, "EXPLAIN (ANALYZE, TIMING OFF) "+q, args, &cursor))
	var plan []string
	require.NoError(t, sql.ScanSlice(&cursor, &plan))
	require.NoError(t, cursor.Close())
	var indexed []string
	for _, line := range plan {
		if strings.Contains(line, "usage_archive_by_request_id") {
			indexed = append(indexed, strings.TrimSpace(line))
		}
	}
	require.NotEmpty(t, indexed, strings.Join(plan, "\n"))
	t.Logf("archive rows=40001, indexed bill lookup: %s", strings.Join(indexed, "; "))
	svc := &ChannelService{AbstractService: &AbstractService{db: f.client}}
	rows, err := svc.channelUsageWithModels(f.ctx, f.client, ids, start, end)
	require.NoError(t, err)
	require.Equal(t, 257, rows[0].RequestCount)
	require.InDelta(t, 0.03, rows[0].TotalCost, 1e-12)
	require.Zero(t, rows[1].RequestCount)
	require.InDelta(t, 0.02, rows[1].TotalCost, 1e-12)
}
