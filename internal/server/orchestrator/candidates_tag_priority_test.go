package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestChannelPriorityTagsKeyAndProjectInheritance(t *testing.T) {
	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "key", Profiles: []objects.APIKeyProfile{{Name: "key"}}}}
	key.Edges.Project = &ent.Project{Profiles: &objects.ProjectProfiles{ActiveProfile: "project", Profiles: []objects.ProjectProfile{{Name: "project", ChannelTags: []string{"project"}}}}}
	require.Equal(t, []string{"project"}, channelPriorityTags(key))
	key.Profiles.Profiles[0].ChannelTags = []string{"key", "shared"}
	require.Equal(t, []string{"key", "shared"}, channelPriorityTags(key))
	key.Profiles.Profiles[0].ChannelTagsMatchMode = objects.ChannelTagsMatchModeNone
	require.Empty(t, channelPriorityTags(key), "excluded tags do not provide defaults or fall back to project tags")
	key.Profiles.Profiles[0].ChannelTags = nil
	key.Edges.Project.Profiles.Profiles[0].ChannelTagsMatchMode = objects.ChannelTagsMatchModeNone
	require.Empty(t, channelPriorityTags(key))
	require.Empty(t, channelPriorityTags(nil))
}

func TestChannelPriorityTagDefaultsThenKeyOverrides(t *testing.T) {
	first := healthGateSelectorCandidate(1, 40, "one")
	second := healthGateSelectorCandidate(2, 30, "two")
	first.Channel.Tags = []string{"Codex", "Shared"}
	first.Channel.Settings = &objects.ChannelSettings{TagPriorities: []objects.ChannelTagPriority{
		{Tag: "Codex", Priority: 14}, {Tag: "Shared", Priority: 0}, {Tag: "removed", Priority: -5},
	}}
	second.Channel.Tags = []string{"Codex"}
	base := &staticChannelSelector{candidates: []*ChannelModelsCandidate{first, second}}
	tags := WithChannelTagPrioritySelector(base, []string{"Codex", "Shared", "removed"})
	result, err := tags.Select(context.Background(), &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Equal(t, 0, result[0].Priority, "overlapping matching tags take the minimum configured priority")
	require.Equal(t, 30, result[1].Priority, "missing default keeps association priority")
	result, err = WithChannelPrioritySelector(tags, []objects.ChannelPriority{{ChannelID: 1, Priority: 20}, {ChannelID: 999, Priority: -100}}).Select(context.Background(), &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Len(t, result, 2, "priority settings cannot introduce an out-of-scope channel")
	require.Equal(t, 20, result[0].Priority)
	require.Equal(t, 30, result[1].Priority)
	require.Equal(t, 40, first.Priority, "shared candidate snapshots stay unchanged")
	result, err = WithChannelTagPrioritySelector(base, nil).Select(context.Background(), &llm.Request{Model: "alias"})
	require.NoError(t, err)
	require.Equal(t, 40, result[0].Priority)
}

func TestChannelPriorityWeightTiersPrecedeStrategyScores(t *testing.T) {
	for _, debug := range []bool{false, true} {
		lb := newTestLoadBalancer(t, &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 3}, &channelBasedStrategy{
			name: "scores", scores: map[int]float64{1: 1000, 2: 1, 3: 500, 4: rateLimitExhaustedScore},
		})
		lb.debug = debug
		candidates := []*ChannelModelsCandidate{
			healthGateSelectorCandidate(1, 14, "model"), healthGateSelectorCandidate(2, 14, "model"),
			healthGateSelectorCandidate(3, 14, "model"), healthGateSelectorCandidate(4, 14, "model"),
		}
		for i, weight := range []int{0, 20, 20, 100} {
			candidates[i].Channel.OrderingWeight = weight
		}
		result := lb.Sort(context.Background(), candidates, "model", false)
		require.Equal(t, []int{3, 2, 1, 4}, lo.Map(result, func(candidate *ChannelModelsCandidate, _ int) int { return candidate.Channel.ID }))
	}
}

func TestChannelPriorityWeightTiersRespectFailureRecovery(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "debug"}[debug], func(t *testing.T) {
			high := healthGateSelectorCandidate(1, 14, "model")
			low := healthGateSelectorCandidate(2, 14, "model")
			high.Channel.OrderingWeight = 100
			low.Channel.OrderingWeight = 10
			now := time.Now()
			metrics := &mockMetricsProvider{metrics: map[int]*biz.AggregatedMetrics{
				1: {LastFailureAt: &now}, 2: {},
			}}
			metrics.metrics[1].ConsecutiveFailures = 3
			lb := newTestLoadBalancer(t, &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 2}, NewErrorAwareStrategy(metrics))
			lb.debug = debug
			candidates := []*ChannelModelsCandidate{high, low}
			require.Equal(t, 2, lb.Sort(context.Background(), candidates, "model", false)[0].Channel.ID)
			metrics.metrics[1] = &biz.AggregatedMetrics{}
			require.Equal(t, 1, lb.Sort(context.Background(), candidates, "model", false)[0].Channel.ID)

			breaker := biz.NewModelCircuitBreaker()
			lb.strategies = []LoadBalanceStrategy{NewModelAwareCircuitBreakerStrategy(breaker)}
			for range 3 {
				breaker.RecordError(context.Background(), 1, "model", false)
			}
			require.Equal(t, 2, lb.Sort(context.Background(), candidates, "model", false)[0].Channel.ID)
			breaker.RecordSuccess(context.Background(), 1, "model")
			require.Equal(t, 1, lb.Sort(context.Background(), candidates, "model", false)[0].Channel.ID)
		})
	}
}
