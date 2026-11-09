package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

type failingSessionHistoryDriver struct {
	dialect.Driver
	failures int
}

func (d *failingSessionHistoryDriver) Query(ctx context.Context, query string, args, value any) error {
	if strings.Contains(query, "previous_channel") {
		d.failures++
		return errors.New("fixture session history failure")
	}
	return d.Driver.Query(ctx, query, args, value)
}

func TestSelfUsageChannelHistoryFailureKeepsLog(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(f.ctx)
	group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("visible").SetShowChannelInSelfUsage(true).SaveX(ctx)
	f.key.Update().SetGroupRef(group.ID).ExecX(ctx)
	channel := f.client.Channel.Create().SetType("openai").SetName("display").SetTags([]string{"tag"}).
		SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	req := f.request(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "model")
	execution := f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(channel.ID).
		SetModelID("model").SetStatus("completed").SetRequestBody([]byte(`{}`)).
		SetCostSnapshot(&objects.RequestCostSnapshot{Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("1.5")}}).SaveX(ctx)
	req.Update().SetResultExecutionID(execution.ID).ExecX(ctx)
	driver := &failingSessionHistoryDriver{Driver: f.client.Driver()}
	service := NewSelfUsageService(ent.NewClient(ent.Driver(driver)), f.service.system)
	page, err := service.Requests(f.ctx, "2026-11-05", "2026-11-05", nil, "", 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, driver.failures)
	require.Len(t, page.Items, 1)
	require.Equal(t, "display", page.Items[0].Channel.Name)
	require.Equal(t, []string{"tag"}, page.Items[0].Channel.Tags)
	require.Equal(t, 1.5, *page.Items[0].Channel.Multiplier)
	require.Empty(t, page.Items[0].Channel.SessionState)
}

func TestSelfUsageChannelVisibilityAndSelection(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(f.ctx)
	group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("visible").SaveX(ctx)
	f.key.Update().SetGroupRef(group.ID).ExecX(ctx)
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	channels := make([]*ent.Channel, 2)
	for i := range channels {
		channels[i] = f.client.Channel.Create().SetType("openai").SetName(fmt.Sprintf("current-%d", i)).
			SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	}
	makeAttempt := func(req *ent.Request, index int, state, dispatch, snapshot string) *ent.RequestExecution {
		return f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(channels[index].ID).
			SetModelID("model").SetUpstreamModelID("model").SetStatus(requestexecution.Status(state)).SetRequestBody([]byte(`{}`)).
			SetSelection(&objects.RequestExecutionSelection{ChannelName: snapshot, Dispatch: dispatch, KeyHint: "private-hint", KeyRemark: "private-remark"}).SaveX(ctx)
	}
	winnerRequest := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "winner")
	winner := makeAttempt(winnerRequest, 0, "completed", objects.RequestExecutionDispatchStarted, "historical-winner")
	makeAttempt(winnerRequest, 1, "canceled", objects.RequestExecutionDispatchStarted, "loser")
	winnerRequest.Update().SetResultExecutionID(winner.ID).SetChannelID(channels[0].ID).ExecX(ctx)
	retryRequest := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusFailed, "retry")
	makeAttempt(retryRequest, 0, "failed", objects.RequestExecutionDispatchStarted, "last-dispatched")
	makeAttempt(retryRequest, 1, "canceled", objects.RequestExecutionDispatchPending, "never-dispatched")
	retryRequest.Update().SetChannelID(channels[0].ID).ExecX(ctx)
	processing := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusProcessing, "processing")
	makeAttempt(processing, 1, "processing", objects.RequestExecutionDispatchStarted, "")
	empty := f.request(at, f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "no-record")
	load := func(groupUsage bool) *SelfUsageRequestPage {
		page, err := f.service.Requests(f.ctx, "2026-11-05", "2026-11-05", nil, "", 1, 20, groupUsage)
		require.NoError(t, err)
		return page
	}
	for _, groupUsage := range []bool{false, true} {
		page := load(groupUsage)
		require.False(t, page.ChannelVisible)
		for _, row := range page.Items {
			require.Nil(t, row.Channel)
		}
	}
	group.Update().SetShowChannelInSelfUsage(true).ExecX(ctx)
	for _, groupUsage := range []bool{false, true} {
		page := load(groupUsage)
		require.True(t, page.ChannelVisible)
		byID := make(map[int]SelfUsageRequest)
		for _, row := range page.Items {
			byID[row.ID] = row
		}
		require.Equal(t, "historical-winner", byID[winnerRequest.ID].Channel.Name)
		require.Equal(t, "last-dispatched", byID[retryRequest.ID].Channel.Name)
		require.Equal(t, channels[1].Name, byID[processing.ID].Channel.Name)
		require.Nil(t, byID[empty.ID].Channel)
		require.Empty(t, byID[empty.ID].ModelExecutions)
		require.Len(t, byID[winnerRequest.ID].ModelExecutions, 2)
		require.Equal(t, "model", byID[winnerRequest.ID].ModelExecutions[1].UpstreamModelID)
		require.EqualValues(t, "completed", byID[winnerRequest.ID].ModelExecutions[1].Status)
	}
	group.Update().SetShowChannelInSelfUsage(false).ExecX(ctx)
	require.False(t, load(false).ChannelVisible, "the existing authenticated key must observe an updated switch")
	group.Update().SetShowChannelInSelfUsage(true).ExecX(ctx)
	other := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("hidden").SaveX(ctx)
	f.key.Update().SetGroupRef(other.ID).ExecX(ctx)
	require.False(t, load(false).ChannelVisible, "current membership controls historical requests")
	f.key.Update().ClearGroupRef().ExecX(ctx)
	page := load(false)
	require.False(t, page.ChannelVisible)
	for _, row := range page.Items {
		require.Nil(t, row.Channel)
	}
}

