package orchestrator

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesRecoveryAfterConfiguredCredentialRotation(t *testing.T) {
	for _, history := range []bool{false, true} {
		for _, live := range []bool{false, true} {
			t.Run(fmt.Sprintf("history=%t/live=%t", history, live), func(t *testing.T) {
				// Given the same allowed channel with a new credential, while the
				// persisted route refers to the old key and the lane may have history.
				lane := shared.NewResponsesLane("lane", shared.NewResponsesBudget(64<<20))
				t.Cleanup(lane.Close)
				origin := shared.ResponsesOrigin{ChannelID: 1, Format: "openai/responses-ws", Model: "gpt-test", Endpoint: "https://fixture.invalid/v1/responses", Generation: 1}
				record := shared.ResponsesRecord{ID: "parent", Origin: origin}
				if history {
					record.Snapshot = shared.NewResponsesSnapshot(nil, []json.RawMessage{json.RawMessage(`{"role":"user","content":"root"}`)}, nil)
				}
				lane.Remember(record)
				retired := false
				if live {
					require.True(t, lane.RegisterLease(origin, func(string) bool { return !retired }, func() { retired = true }))
				}
				ctx := contexts.EnsureContainer(shared.WithResponsesAttempt(shared.WithSessionScope(shared.WithResponsesLane(shared.WithResponsesWebSocket(t.Context()), lane), "project:1:key:1"), &shared.ResponsesAttempt{}))
				raw := []byte(`{"model":"gpt-test","previous_response_id":"parent","service_tier":"priority","input":"delta"}`)
				req, err := responses.NewInboundTransformer().TransformRequest(ctx, &httpclient.Request{Body: raw})
				require.NoError(t, err)
				outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{BaseURL: "https://fixture.invalid/v1", APIKeyProvider: biz.NewChannelAPIKeyContextProvider(auth.NewStaticKeyProvider("fixture-new")), Transport: responses.TransportWebSocket})
				require.NoError(t, err)
				channel := &biz.Channel{Channel: &ent.Channel{ID: 1, Credentials: objects.ChannelCredentials{APIKey: "fixture-new"}, Settings: &objects.ChannelSettings{}}, Outbound: outbound}
				candidate := &ChannelModelsCandidate{Channel: channel, APIFormat: string(llm.APIFormatOpenAIResponseWebSocket), Models: []biz.ChannelModelEntry{{RequestModel: "gpt-test", ActualModel: "gpt-test"}}}
				provider := &continuationRouteFixture{found: true, route: biz.ResponsesContinuationRoute{ChannelID: 1, KeyRef: upstream.KeyRef("fixture-old")}}
				selected, handled, err := selectResponsesContinuation(ctx, req, []*ChannelModelsCandidate{candidate}, provider)
				require.NoError(t, err)
				require.True(t, handled)
				require.Equal(t, 1, selected[0].Channel.ID)
				c := newResponsesContinuation(nil, raw)
				defer c.releaseRetention()
				defer c.releaseCapture()
				p := &PersistentOutboundTransformer{wrapped: outbound, state: &PersistenceState{ChannelModelsCandidates: selected, LlmRequest: req, ResponsesContinuation: c}}
				// When the production transform resolves the old route's credential.
				wire, err := p.TransformRequest(ctx, req)
				if !history {
					require.ErrorContains(t, err, "绑定的上游凭据当前不可用")
					require.True(t, selected[0].continuationStrict)
					return
				}
				require.NoError(t, err)
				require.True(t, c.complete)
				require.False(t, selected[0].continuationStrict)
				if live {
					require.True(t, retired, "removed credentials must not leave a reusable old socket")
				}
				wire, err = httpclient.FinalizeAuthHeaders(wire)
				require.NoError(t, err)
				wire, err = applyResponsesContinuationBody(p).OnOutboundRawRequest(ctx, wire)
				require.NoError(t, err)
				wire, err = verifyResponsesAttempt(p).OnOutboundRawRequest(ctx, wire)
				require.NoError(t, err)
				// Then the final request uses the current credential and full context.
				require.Equal(t, "Bearer fixture-new", wire.Headers.Get("Authorization"))
				require.False(t, gjson.GetBytes(wire.Body, "previous_response_id").Exists())
				require.Contains(t, string(wire.Body), "root")
				require.Contains(t, string(wire.Body), "delta")
				require.Equal(t, "priority", gjson.GetBytes(wire.Body, "service_tier").String())
			})
		}
	}
}
