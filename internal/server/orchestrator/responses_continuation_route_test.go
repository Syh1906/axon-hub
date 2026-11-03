package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/upstream"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

type continuationRouteFixture struct {
	fakePreviousChannelProvider
	route      biz.ResponsesContinuationRoute
	found      bool
	err        error
	previousID string
}

func (p *continuationRouteFixture) GetResponsesContinuationRoute(_ context.Context, previousID string) (biz.ResponsesContinuationRoute, bool, error) {
	p.previousID = previousID
	return p.route, p.found, p.err
}

func TestResponsesContinuationRouting(t *testing.T) {
	provider := &continuationRouteFixture{route: biz.ResponsesContinuationRoute{ChannelID: 2, KeyRef: "key-reference"}, found: true}
	policy := &mockRetryPolicyProvider{policy: &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 3, TraceStickyMode: biz.TraceStickyDisabled}}
	candidates := []*ChannelModelsCandidate{stickyTestCandidate(1, 0), stickyTestCandidate(2, 9)}
	selector := WithTraceStickyLoadBalancedSelector(&staticChannelSelector{candidates: candidates}, NewLoadBalancer(policy, nil), policy, provider)
	req := &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, Model: "gpt-test", Messages: []llm.Message{{ReasoningSignature: lo.ToPtr("opaque-test-value")}}}
	selected, err := selector.Select(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, 2, selected[0].Channel.ID)
	require.True(t, selected[0].continuationBound)
	require.Equal(t, "key-reference", selected[0].continuationKeyRef)
	require.False(t, candidates[1].continuationBound, "do not mutate the shared candidate")

	provider.route.ChannelID = 10
	_, err = selector.Select(t.Context(), req)
	var responseErr *llm.ResponseError
	require.ErrorAs(t, err, &responseErr)
	require.Equal(t, "continuation_channel_unavailable", responseErr.Detail.Code)
	provider.err = errors.New("database unavailable")
	_, err = selector.Select(t.Context(), req)
	require.ErrorIs(t, err, provider.err)

	provider.err, provider.found = nil, false
	selected, err = selector.Select(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, selected, 2, "imported history does not create an unknown binding")
	provider.found, provider.route.ChannelID = true, 2
	req.Messages = nil
	selected, err = selector.Select(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, selected, 2, "plain-text requests retain normal routing")
	ctx := context.WithValue(t.Context(), responsesPreviousIDContextKey{}, "resp_original")
	selected, err = selector.Select(ctx, req)
	require.NoError(t, err)
	require.Len(t, selected, 2, "restored plaintext has no opaque upstream dependency")
	req.Messages = []llm.Message{{ReasoningSignature: lo.ToPtr("opaque-test-value")}}
	selected, err = selector.Select(ctx, req)
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, "resp_original", provider.previousID, "preserve identity after local continuation expands the input")
}

func TestResponsesContinuationDetectsOpaqueInput(t *testing.T) {
	for _, input := range []string{
		`{"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`,
		`{"input":[{"type":"compaction","encrypted_content":"opaque"}]}`,
		`{"input":[{"type":"message","encrypted_content":"opaque"}]}`,
	} {
		require.True(t, requiresResponsesContinuationRoute(&llm.Request{APIFormat: llm.APIFormatOpenAIResponse, RawRequest: &httpclient.Request{Body: []byte(input)}}))
	}
	require.False(t, requiresResponsesContinuationRoute(&llm.Request{APIFormat: llm.APIFormatOpenAIChatCompletion, Messages: []llm.Message{{ReasoningSignature: lo.ToPtr("unrelated")}}}))
}

func TestResponsesContinuationFiltersModelFormatsBeforeHealthGate(t *testing.T) {
	for _, responsesFirst := range []bool{true, false} {
		name := "responses_first_and_unhealthy"
		if !responsesFirst {
			name = "chat_first"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			candidate := healthGateSelectorCandidate(1, 0, "responses-model", "chat-model")
			candidate.modelAPIFormats = []string{string(llm.APIFormatOpenAIResponse), string(llm.APIFormatOpenAIChatCompletion)}
			if !responsesFirst {
				candidate.Models[0], candidate.Models[1] = candidate.Models[1], candidate.Models[0]
				candidate.modelAPIFormats[0], candidate.modelAPIFormats[1] = candidate.modelAPIFormats[1], candidate.modelAPIFormats[0]
			}
			candidate.APIFormat = candidate.modelAPIFormats[0]
			provider := &continuationRouteFixture{route: biz.ResponsesContinuationRoute{ChannelID: 1}, found: true}
			selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{candidate}, biz.TraceStickyDisabled, nil)
			selector.previousChannelProvider = provider
			if responsesFirst {
				openHealthGateModel(t, gate, policy, candidate, "responses-model")
			}
			selected, err := selector.Select(t.Context(), &llm.Request{
				Model: "alias", APIFormat: llm.APIFormatOpenAIResponse, PreviousResponseID: lo.ToPtr("resp_fixture"),
			})
			require.NoError(t, err)
			require.Len(t, selected, 1)
			require.Len(t, selected[0].Models, 1)
			require.Equal(t, "responses-model", selected[0].Models[0].ActualModel)
			require.Equal(t, string(llm.APIFormatOpenAIResponse), selected[0].APIFormat)
			require.True(t, selected[0].continuationBound)
			require.Len(t, candidate.Models, 2, "the original routing candidate remains available to other requests")
		})
	}
}