func TestSelfUsageChannelQueriesStayBounded(t *testing.T) {
	var queries []string
	capture := false
	f := newSelfUsageFixture(t, "UTC", enttest.WithOptions(ent.Debug(), ent.Log(func(v ...any) {
		if capture {
			queries = append(queries, fmt.Sprint(v...))
		}
	})))
	ctx := authz.WithTestBypass(f.ctx)
	group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("visible").SetShowChannelInSelfUsage(true).SaveX(ctx)
	f.key.Update().SetGroupRef(group.ID).ExecX(ctx)
	ch := f.client.Channel.Create().SetType("openai").SetName("display").SetCredentials(objects.ChannelCredentials{}).
		SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	for i := 0; i < 20; i++ {
		req := f.request(time.Date(2026, 11, 5, 12, 0, i, 0, time.UTC), f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "model")
		execution := f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(ch.ID).
			SetModelID("model").SetUpstreamModelID("model").SetStatus("completed").SetRequestBody([]byte(`{"private":"payload"}`)).SaveX(ctx)
		req.Update().SetResultExecutionID(execution.ID).ExecX(ctx)
	}
	read := func(size int) []string {
		queries = nil
		capture = true
		page, err := f.service.Requests(f.ctx, "2026-11-05", "2026-11-05", nil, "", 1, size)
		capture = false
		require.NoError(t, err)
		require.Len(t, page.Items, size)
		return queries
	}
	read(1) // Warm the existing general-settings cache before comparing page sizes.
	one, many := read(1), read(20)
	require.Equal(t, len(one), len(many), "queries must not grow per request row")
	nameQueries := 0
	for _, query := range many {
		if strings.Contains(query, "display_attempt") {
			nameQueries++
		}
		for _, field := range []string{"request_body", "response_body", "response_chunks", "credentials", "key_hint", "key_remark"} {
			require.NotContains(t, query, field)
		}
	}
	require.Equal(t, 1, nameQueries)
	group.Update().SetShowChannelInSelfUsage(false).ExecX(ctx)
	hidden := read(20)
	require.Equal(t, len(many)-2, len(hidden))
	for _, query := range hidden {
		require.NotContains(t, query, "display_attempt")
	}
	t.Logf("rows=1 queries=%d; rows=20 queries=%d; hidden queries=%d", len(one), len(many), len(hidden))
}

