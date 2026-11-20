//nolint:exhaustruct_v5 // Test fixtures intentionally set only fields relevant to each scenario.
package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestPopulateAPIFormatFiltersAlphaSearchUnsupportedChannels(t *testing.T) {
	unsupported := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenai}}}
	supported := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeCodex}}}

	candidates := populateAPIFormat(context.Background(), []*ChannelModelsCandidate{unsupported, supported}, &llm.Request{
		RequestType: llm.RequestTypeAlphaSearch,
		APIFormat:   llm.APIFormatOpenAIAlphaSearch,
	})

	require.Equal(t, []*ChannelModelsCandidate{supported}, candidates)
	require.Equal(t, llm.APIFormatOpenAIAlphaSearch.String(), supported.APIFormat)
}

func TestAlphaSearchUnmatchedAssociationDoesNotReportMissingEndpoint(t *testing.T) {
	resolved := []*resolvedAssociationCandidate{{
		channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenaiResponses}},
		models:  []biz.ChannelModelEntry{{ActualModel: "test-model"}},
		when: &objects.ModelAssociationWhen{
			Enabled: true,
			Condition: &objects.Condition{
				Type: objects.ConditionTypeCondition, Field: "request_format", Operator: "eq", Value: "openai/responses",
			},
		},
	}}
	req := &llm.Request{Model: "test-model", RequestType: llm.RequestTypeAlphaSearch, APIFormat: llm.APIFormatOpenAIAlphaSearch}
	candidates, err := candidatesForMatchedModel(t.Context(), req, &ent.Model{}, resolved)
	require.NoError(t, err)
	require.Empty(t, candidates)
}
