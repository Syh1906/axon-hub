package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestExpiredResponsesAffinityResumesOrdinarySessionRouting(t *testing.T) {
	provider := &continuationRouteFixture{found: false}
	provider.traceChannelIDs = map[int]int{7: 2}
	policy := &mockRetryPolicyProvider{policy: &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 1, TraceStickyMode: biz.TraceStickyPreferPreviousChannel}}
	candidates := []*ChannelModelsCandidate{stickyTestCandidate(1, 0), stickyTestCandidate(2, 9), stickyTestCandidate(3, -1)}
	candidates[0].APIFormat, candidates[1].APIFormat = string(llm.APIFormatOpenAIResponse), string(llm.APIFormatOpenAIResponse)
	candidates[2].APIFormat = string(llm.APIFormatOpenAIChatCompletion)
	selector := newLivePolicyTestSelector(&staticChannelSelector{candidates: candidates}, NewLoadBalancer(nil, nil), policy, provider)
	ctx := contexts.WithTrace(t.Context(), &ent.Trace{ID: 7})
	req := &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, Model: "alias", RawRequest: &httpclient.Request{Body: []byte(`{"input":[{"encrypted_content":"expired"}]}`)}}
	selected, err := selector.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 2, selected[0].Channel.ID, "an active ordinary binding still wins")
	provider.traceChannelIDs = nil
	selected, err = selector.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, selected[0].Channel.ID, "after both bindings expire, use current priority within Responses channels")
}

func TestResponsesContinuationUnavailableUsesEligibleCandidates(t *testing.T) {
	for _, reason := range []string{"disabled", "missing", "not_permitted", "mixed_origins", "lookup_failed"} {
		t.Run(reason, func(t *testing.T) {
			provider := &continuationRouteFixture{found: true, route: biz.ResponsesContinuationRoute{ChannelID: 10}}
			switch reason {
			case "disabled", "missing":
				provider.route.UnavailableCode = "continuation_channel_" + reason
			case "mixed_origins":
				provider.err = biz.ErrResponsesOriginConflict
			case "lookup_failed":
				provider.err = errors.New("origin lookup unavailable")
			}
			policy := &mockRetryPolicyProvider{policy: &biz.RetryPolicy{Enabled: true, MaxChannelRetries: 1}}
			candidates := []*ChannelModelsCandidate{stickyTestCandidate(1, 0), stickyTestCandidate(2, 1)}
			for _, candidate := range candidates {
				candidate.APIFormat = string(llm.APIFormatOpenAIResponse)
			}
			selector := newLivePolicyTestSelector(&staticChannelSelector{candidates: candidates}, NewLoadBalancer(nil, nil), policy, provider)
			body := []byte(`{"model":"gpt-test","input":[{"type":"reasoning","encrypted_content":"opaque-history"}]}`)
			request := &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, Model: "gpt-test", RawRequest: &httpclient.Request{Body: body}}
			selected, err := selector.Select(t.Context(), request)
			require.NoError(t, err)
			require.Len(t, selected, 2)
			require.Equal(t, body, request.RawRequest.Body, "changing providers must preserve the supplied history")
			for _, candidate := range selected {
				require.False(t, candidate.continuationBound)
				require.Contains(t, []int{1, 2}, candidate.Channel.ID)
			}
		})
	}
}

func TestResponsesContinuationHealthGatePreference(t *testing.T) {
	for _, mode := range []string{"preferred", "open_source", "reuse_disabled", "strict_reuse_disabled"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Unix(1000, 0)
			source := healthGateSelectorCandidate(2, 9, "responses", "chat")
			source.modelAPIFormats = []string{string(llm.APIFormatOpenAIResponse), string(llm.APIFormatOpenAIChatCompletion)}
			backup := healthGateSelectorCandidate(1, 0, "responses")
			backup.modelAPIFormats = []string{string(llm.APIFormatOpenAIResponse)}
			provider := &continuationRouteFixture{found: true, route: biz.ResponsesContinuationRoute{ChannelID: 2}}
			selector, gate, policy := healthGateSelectorFixture(&now, []*ChannelModelsCandidate{backup, source}, biz.TraceStickyDisabled, nil)
			selector.previousChannelProvider = provider
			req := &llm.Request{Model: "alias", APIFormat: llm.APIFormatOpenAIResponse, Messages: []llm.Message{{ReasoningSignature: lo.ToPtr("opaque")}}}
			want := 2
			switch mode {
			case "open_source":
				openHealthGateModel(t, gate, policy, source, "responses")
				want = 1
			case "reuse_disabled", "strict_reuse_disabled":
				source.Channel.Settings = &objects.ChannelSettings{DisableSessionReuse: true}
				want = 1
				if mode == "strict_reuse_disabled" {
					req.PreviousResponseID = lo.ToPtr("upstream-only")
					want = 2
				}
			}
			selected, err := selector.Select(t.Context(), req)
			require.NoError(t, err)
			require.NotEmpty(t, selected)
			require.Equal(t, want, selected[0].Channel.ID)
			for _, candidate := range selected {
				require.Len(t, candidate.Models, 1)
				require.Equal(t, "responses", candidate.Models[0].ActualModel)
			}
		})
	}
}