func TestSelfUsageChannelSessionHistoryOutsideVisibleScope(t *testing.T) {
	f := newSelfUsageFixture(t, "UTC")
	ctx := authz.WithTestBypass(f.ctx)
	group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("visible").SetShowChannelInSelfUsage(true).SaveX(ctx)
	f.key.Update().SetGroupRef(group.ID).ExecX(ctx)
	thread := f.client.Thread.Create().SetProjectID(f.project.ID).SetThreadID("channel-session").SaveX(ctx)
	trace := f.client.Trace.Create().SetProjectID(f.project.ID).SetTraceID("channel-session-trace").SetThreadID(thread.ID).SaveX(ctx)
	channels := make([]*ent.Channel, 3)
	for i := range channels {
		channels[i] = f.client.Channel.Create().SetType("openai").SetName(fmt.Sprint("channel-", i)).SetTags([]string{"tag-a", "tag-b"}).
			SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	}
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	add := func(keyID, channelIndex int, created time.Time, model string, status request.Status) {
		req := f.client.Request.Create().SetAPIKeyID(keyID).SetProjectID(f.project.ID).SetTraceID(trace.ID).
			SetSource(request.SourceAPI).SetStatus(status).SetModelID(model).SetRequestBody([]byte(`{}`)).SetCreatedAt(created).SaveX(ctx)
		execution := f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(channels[channelIndex].ID).
			SetModelID("model").SetStatus(requestexecution.Status(status)).SetRequestBody([]byte(`{}`)).
			SetSelection(&objects.RequestExecutionSelection{ChannelName: "historical", Provider: "openai", SessionState: "incorrect-snapshot", Dispatch: objects.RequestExecutionDispatchStarted}).SaveX(ctx)
		req.Update().SetResultExecutionID(execution.ID).SetChannelID(channels[channelIndex].ID).ExecX(ctx)
	}
	// These earlier requests are outside the selected dates and authenticated key.
	add(f.other.ID, 0, at.AddDate(0, -1, 0), "older-a", request.StatusCompleted)
	add(f.other.ID, 1, at.AddDate(0, -1, 1), "older-b", request.StatusCompleted)
	for i, tc := range []struct {
		model, state string
		channel      int
		status       request.Status
	}{
		{"returned", "returned", 0, request.StatusCompleted},
		{"reused", "reused", 0, request.StatusCompleted},
		{"new", "new", 2, request.StatusCompleted},
		{"active", "", 2, request.StatusProcessing},
	} {
		add(f.key.ID, tc.channel, at.Add(time.Duration(i)*time.Minute), tc.model, tc.status)
		page, err := f.service.Requests(f.ctx, "2026-11-05", "2026-11-05", &tc.model, "", 1, 1)
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		display := page.Items[0].Channel
		require.NotNil(t, display)
		require.Equal(t, tc.state, display.SessionState)
		require.Equal(t, "openai", display.Provider)
		require.Equal(t, "openai", display.ChannelType)
		require.Equal(t, []string{"tag-a", "tag-b"}, display.Tags)
	}
}

func TestSelfUsageChannelMultiplierMatchesAdminRules(t *testing.T) {
	for _, tc := range []struct {
		name        string
		snapshot    bool
		costs       []*float64
		multipliers []string
		want        *float64
		mixed       bool
	}{
		{name: "no snapshot", costs: []*float64{lo.ToPtr(1.0)}, multipliers: []string{"2"}},
		{name: "no usage", snapshot: true, want: lo.ToPtr(1.5)},
		{name: "unpriced usage", snapshot: true, costs: []*float64{nil}, multipliers: []string{"2"}, want: lo.ToPtr(1.5)},
		{name: "zero cost and multiplier", snapshot: true, costs: []*float64{lo.ToPtr(0.0)}, multipliers: []string{"0"}, want: lo.ToPtr(0.0)},
		{name: "priced usage", snapshot: true, costs: []*float64{lo.ToPtr(1.0)}, multipliers: []string{"2"}, want: lo.ToPtr(2.0)},
		{name: "mixed usage", snapshot: true, costs: []*float64{lo.ToPtr(1.0), lo.ToPtr(2.0)}, multipliers: []string{"2", "3"}, mixed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSelfUsageFixture(t, "UTC")
			ctx := authz.WithTestBypass(f.ctx)
			group := f.client.APIKeyGroup.Create().SetProjectID(f.project.ID).SetName("visible").SetShowChannelInSelfUsage(true).SaveX(ctx)
			f.key.Update().SetGroupRef(group.ID).ExecX(ctx)
			channel := f.client.Channel.Create().SetType("openai").SetName("display").SetCredentials(objects.ChannelCredentials{}).
				SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
			req := f.request(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), f.key.ID, f.project.ID, request.SourceAPI, request.StatusCompleted, "model")
			builder := f.client.RequestExecution.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetChannelID(channel.ID).
				SetModelID("model").SetStatus("completed").SetRequestBody([]byte(`{}`))
			if tc.snapshot {
				builder.SetCostSnapshot(&objects.RequestCostSnapshot{KeyRef: "private-key-ref", KeyLabel: "private-key-label", Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString("1.5")}})
			}
			execution := builder.SaveX(ctx)
			req.Update().SetResultExecutionID(execution.ID).ExecX(ctx)
			for i, cost := range tc.costs {
				usage := f.client.UsageLog.Create().SetRequestID(req.ID).SetProjectID(f.project.ID).SetModelID("model").
					SetCostSnapshot(&objects.RequestCostSnapshot{Applied: objects.AppliedCostMultiplier{Value: decimal.RequireFromString(tc.multipliers[i])}})
				if cost != nil {
					usage.SetTotalCost(*cost)
				}
				usage.SaveX(ctx)
			}
			page, err := f.service.Requests(f.ctx, "2026-11-05", "2026-11-05", nil, "", 1, 20)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.NotNil(t, page.Items[0].Channel)
			require.Equal(t, tc.want, page.Items[0].Channel.Multiplier)
			require.Equal(t, tc.mixed, page.Items[0].Channel.MultiplierMixed)
		})
	}
}