func TestResponsesContinuationCredential(t *testing.T) {
	ch := &biz.Channel{Channel: &ent.Channel{Credentials: objects.ChannelCredentials{APIKeys: []string{"fixture-key-a123", "fixture-key-b456"}}}}
	key, err := continuationCredential(ch, biz.ResponsesContinuationRoute{KeyRef: upstream.KeyRef("fixture-key-a123")})
	require.NoError(t, err)
	require.Equal(t, "fixture-key-a123", key)
	key, err = continuationCredential(ch, biz.ResponsesContinuationRoute{KeyHint: upstream.MaskKey("fixture-key-b456")})
	require.NoError(t, err)
	require.Equal(t, "fixture-key-b456", key)
	_, err = continuationCredential(ch, biz.ResponsesContinuationRoute{KeyRef: "missing"})
	require.Error(t, err)
	ch.Credentials.APIKeys = append(ch.Credentials.APIKeys, "fixture-other-a123")
	_, err = continuationCredential(ch, biz.ResponsesContinuationRoute{KeyHint: upstream.MaskKey("fixture-key-a123")})
	require.Error(t, err, "legacy tail collisions must not choose an arbitrary account")
}

func TestResponsesContinuationRetryKeepsModelAndRejectsInvalidHistory(t *testing.T) {
	candidate := stickyTestCandidate(1, 0)
	candidate.continuationBound, candidate.TraceSticky = true, true
	candidate.Models = []biz.ChannelModelEntry{{ActualModel: "first"}, {ActualModel: "second"}}
	outbound := &PersistentOutboundTransformer{wrapped: &mockTransformer{}, state: &PersistenceState{
		CurrentCandidate: candidate, ChannelModelsCandidates: []*ChannelModelsCandidate{candidate},
	}}
	require.True(t, outbound.CanRetry(&llm.ResponseError{StatusCode: 502, Detail: llm.ErrorDetail{Message: "temporary provider error"}}))
	require.NoError(t, outbound.PrepareForRetry(t.Context()))
	require.Equal(t, "first", outbound.GetCurrentModelID())
	require.False(t, outbound.HasMoreChannels())
	require.False(t, outbound.CanRetry(&llm.ResponseError{StatusCode: 502, Detail: llm.ErrorDetail{
		Message: "The encrypted content for item fixture could not be verified. Reason: encrypted content hydration failed: Encrypted content could not be decrypted or parsed.",
	}}))
	for _, status := range []int{400, 401, 403, 409} {
		require.False(t, outbound.CanRetry(&llm.ResponseError{StatusCode: status}),
			"a bound continuation cannot try another model to recover status %d", status)
	}
	candidate.continuationBound, candidate.TraceSticky = false, false
	require.True(t, outbound.CanRetry(&llm.ResponseError{StatusCode: 400}),
		"ordinary multi-model routing retains its fallback")
}

func TestResponsesContinuationLegacyRetryKeepsSelectedCredential(t *testing.T) {
	for _, bound := range []bool{true, false} {
		name := "ordinary_request"
		if bound {
			name = "legacy_continuation"
		}
		t.Run(name, func(t *testing.T) {
			keys := &addressRetryKeys{}
			transformer, err := responses.NewOutboundTransformerWithConfig(&responses.Config{
				BaseURL: "https://fixture.example/v1", APIKeyProvider: biz.NewChannelAPIKeyContextProvider(keys),
			})
			require.NoError(t, err)
			candidate := stickyTestCandidate(1, 0)
			candidate.Models = []biz.ChannelModelEntry{{ActualModel: "gpt-test"}}
			candidate.APIFormat = string(llm.APIFormatOpenAIResponse)
			candidate.Channel.Outbound = transformer
			candidate.Channel.Settings = &objects.ChannelSettings{DisableKeySessionReuse: true}
			candidate.continuationBound, candidate.TraceSticky = bound, bound
			outbound := &PersistentOutboundTransformer{state: &PersistenceState{
				ChannelModelsCandidates: []*ChannelModelsCandidate{candidate},
			}}
			ctx := contexts.EnsureContainer(t.Context())
			request := &llm.Request{Model: "gpt-test", APIFormat: llm.APIFormatOpenAIResponse,
				Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("fixture")}}},
			}
			first, err := outbound.TransformRequest(ctx, request)
			require.NoError(t, err)
			require.Equal(t, "fixture-first-account", first.Auth.APIKey)
			require.True(t, outbound.CanRetry(&httpclient.Error{StatusCode: 503}))
			require.NoError(t, outbound.PrepareForRetry(ctx))
			second, err := outbound.TransformRequest(ctx, request)
			require.NoError(t, err)
			require.Equal(t, first.Body, second.Body)
			if bound {
				require.Equal(t, first.Auth.APIKey, second.Auth.APIKey)
				require.Equal(t, 1, keys.calls)
			} else {
				require.Equal(t, "fixture-another-account", second.Auth.APIKey)
				require.Equal(t, 2, keys.calls)
			}
		})
	}
}
