package gql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestAPIKeyUsesTemplate(t *testing.T) {
	templateID := 42

	tests := []struct {
		name     string
		apiKey   *ent.APIKey
		template map[int]struct{}
		expected bool
	}{
		{
			name: "matches a loaded template profile",
			apiKey: &ent.APIKey{
				Profiles: &objects.APIKeyProfiles{
					Profiles: []objects.APIKeyProfile{
						{TemplateID: &templateID},
					},
				},
			},
			template: map[int]struct{}{templateID: {}},
			expected: true,
		},
		{
			name: "does not match an independently managed profile",
			apiKey: &ent.APIKey{
				Profiles: &objects.APIKeyProfiles{
					Profiles: []objects.APIKeyProfile{
						{Name: "custom"},
					},
				},
			},
			template: map[int]struct{}{templateID: {}},
			expected: false,
		},
		{
			name:     "does not match a missing profile collection",
			apiKey:   &ent.APIKey{},
			template: map[int]struct{}{templateID: {}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(
				t,
				tt.expected,
				apiKeyUsesTemplate(tt.apiKey, tt.template),
			)
		})
	}
}

func TestIntersectAPIKeyIDs(t *testing.T) {
	result := intersectAPIKeyIDs([]int{1, 2, 3}, []int{2, 3, 4}, []int{3, 5})
	require.ElementsMatch(t, []int{3}, result)

	require.Empty(t, intersectAPIKeyIDs([]int{1}, []int{}))
	require.Nil(t, intersectAPIKeyIDs())
}

func TestSortAPIKeyStatsByCost(t *testing.T) {
	stats := []*AnalyticsAPIKeyStat{
		{Name: "low cost", Cost: 1, RequestCount: 100, TotalTokens: 1000},
		{
			Name: "high cost low requests", Cost: 10,
			RequestCount: 5, TotalTokens: 500,
		},
		{
			Name: "high cost high requests", Cost: 10,
			RequestCount: 10, TotalTokens: 100,
		},
	}

	sortAPIKeyStatsByCost(stats)

	require.Equal(t, "high cost high requests", stats[0].Name)
	require.Equal(t, "high cost low requests", stats[1].Name)
	require.Equal(t, "low cost", stats[2].Name)
}

