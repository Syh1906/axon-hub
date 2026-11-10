package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestImageGenerationToolSelector(t *testing.T) {
	blocked := stickyTestCandidate(1, 0)
	blocked.Channel.Settings = &objects.ChannelSettings{AllowImageGenerationTool: lo.ToPtr(false)}
	legacy := stickyTestCandidate(2, 1)
	legacy.Channel.Settings = nil
	unset := stickyTestCandidate(3, 2)
	unset.Channel.Settings = &objects.ChannelSettings{}
	enabled := stickyTestCandidate(4, 3)
	enabled.Channel.Settings = &objects.ChannelSettings{AllowImageGenerationTool: lo.ToPtr(true)}
	imageTool := []llm.Tool{{Type: llm.ToolTypeImageGeneration}}
	all := []*ChannelModelsCandidate{blocked, legacy, unset, enabled}
	for _, tc := range []struct {
		name string
		req  *llm.Request
	}{
		{"no tools", &llm.Request{}},
		{"ordinary function with same name", &llm.Request{Tools: []llm.Tool{{Type: llm.ToolTypeFunction, Function: llm.Function{Name: "image_generation"}}}}},
		{"standalone image", &llm.Request{RequestType: llm.RequestTypeImage, Image: &llm.ImageRequest{}, Tools: imageTool}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := WithImageGenerationToolSelector(&mockSelector{candidates: all}).Select(context.Background(), tc.req)
			require.NoError(t, err)
			require.Equal(t, all, got)
		})
	}
	req := &llm.Request{Tools: imageTool, ToolChoice: &llm.ToolChoice{ToolChoice: lo.ToPtr("required")}}
	selector := WithImageGenerationToolSelector(&mockSelector{candidates: all})
	got, err := selector.Select(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, []*ChannelModelsCandidate{legacy, unset, enabled}, got)
	require.Equal(t, imageTool, req.Tools)
	require.Equal(t, "required", *req.ToolChoice.ToolChoice)
	require.Same(t, blocked, all[0], "filtering must not mutate cached candidates")

	_, err = WithImageGenerationToolSelector(&mockSelector{candidates: []*ChannelModelsCandidate{blocked}}).Select(context.Background(), req)
	var responseErr *llm.ResponseError
	require.ErrorAs(t, err, &responseErr)
	require.Equal(t, http.StatusServiceUnavailable, responseErr.StatusCode)
	require.Equal(t, "image_generation_tool_not_allowed", responseErr.Detail.Code)

	got, err = WithImageGenerationToolSelector(&mockSelector{}).Select(context.Background(), req)
	require.NoError(t, err, "no initial candidates must retain the existing model/access error")
	require.Empty(t, got)
	originalErr := errors.New("selection failed")
	_, err = WithImageGenerationToolSelector(&mockSelector{err: originalErr}).Select(context.Background(), req)
	require.ErrorIs(t, err, originalErr)
}

func TestImageGenerationToolRoutingPipeline(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		blockedOnly, keyRestricted bool
	}{
		{"select allowed backup", false, false},
		{"all blocked", true, false},
		{"allowed channel outside key scope", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db := setupTest(t)
			project := createTestProject(t, ctx, db)
			ctx = contexts.WithProjectID(ctx, project.ID)
			channels, requests, system, usage := setupTestServices(t, db)
			first := createTestChannel(t, ctx, db)
			first = first.Update().SetName("blocked image tool").SaveX(ctx)
			second := createTestChannel(t, ctx, db)
			first.Settings = &objects.ChannelSettings{AllowImageGenerationTool: lo.ToPtr(false)}
			outbound, err := responses.NewOutboundTransformer("https://example.invalid/v1", "fixture-key")
			require.NoError(t, err)
			candidates := channelsToTestCandidates([]*biz.Channel{{Channel: first, Outbound: outbound}, {Channel: second, Outbound: outbound}}, "gpt-4")
			if tc.blockedOnly {
				candidates = candidates[:1]
			}
			if tc.keyRestricted {
				key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{ActiveProfile: "restricted", Profiles: []objects.APIKeyProfile{{Name: "restricted", ChannelIDs: []int{first.ID}}}}}
				ctx = contexts.WithAPIKey(ctx, key)
			}
			executor := &mockExecutor{response: &httpclient.Response{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp-image-tool","object":"response","model":"gpt-4","status":"completed","output":[]}`)}}
			processor := &ChatCompletionOrchestrator{
				channelSelector: &staticChannelSelector{candidates: candidates}, Inbound: responses.NewInboundTransformer(),
				RequestService: requests, ChannelService: channels, PromptProvider: &stubPromptProvider{}, SystemService: system, UsageLogService: usage,
				PipelineFactory: pipeline.NewFactory(executor), ModelMapper: NewModelMapper(), channelLimiterManager: NewChannelLimiterManager(),
			}
			result, err := processor.Process(ctx, &httpclient.Request{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"gpt-4","input":"draw a tree","stream":false,"tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`)})
			if tc.blockedOnly || tc.keyRestricted {
				var responseErr *llm.ResponseError
				require.ErrorAs(t, err, &responseErr)
				require.Equal(t, "image_generation_tool_not_allowed", responseErr.Detail.Code)
				require.Zero(t, executor.requestCalls.Load())
				require.Zero(t, db.RequestExecution.Query().CountX(ctx))
				failure := db.Request.GetX(ctx, result.RequestID).Failure
				require.NotNil(t, failure)
				require.Equal(t, "routing", failure.Stage)
				require.Equal(t, responseErr.Detail.Code, failure.Code)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, executor.requestCalls.Load())
			require.Equal(t, second.ID, db.RequestExecution.Query().OnlyX(ctx).ChannelID)
			require.Equal(t, "image_generation", gjson.GetBytes(executor.lastRequest.Body, "tools.0.type").String())
			require.Equal(t, "image_generation", gjson.GetBytes(executor.lastRequest.Body, "tool_choice.type").String())
		})
	}
}

func TestImageGenerationToolBeforeStreamPreference(t *testing.T) {
	blocked := stickyTestCandidate(1, 0)
	blocked.Channel.Settings = &objects.ChannelSettings{AllowImageGenerationTool: lo.ToPtr(false)}
	allowed := stickyTestCandidate(2, 1)
	allowed.Channel.Policies.Stream = objects.CapabilityPolicyRequire
	state := &PersistenceState{CandidateSelector: &staticChannelSelector{candidates: []*ChannelModelsCandidate{blocked, allowed}}}
	_, db := setupTest(t)
	system := newTestSystemService(db)
	state.Settings = (&ChatCompletionOrchestrator{SystemService: system}).loadRequestSettings(context.Background())
	req := &llm.Request{Model: "gpt-4", APIFormat: llm.APIFormatOpenAIResponse, RequestType: llm.RequestTypeChat, Stream: lo.ToPtr(false), Tools: []llm.Tool{{Type: llm.ToolTypeImageGeneration}}}
	_, err := selectCandidates(&PersistentInboundTransformer{state: state}, nil).OnInboundLlmRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, []*ChannelModelsCandidate{allowed}, state.ChannelModelsCandidates, "a disabled native candidate must not hide the eligible streaming fallback")
}
