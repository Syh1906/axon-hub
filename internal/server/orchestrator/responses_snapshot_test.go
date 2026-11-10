package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesRecoveryAfterFinalOriginChanges(t *testing.T) {
	for _, changed := range []string{"credential", "endpoint"} {
		t.Run(changed, func(t *testing.T) {
			// Given an available parent socket, so prepare initially keeps the delta.
			lane := shared.NewResponsesLane("lane", shared.NewResponsesBudget(64<<20))
			t.Cleanup(lane.Close)
			previousWire := &httpclient.Request{URL: "https://example.invalid/v1/responses", Headers: map[string][]string{"Authorization": {"Bearer fixture-old"}}}
			origin := shared.ResponsesOrigin{ChannelID: 1, Format: "openai/responses-ws", Model: "model", Endpoint: previousWire.URL, Auth: responses.ContinuationAuthIdentity(previousWire), Generation: 1}
			lane.Remember(shared.ResponsesRecord{ID: "parent", Origin: origin, Snapshot: shared.NewResponsesSnapshot(nil, []json.RawMessage{json.RawMessage(`{"role":"user","content":"root"}`)}, []json.RawMessage{json.RawMessage(`{"role":"assistant","content":"answer"}`)})})
			require.True(t, lane.RegisterLease(origin, func(string) bool { return true }, func() {}))
			ctx := shared.WithResponsesAttempt(shared.WithSessionScope(shared.WithResponsesLane(shared.WithResponsesWebSocket(t.Context()), lane), "project:1:key:1"), &shared.ResponsesAttempt{})
			outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: previousWire.URL, APIKeyProvider: auth.NewStaticKeyProvider("fixture-old"), Transport: responses.TransportWebSocket})
			require.NoError(t, err)
			candidate := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{ID: 1}}, APIFormat: "openai/responses-ws", Models: []biz.ChannelModelEntry{{ActualModel: "model"}}}
			c := newResponsesContinuation(nil, []byte(`{"model":"model","previous_response_id":"parent","service_tier":"priority","input":"delta"}`))
			p := &PersistentOutboundTransformer{state: &PersistenceState{CurrentCandidate: candidate, ResponsesContinuation: c}, wrapped: outbound}
			body, err := prepareResponsesAttemptBody(ctx, p, responsesAttemptTarget{origin: origin, forwardsID: true})
			require.NoError(t, err)
			require.True(t, gjson.GetBytes(body, "previous_response_id").Exists())
			// When the final request uses a changed credential or endpoint.
			wireValue := *previousWire
			wire := &wireValue
			wire.Headers = previousWire.Headers.Clone()
			wire.Body, wire.APIFormat = body, string(llm.APIFormatOpenAIResponseWebSocket)
			if changed == "credential" {
				wire.Headers.Set("Authorization", "Bearer fixture-new")
			} else {
				wire.URL = "https://other.invalid/v1/responses"
			}
			actual, err := verifyResponsesAttempt(p).OnOutboundRawRequest(ctx, wire)
			// Then the old ID is never sent to the changed source, while current settings survive.
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(actual.Body, "previous_response_id").Exists())
			require.Contains(t, string(actual.Body), "root")
			require.Contains(t, string(actual.Body), "answer")
			require.Contains(t, string(actual.Body), "delta")
			require.Equal(t, "priority", gjson.GetBytes(actual.Body, "service_tier").String())
			c.releaseRetention()
		})
	}
}

func TestResponsesNativeRetentionOverflowDoesNotBlockHealthyDelta(t *testing.T) {
	// Given a complete response whose history cannot fit the lane budget.
	lane := shared.NewResponsesLane("tiny", shared.NewResponsesBudget(1))
	t.Cleanup(lane.Close)
	origin := shared.ResponsesOrigin{ChannelID: 1, Format: "openai/responses-ws", Model: "model", Generation: 1}
	lane.Remember(shared.ResponsesRecord{ID: "parent", Origin: origin, Snapshot: shared.NewResponsesSnapshot(nil, []json.RawMessage{json.RawMessage(`"root"`)}, nil)})
	require.Nil(t, lane.Record().Snapshot)
	live := true
	require.True(t, lane.RegisterLease(origin, func(string) bool { return live }, func() { live = false }))
	ctx := shared.WithSessionScope(shared.WithResponsesLane(shared.WithResponsesWebSocket(t.Context()), lane), "project:1:key:1")
	body := []byte(`{"previous_response_id":"parent","input":"delta"}`)
	c := newResponsesContinuation(nil, body)
	p := &PersistentOutboundTransformer{state: &PersistenceState{ResponsesContinuation: c}}
	// Healthy native delta remains valid despite optional history overflow.
	actual, err := prepareResponsesAttemptBody(ctx, p, responsesAttemptTarget{origin: origin, forwardsID: true})
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(actual))
	require.False(t, c.retain)
	// Once the socket is gone, incomplete history cannot become a fresh request.
	lane.RetireLeases()
	c = newResponsesContinuation(nil, body)
	p.state.ResponsesContinuation = c
	_, err = prepareResponsesAttemptBody(ctx, p, responsesAttemptTarget{origin: shared.ResponsesOrigin{ChannelID: 2, Format: "openai/responses-ws", Model: "model"}, forwardsID: true})
	require.ErrorIs(t, err, shared.ErrResponsesContinuationUnavailable)
}