func TestQueryAnalyticsModelStatsCountsExecutionAttempts(t *testing.T) {
	client := enttest.NewEntClient(
		t,
		"sqlite3",
		"file:model-analytics?mode=memory&_fk=1",
	)
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	project := client.Project.Create().
		SetName("Model analytics project").
		SaveX(ctx)
	premiumChannel := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Premium channel").
		SetCredentials(objects.ChannelCredentials{APIKey: "premium-key"}).
		SetSupportedModels([]string{"model-x"}).
		SetDefaultTestModel("model-x").
		SetStatus(channel.StatusEnabled).
		SetTags([]string{"premium", "east"}).
		SaveX(ctx)
	budgetChannel := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Budget channel").
		SetCredentials(objects.ChannelCredentials{APIKey: "budget-key"}).
		SetSupportedModels([]string{"model-x", "model-y"}).
		SetDefaultTestModel("model-x").
		SetStatus(channel.StatusEnabled).
		SetTags([]string{"budget"}).
		SaveX(ctx)
	client.Project.UpdateOne(project).
		SetProfiles(&objects.ProjectProfiles{
			ActiveProfile: "analytics",
			Profiles: []objects.ProjectProfile{
				{
					Name:       "analytics",
					ChannelIDs: []int{premiumChannel.ID},
				},
			},
		}).
		SaveX(ctx)

	retriedRequest := client.Request.Create().
		SetProjectID(project.ID).
		SetModelID("model-x").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(request.StatusCompleted).
		SaveX(ctx)
	client.RequestExecution.Create().
		SetProjectID(project.ID).
		SetRequestID(retriedRequest.ID).
		SetChannelID(premiumChannel.ID).
		SetModelID("model-x").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(requestexecution.StatusFailed).
		SetMetricsLatencyMs(1000).
		SaveX(ctx)
	client.RequestExecution.Create().
		SetProjectID(project.ID).
		SetRequestID(retriedRequest.ID).
		SetChannelID(budgetChannel.ID).
		SetModelID("model-x").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(requestexecution.StatusFailed).
		SetMetricsLatencyMs(500).
		SaveX(ctx)
	client.RequestExecution.Create().
		SetProjectID(project.ID).
		SetRequestID(retriedRequest.ID).
		SetChannelID(budgetChannel.ID).
		SetModelID("model-x").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetMetricsLatencyMs(1000).
		SaveX(ctx)
	client.UsageLog.Create().
		SetProjectID(project.ID).
		SetRequestID(retriedRequest.ID).
		SetChannelID(budgetChannel.ID).
		SetModelID("model-x").
		SetPromptTokens(900).
		SetPromptCachedTokens(300).
		SetCompletionTokens(100).
		SetTotalTokens(1000).
		SetTotalCost(1).
		SaveX(ctx)

	streamRequest := client.Request.Create().
		SetProjectID(project.ID).
		SetModelID("model-x").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(request.StatusCompleted).
		SetStream(true).
		SaveX(ctx)
	client.RequestExecution.Create().
		SetProjectID(project.ID).
		SetRequestID(streamRequest.ID).
		SetChannelID(premiumChannel.ID).
		SetModelID("model-x").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetStream(true).
		SetMetricsLatencyMs(2000).
		SetMetricsFirstTokenLatencyMs(500).
		SetMetricsFirstByteLatencyMs(500).
		SaveX(ctx)
	client.UsageLog.Create().
		SetProjectID(project.ID).
		SetRequestID(streamRequest.ID).
		SetChannelID(premiumChannel.ID).
		SetModelID("model-x").
		SetPromptTokens(2850).
		SetPromptCachedTokens(570).
		SetCompletionTokens(150).
		SetCompletionReasoningTokens(40).
		SetCompletionAudioTokens(10).
		SetTotalTokens(3000).
		SetTotalCost(3).
		SaveX(ctx)

	highCostRequest := client.Request.Create().
		SetProjectID(project.ID).
		SetModelID("model-y").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(request.StatusCompleted).
		SaveX(ctx)
	client.RequestExecution.Create().
		SetProjectID(project.ID).
		SetRequestID(highCostRequest.ID).
		SetChannelID(budgetChannel.ID).
		SetModelID("model-y").
		SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
		SetStatus(requestexecution.StatusCompleted).
		SetMetricsLatencyMs(500).
		SaveX(ctx)
	client.UsageLog.Create().
		SetProjectID(project.ID).
		SetRequestID(highCostRequest.ID).
		SetChannelID(budgetChannel.ID).
		SetModelID("model-y").
		SetPromptTokens(950).
		SetCompletionTokens(50).
		SetTotalTokens(1000).
		SetTotalCost(10).
		SaveX(ctx)

	resolver := &queryResolver{&Resolver{
		client: client,
		systemService: biz.NewSystemService(biz.SystemServiceParams{
			CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
			Ent:         client,
		}),
	}}
	_, err := resolver.AnalyticsChannelTags(context.Background())
	require.Error(t, err)

	tags, err := resolver.AnalyticsChannelTags(
		contexts.WithProjectID(ctx, project.ID),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"east", "premium"}, tags)

	stats, err := resolver.queryAnalyticsModelStats(ctx, nil)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	require.Equal(t, "model-y", stats[0].ID)

	modelX := stats[1]
	require.Equal(t, "model-x", modelX.ID)
	require.Equal(t, 4, modelX.RequestCount)
	require.Equal(t, 4000, modelX.TotalTokens)
	require.InDelta(t, 4, modelX.Cost, 0.0001)
	require.InDelta(t, 1000, modelX.CostPerMillion, 0.0001)
	require.InDelta(t, 50, modelX.SuccessRate, 0.0001)
	require.InDelta(t, 23.2, modelX.CacheHitRate, 0.0001)
	require.InDelta(t, 500, *modelX.AvgFirstTokenLatencyMs, 0.0001)
	require.Nil(t, modelX.AvgOutputTokensPerSecond, "the only stream contains audio output")
	require.Len(t, modelX.Channels, 2)
	require.Equal(t, "Premium channel", modelX.Channels[0].Name)
	require.Equal(t, 2, modelX.Channels[0].RequestCount)
	require.InDelta(t, 50, modelX.Channels[0].SuccessRate, 0.0001)
	require.InDelta(t, 20, modelX.Channels[0].CacheHitRate, 0.0001)
	require.Equal(t, "Budget channel", modelX.Channels[1].Name)
	require.Equal(t, 2, modelX.Channels[1].RequestCount)
	require.Equal(t, 1000, modelX.Channels[1].TotalTokens)
	require.InDelta(t, 1, modelX.Channels[1].Cost, 0.0001)
	require.InDelta(t, 50, modelX.Channels[1].SuccessRate, 0.0001)
	require.InDelta(t, 100.0/3, modelX.Channels[1].CacheHitRate, 0.0001)

	filtered, err := resolver.queryAnalyticsModelStats(ctx, &AnalyticsModelFilter{
		ChannelTags: []string{"premium"},
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "model-x", filtered[0].ID)
	require.Equal(t, 2, filtered[0].RequestCount)
	require.InDelta(t, 20, filtered[0].CacheHitRate, 0.0001)
	require.Len(t, filtered[0].Channels, 1)
	require.Equal(t, "Premium channel", filtered[0].Channels[0].Name)

	require.NoError(t, resolver.systemService.SetGeneralSettings(
		ctx,
		biz.SystemGeneralSettings{
			CurrencyCode: "USD",
			Timezone:     "Asia/Shanghai",
		},
	))
	for index, createdAt := range []time.Time{
		time.Date(2026, 8, 25, 15, 59, 59, 0, time.UTC),
		time.Date(2026, 8, 25, 16, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 26, 15, 59, 59, 0, time.UTC),
		time.Date(2026, 8, 26, 16, 0, 0, 0, time.UTC),
	} {
		boundaryRequest := client.Request.Create().
			SetProjectID(project.ID).
			SetModelID("model-timezone-boundary").
			SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
			SetStatus(request.StatusCompleted).
			SaveX(ctx)
		client.RequestExecution.Create().
			SetCreatedAt(createdAt).
			SetProjectID(project.ID).
			SetRequestID(boundaryRequest.ID).
			SetChannelID(premiumChannel.ID).
			SetModelID("model-timezone-boundary").
			SetRequestBody(objects.JSONRawMessage(`{"seed":true}`)).
			SetStatus(requestexecution.StatusCompleted).
			SetExternalID(fmt.Sprintf("timezone-boundary-%d", index)).
			SaveX(ctx)
	}

	boundaryStats, err := resolver.queryAnalyticsModelStats(
		ctx,
		&AnalyticsModelFilter{
			StartTime: lo.ToPtr("2026-08-26"),
			EndTime:   lo.ToPtr("2026-08-26"),
			ModelIDs:  []string{"model-timezone-boundary"},
		},
	)
	require.NoError(t, err)
	require.Len(t, boundaryStats, 1)
	require.Equal(t, 2, boundaryStats[0].RequestCount)
}

