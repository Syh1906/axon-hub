package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestChannelResponseTimeouts(t *testing.T) {
	policy := &biz.RetryPolicy{Enabled: true, StreamFirstEventTimeoutSeconds: 20, NonStreamResponseTimeoutSeconds: 120}
	first, full := channelResponseTimeouts(policy, nil)
	require.Equal(t, 20*time.Second, first)
	require.Equal(t, 120*time.Second, full)
	ch := &biz.Channel{Channel: &ent.Channel{Settings: &objects.ChannelSettings{StreamFirstEventTimeoutSeconds: lo.ToPtr(0), NonStreamResponseTimeoutSeconds: lo.ToPtr(3)}}}
	first, full = channelResponseTimeouts(policy, ch)
	require.Zero(t, first)
	require.Equal(t, 3*time.Second, full)
	policy.Enabled = false
	first, full = channelResponseTimeouts(policy, ch)
	require.Zero(t, first)
	require.Equal(t, 3*time.Second, full, "channel limits do not depend on retries")
	first, full = channelResponseTimeouts(policy, nil)
	require.Zero(t, first)
	require.Zero(t, full)
}

func TestDisabledSessionReuseKeepsOrdinaryCandidates(t *testing.T) {
	first, second := stickyTestCandidate(1, 0), stickyTestCandidate(2, 1)
	second.Channel.Settings = &objects.ChannelSettings{DisableSessionReuse: true}
	candidates := []*ChannelModelsCandidate{first, second}
	sticky, remaining := extractStickyCandidate(candidates, 2)
	require.Nil(t, sticky)
	require.Equal(t, candidates, remaining)
}

func TestHealthGateSessionDisabledReuseDoesNotBind(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	f.candidates[0].Channel.Settings = &objects.ChannelSettings{DisableSessionReuse: true}
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	selected := f.selectCandidates(t, ctx)
	require.Equal(t, 1, selected[0].Channel.ID, "ordinary selection still permits the channel")
	f.complete(t, ctx, selected, 0, true, false)
	_, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.False(t, found)
}

func TestHealthGateSessionDisabledOwnerMigratesWithoutHysteresis(t *testing.T) {
	f := newHealthGateSessionFixture(t)
	f.candidates[1].Channel.Settings = &objects.ChannelSettings{DisableSessionReuse: true}
	ctx := contexts.WithTrace(f.ctx, &ent.Trace{ID: 10, ThreadID: 20})
	setHealthGateSessionOwner(f.request, ctx, 20, 10, biz.SessionOwner{ChannelID: 2})
	selected := f.selectCandidates(t, ctx)
	require.Equal(t, 1, selected[0].Channel.ID)
	require.False(t, selected[0].TraceSticky)
	require.True(t, selected[0].healthGate.probeEligible)
	record := f.complete(t, ctx, selected, 0, true, false)
	require.NotNil(t, record.Migration)
	require.Equal(t, objects.RoutingMigrationReasonSessionReuseDisabled, record.Migration.Reason)
	owner, found, err := f.request.GetSessionOwner(ctx, 20, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 1, owner.ChannelID)
}

func TestChannelPriorityOverridesRemainRequestLocal(t *testing.T) {
	first := healthGateSelectorCandidate(1, 0, "one")
	second := healthGateSelectorCandidate(2, 1, "two")
	duplicate := healthGateSelectorCandidate(1, 2, "one", "three")
	base := &staticChannelSelector{candidates: []*ChannelModelsCandidate{first, second, duplicate}}
	for _, tc := range []struct {
		name          string
		priorities    []objects.ChannelPriority
		first, second int
	}{
		{"key-a", []objects.ChannelPriority{{ChannelID: 1, Priority: 10}, {ChannelID: 2, Priority: 20}}, 10, 20},
		{"key-b", []objects.ChannelPriority{{ChannelID: 1, Priority: 20}, {ChannelID: 2, Priority: 0}, {ChannelID: 999, Priority: -1}}, 20, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := WithChannelPrioritySelector(base, tc.priorities).Select(context.Background(), &llm.Request{Model: "alias"})
			require.NoError(t, err)
			require.Len(t, result, 2)
			require.Equal(t, tc.first, result[0].Priority)
			require.Equal(t, tc.second, result[1].Priority)
			require.Len(t, result[0].Models, 2, "duplicate channels do not gain extra weight")
			require.Equal(t, 0, first.Priority)
			require.Equal(t, 1, second.Priority)
			require.Len(t, first.Models, 1)
		})
	}
}

func TestChannelPriorityMergePreservesRetryModelProtocols(t *testing.T) {
	ctx := context.Background()
	ch := &biz.Channel{Channel: &ent.Channel{
		ID: 1, Name: "multi-model",
		Endpoints: []objects.ChannelEndpoint{
			{APIFormat: llm.APIFormatOpenAIChatCompletion.String()},
			{APIFormat: llm.APIFormatAnthropicMessage.String()},
			{APIFormat: llm.APIFormatOpenAIResponse.String()},
		},
		Settings: &objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{
			{Model: "model-a", APIFormats: []string{llm.APIFormatAnthropicMessage.String()}},
			{Model: "model-b", APIFormats: []string{llm.APIFormatOpenAIResponse.String()}},
		}},
	}}
	req := &llm.Request{Model: "client-model", RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion}
	candidates := populateAPIFormat(ctx, []*ChannelModelsCandidate{
		{Channel: ch, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "model-a", ActualModel: "model-a"}}},
		{Channel: ch, Priority: 1, Models: []biz.ChannelModelEntry{{RequestModel: "model-b", ActualModel: "model-b"}}},
		{Channel: ch, Priority: 2, Models: []biz.ChannelModelEntry{{RequestModel: "model-b", ActualModel: "model-b"}}},
	}, req)
	merged, err := WithChannelPrioritySelector(&staticChannelSelector{candidates: candidates}, []objects.ChannelPriority{{ChannelID: 1, Priority: 10}}).Select(ctx, req)
	require.NoError(t, err)
	require.Len(t, merged, 1)
	require.Len(t, merged[0].Models, 2, "merging must deduplicate models and their protocol entries together")
	(&PersistentOutboundTransformer{}).refreshCandidateAPIFormat(ctx, merged[0], 1, req)
	require.Equal(t, llm.APIFormatOpenAIResponse.String(), merged[0].APIFormat, "retrying model-b must use its own forced protocol")
	require.Equal(t, []string{llm.APIFormatAnthropicMessage.String(), llm.APIFormatOpenAIResponse.String()}, merged[0].modelAPIFormats)

	remaining := cloneHealthGateCandidate(ctx, req, merged[0], []int{1}, nil)
	require.Equal(t, []string{llm.APIFormatOpenAIResponse.String()}, remaining.modelAPIFormats)
	require.Equal(t, llm.APIFormatOpenAIResponse.String(), remaining.APIFormat)
	require.Equal(t, "model-b", remaining.Models[0].ActualModel)

	merged[0].modelAPIFormats[0] = "changed-in-this-request"
	require.Equal(t, []string{llm.APIFormatAnthropicMessage.String()}, candidates[0].modelAPIFormats)
	require.Equal(t, llm.APIFormatAnthropicMessage.String(), candidates[0].APIFormat)
}