func TestResponsesContinuationStrictUnavailableStillRejects(t *testing.T) {
	for _, reason := range []string{"disabled", "not_permitted", "mixed_origins", "lookup_failed"} {
		t.Run(reason, func(t *testing.T) {
			provider := &continuationRouteFixture{found: true, route: biz.ResponsesContinuationRoute{ChannelID: 10}}
			switch reason {
			case "disabled":
				provider.route.UnavailableCode = "continuation_channel_disabled"
			case "mixed_origins":
				provider.err = biz.ErrResponsesOriginConflict
			case "lookup_failed":
				provider.err = errors.New("origin lookup unavailable")
			}
			_, handled, err := selectResponsesContinuation(t.Context(), &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, PreviousResponseID: lo.ToPtr("upstream-only")}, []*ChannelModelsCandidate{stickyTestCandidate(1, 0)}, provider)
			require.True(t, handled)
			require.Error(t, err)
		})
	}
}

type orderedOriginFixture struct {
	fakePreviousChannelProvider

	queries []string
}

func (p *orderedOriginFixture) GetResponsesAffinity(ctx context.Context, _ string, previousID, latest string) (biz.ResponsesContinuationRoute, bool, error) {
	route, found, err := p.GetResponsesContinuationRoute(ctx, previousID)
	if previousID == "" {
		p.queries = p.queries[:len(p.queries)-1]
	}
	if !found && err == nil && latest != "" {
		return p.GetResponsesContinuationRoute(ctx, "", latest)
	}
	return route, found, err
}

func (p *orderedOriginFixture) GetResponsesContinuationRoute(_ context.Context, id string, contents ...string) (biz.ResponsesContinuationRoute, bool, error) {
	query := id
	if len(contents) > 0 {
		query = contents[0]
	}
	p.queries = append(p.queries, query)
	return biz.ResponsesContinuationRoute{ChannelID: 2}, query == "latest" || query == "known-id", nil
}

func TestResponsesContinuationUsesLatestActualHistory(t *testing.T) {
	for _, id := range []string{"", "known-id", "missing-id"} {
		provider := &orderedOriginFixture{}
		req := &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, RawRequest: &httpclient.Request{Body: []byte(`{"input":[{"encrypted_content":"old"},{"encrypted_content":"latest"}]}`)}, Messages: []llm.Message{{ReasoningSignature: lo.ToPtr("normalized-old")}}}
		ctx := context.WithValue(t.Context(), responsesPreviousIDContextKey{}, id)
		selected, handled, err := selectResponsesContinuation(ctx, req, []*ChannelModelsCandidate{stickyTestCandidate(1, 0), stickyTestCandidate(2, 9)}, provider)
		require.NoError(t, err)
		require.True(t, handled)
		require.Equal(t, 2, selected[0].Channel.ID)
		want := []string{"latest"}
		if id == "known-id" {
			want = []string{id}
		} else if id != "" {
			want = []string{id, "latest"}
		}
		require.Equal(t, want, provider.queries)
	}
}

func TestResponsesContinuationChangedIdentityUsesCurrentConfiguration(t *testing.T) {
	for _, change := range []string{"credential", "address"} {
		t.Run(change, func(t *testing.T) {
			executor := &mockExecutor{response: &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp_old_identity","object":"response","status":"completed","model":"gpt-4","output":[{"id":"rs_old","type":"reasoning","summary":[],"encrypted_content":"gAAAA_opaque-old"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)}}
			ctx, processor, client, source := responsesOriginGateway(t, executor)
			_, err := processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":"first"}`)})
			require.NoError(t, err)
			update := client.Channel.UpdateOneID(source.Channel.ID)
			if change == "credential" {
				update.SetCredentials(objects.ChannelCredentials{APIKey: "fixture-new-key"})
			} else {
				update.SetBaseURL("https://new-address.example/v1")
			}
			source.Channel.Channel = update.SaveX(ctx)
			source.Channel.Outbound, err = responses.NewOutboundTransformerWithConfig(&responses.Config{
				BaseURL:        source.Channel.BaseURL,
				APIKeyProvider: biz.NewChannelAPIKeyContextProvider(auth.NewStaticKeyProvider(source.Channel.Credentials.APIKey)),
			})
			require.NoError(t, err)
			_, err = processor.Process(ctx, &httpclient.Request{Method: "POST", Body: []byte(`{"model":"gpt-4","input":[{"type":"reasoning","summary":[],"encrypted_content":"gAAAA_opaque-old"}]}`)})
			require.NoError(t, err)
			require.EqualValues(t, 2, executor.requestCalls.Load())
			require.Contains(t, string(executor.lastRequest.Body), "gAAAA_opaque-old")
			if change == "credential" {
				require.Equal(t, "Bearer fixture-new-key", executor.lastRequest.Headers.Get("Authorization"))
			} else {
				require.Contains(t, executor.lastRequest.URL, "new-address.example")
			}
		})
	}
}