func TestModelAnalyticsFixtureIsIdempotent(t *testing.T) {
	client := enttest.NewEntClient(
		t,
		"sqlite3",
		"file:model-analytics-fixture?mode=memory&_fk=1",
	)
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	client.Project.Create().SetName("Fixture project").SaveX(ctx)
	fixturePath := filepath.Join(
		"..",
		"..",
		"..",
		"scripts",
		"e2e",
		"fixtures",
		"model-analytics.sql",
	)
	fixture, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	driver, ok := client.Driver().(*entsql.Driver)
	require.True(t, ok)

	for range 2 {
		_, err = driver.DB().ExecContext(ctx, string(fixture))
		require.NoError(t, err)
	}

	requestCount := client.Request.Query().
		Where(request.ExternalIDHasPrefix("model-analytics-seed:")).
		CountX(ctx)
	executionCount := client.RequestExecution.Query().
		Where(requestexecution.HasRequestWith(
			request.ExternalIDHasPrefix("model-analytics-seed:"),
		)).
		CountX(ctx)
	usageLogs := client.UsageLog.Query().
		Where(usagelog.HasRequestWith(
			request.ExternalIDHasPrefix("model-analytics-seed:"),
		)).
		AllX(ctx)

	require.Equal(t, 6, requestCount)
	require.Equal(t, 10, executionCount)
	require.Len(t, usageLogs, 5)
	require.Equal(t, int64(4500), lo.SumBy(
		usageLogs,
		func(usage *ent.UsageLog) int64 { return usage.PromptCachedTokens },
	))
}
