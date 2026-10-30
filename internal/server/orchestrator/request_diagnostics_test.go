package orchestrator

import (
	"context"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExecutionSelectionKeepsActualKeyAndSession(t *testing.T) {
	const firstKey, secondKey = "fixture-key-first-a123", "fixture-key-second-b456"
	settings := &objects.ChannelSettings{KeySettings: []objects.ChannelKeySettings{{KeyRef: upstream.KeyRef(firstKey), Remark: "first"}, {KeyRef: upstream.KeyRef(secondKey), Remark: "second"}}}
	candidate := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "channel", Type: channel.TypeOpenai, Settings: settings}}, SessionState: "new", Priority: 7}
	state := &PersistenceState{ChannelModelsCandidates: []*ChannelModelsCandidate{candidate}, RoutingPolicy: EffectiveRoutingPolicy{LoadBalancerStrategy: "adaptive"}}
	first := executionSelection(contexts.WithChannelAPIKey(context.Background(), firstKey), state)
	candidate.TraceSticky = true
	second := executionSelection(contexts.WithChannelAPIKey(context.Background(), secondKey), state)
	require.Equal(t, "new", first.SessionState)
	require.Equal(t, "reused", second.SessionState)
	require.Equal(t, "first", first.KeyRemark)
	require.Equal(t, "second", second.KeyRemark)
	require.Equal(t, upstream.MaskKey(firstKey), first.KeyHint)
	require.Equal(t, upstream.MaskKey(secondKey), second.KeyHint)
	candidate.Channel.Name = "renamed"
	require.Equal(t, "channel", first.ChannelName)
	require.Equal(t, "channel", first.Candidates[0].ChannelName)
	candidate.TraceSticky = false
	candidate.healthGate = &healthGateCandidateInfo{decision: &healthGateRequestDecision{sticky: true, lookupFailed: true}}
	require.Equal(t, "unrecorded", executionSelection(context.Background(), state).SessionState)
}
