package orchestrator

import (
	"testing"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestChannelProtocolAliasOrder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		strict     bool
		condition  *objects.Condition
		stream     bool
		format     llm.APIFormat
		routeCount int
	}{
		{name: "automatic keeps first available alias", format: llm.APIFormatOpenAIResponse, routeCount: 1},
		{name: "same protocol skips incompatible alias before dedup", strict: true, format: llm.APIFormatOpenAIChatCompletion, routeCount: 1},
		{name: "unknown condition matches first alias", condition: &objects.Condition{Type: objects.ConditionTypeCondition, Field: "stream", Operator: "eq", Value: true}, stream: true, format: llm.APIFormatOpenAIResponse, routeCount: 2},
		{name: "unknown condition falls back to second alias", condition: &objects.Condition{Type: objects.ConditionTypeCondition, Field: "stream", Operator: "eq", Value: true}, format: llm.APIFormatOpenAIChatCompletion, routeCount: 2},
		{name: "known protocol condition excludes first alias", condition: &objects.Condition{Type: objects.ConditionTypeCondition, Field: "request_format", Operator: "eq", Value: llm.APIFormatOpenAIResponse.String()}, format: llm.APIFormatOpenAIChatCompletion, routeCount: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := &biz.Channel{Channel: &ent.Channel{ID: 1, Type: channel.TypeOpenai, Name: "aliases", Status: channel.StatusEnabled, SupportedModels: []string{"upstream"}, Endpoints: []objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAIResponse.String()}}, Settings: &objects.ChannelSettings{
				ProtocolPolicy: &objects.ChannelProtocolPolicy{SameProtocolOnly: tc.strict},
				ModelMappings:  []objects.ModelMapping{{From: "alias-responses", To: "upstream"}, {From: "alias-chat", To: "upstream"}},
				ModelProtocols: []objects.ModelProtocol{{Model: "alias-responses", APIFormats: []string{llm.APIFormatOpenAIResponse.String()}}, {Model: "alias-chat", APIFormats: []string{llm.APIFormatOpenAIChatCompletion.String()}}},
			}}}
			m := &ent.Model{ModelID: "public", Settings: &objects.ModelSettings{Associations: []*objects.ModelAssociation{
				{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: 1, ModelID: "alias-responses"}},
				{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: 1, ModelID: "alias-chat"}},
			}}}
			if tc.condition != nil {
				m.Settings.Associations[0].When = &objects.ModelAssociationWhen{Enabled: true, Condition: tc.condition}
			}
			matches := biz.MatchAssociationsForModel(m.Settings.Associations, []*biz.Channel{ch}, "public")
			resolved := make([]*resolvedAssociationCandidate, 0, len(matches))
			for _, match := range matches {
				for _, conn := range match.Connections {
					resolved = append(resolved, &resolvedAssociationCandidate{channel: ch, priority: conn.Priority, models: conn.Models, when: match.Association.When})
				}
			}
			req := &llm.Request{Model: "public", RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion, Stream: lo.ToPtr(tc.stream)}
			selected := filterResolvedCandidatesForRequest(t.Context(), req, resolved)
			require.Len(t, selected, 1)
			require.Equal(t, tc.format.String(), selected[0].APIFormat)
			require.Len(t, selected[0].Models, 1)
			require.Equal(t, "alias-responses", resolved[0].models[0].RequestModel, "selection must not modify cached resolution")
			routes := InspectProtocolRoutes(t.Context(), &biz.SystemModelSettings{}, m, "public", req.APIFormat, []*biz.Channel{ch})
			require.Len(t, routes, tc.routeCount)
			if tc.routeCount == 1 {
				require.Equal(t, tc.format.String(), routes[0].APIFormat)
			}
			summary := SummarizeProtocolRoutes("public", "Public", routes)
			require.Equal(t, 1, summary.Native+summary.Converted+summary.Conditional+summary.Excluded)
		})
	}
}

func TestChannelProtocolPolicySelectionAndDispatch(t *testing.T) {
	ch := &biz.Channel{Channel: &ent.Channel{Type: channel.TypeAnthropic, Settings: &objects.ChannelSettings{ProtocolPolicy: &objects.ChannelProtocolPolicy{SameProtocolOnly: true}}}}
	req := &llm.Request{Model: "m", RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion}
	entry := []biz.ChannelModelEntry{{RequestModel: "m", ActualModel: "m"}}
	format, reason := selectChannelAPIFormat(t.Context(), ch, entry, req, "m")
	require.Empty(t, format)
	require.Equal(t, "conversion_disabled", reason)
	require.Empty(t, populateAPIFormat(t.Context(), []*ChannelModelsCandidate{{Channel: ch, Models: entry}}, req))
	require.ErrorIs(t, validateChannelProtocol(ch, req, llm.APIFormatAnthropicMessage), transformer.ErrUnsupportedConversion)
	ch.Settings.ProtocolPolicy.SameProtocolOnly = false
	format, _ = selectChannelAPIFormat(t.Context(), ch, entry, req, "m")
	require.Equal(t, llm.APIFormatAnthropicMessage.String(), format)
	ch.Settings.ProtocolPolicy.AllowedProtocols = []string{llm.APIFormatAnthropicMessage.String()}
	format, reason = selectChannelAPIFormat(t.Context(), ch, entry, req, "m")
	require.Empty(t, format)
	require.Equal(t, "client_protocol_denied", reason)
	req.APIFormat = llm.APIFormatAnthropicMessage
	format, _ = selectChannelAPIFormat(t.Context(), ch, entry, req, "m")
	require.Equal(t, llm.APIFormatAnthropicMessage.String(), format)
	ch.Settings.ProtocolPolicy = &objects.ChannelProtocolPolicy{AllowedProtocols: []string{llm.APIFormatOpenAIResponse.String()}, SameProtocolOnly: true}
	req.APIFormat = llm.APIFormatOpenAIResponseWebSocket
	require.NoError(t, validateChannelProtocol(ch, req, llm.APIFormatOpenAIResponse))
	req.RequestType = llm.RequestTypeImage
	require.NoError(t, validateChannelProtocol(ch, req, llm.APIFormatOpenAIImageGeneration))
}

func TestProtocolRoutesShareSelectionAndCountChannels(t *testing.T) {
	ch := &biz.Channel{Channel: &ent.Channel{ID: 1, Type: channel.TypeAnthropic, Name: "A", Status: channel.StatusEnabled, SupportedModels: []string{"upstream"}, Settings: &objects.ChannelSettings{}}}
	m := &ent.Model{ModelID: "public", Settings: &objects.ModelSettings{Associations: []*objects.ModelAssociation{{Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: 1, ModelID: "upstream"}}}}}
	routes := InspectProtocolRoutes(t.Context(), &biz.SystemModelSettings{}, m, "public", llm.APIFormatOpenAIChatCompletion, []*biz.Channel{ch})
	require.Len(t, routes, 1)
	require.Equal(t, "converted", routes[0].State)
	ch.Settings.ProtocolPolicy = &objects.ChannelProtocolPolicy{SameProtocolOnly: true}
	routes = InspectProtocolRoutes(t.Context(), &biz.SystemModelSettings{}, m, "public", llm.APIFormatOpenAIChatCompletion, []*biz.Channel{ch})
	require.Equal(t, "excluded", routes[0].State)
	require.Equal(t, "conversion_disabled", routes[0].Reason)
	summary := SummarizeProtocolRoutes("m", "m", []*ProtocolRoute{{ChannelID: 1, State: "native"}, {ChannelID: 1, State: "conditional"}, {ChannelID: 2, State: "converted"}})
	require.Equal(t, 1, summary.Native)
	require.Equal(t, 1, summary.Converted)
	require.Zero(t, summary.Conditional)
	ch.Settings.ProtocolPolicy = nil
	m.Settings.Associations[0].When = &objects.ModelAssociationWhen{Enabled: true, Condition: &objects.Condition{Type: objects.ConditionTypeCondition, Field: "prompt_tokens", Operator: "gt", Value: 100}}
	alternative := *m.Settings.Associations[0]
	alternative.When = &objects.ModelAssociationWhen{Enabled: true, Condition: &objects.Condition{Type: objects.ConditionTypeCondition, Field: "stream", Operator: "eq", Value: true}}
	m.Settings.Associations = append(m.Settings.Associations, &alternative)
	routes = InspectProtocolRoutes(t.Context(), &biz.SystemModelSettings{}, m, "public", llm.APIFormatOpenAIChatCompletion, []*biz.Channel{ch})
	require.Len(t, routes, 1)
	require.Equal(t, "conditional", routes[0].State)
	require.Equal(t, "or", routes[0].Condition.Logic)
	require.Len(t, routes[0].Condition.Conditions, 2)
}

func TestProtocolConditionDoesNotInventRequestValues(t *testing.T) {
	format := objects.Condition{Type: objects.ConditionTypeCondition, Field: "request_format", Operator: "eq", Value: llm.APIFormatAnthropicMessage.String()}
	unknown := objects.Condition{Type: objects.ConditionTypeCondition, Field: "prompt_tokens", Operator: "gt", Value: 100}
	c := &objects.Condition{Type: objects.ConditionTypeGroup, Logic: "and", Conditions: []objects.Condition{format, unknown}}
	known, value := protocolCondition(c, llm.APIFormatOpenAIChatCompletion.String())
	require.True(t, known)
	require.False(t, value)
	known, _ = protocolCondition(c, llm.APIFormatAnthropicMessage.String())
	require.False(t, known)
	c.Logic = " OR "
	known, value = protocolCondition(c, llm.APIFormatAnthropicMessage.String())
	require.True(t, known)
	require.True(t, value)
	for _, condition := range []objects.Condition{
		{Type: objects.ConditionTypeGroup, Logic: "or"},
		{Conditions: []objects.Condition{{Field: "request_format", Operator: "eq", Value: llm.APIFormatAnthropicMessage.String()}}},
	} {
		known, value = protocolCondition(&condition, llm.APIFormatAnthropicMessage.String())
		require.True(t, known)
		require.Equal(t, objects.Evaluate(condition, map[string]any{"request_format": llm.APIFormatAnthropicMessage.String()}), value)
	}
}

func TestChannelProtocolPolicyRetryAndFinalDispatch(t *testing.T) {
	ctx, requestService, execution := setupRetryExecution(t)
	outbound := &mockTransformer{apiFormat: llm.APIFormatOpenAIChatCompletion}
	ch := &biz.Channel{Channel: &ent.Channel{
		ID: 1, Type: channel.TypeOpenai, Name: "protocol-policy",
		Endpoints: []objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAIResponse.String()}},
		Settings: &objects.ChannelSettings{
			ProtocolPolicy: &objects.ChannelProtocolPolicy{SameProtocolOnly: true},
			ModelProtocols: []objects.ModelProtocol{
				{Model: "denied", APIFormats: []string{llm.APIFormatOpenAIResponse.String()}},
			},
		},
	}, Outbound: outbound}
	req := &llm.Request{Model: "public", RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion}
	candidates := populateAPIFormat(ctx, []*ChannelModelsCandidate{{Channel: ch, Models: []biz.ChannelModelEntry{
		{ActualModel: "first"}, {ActualModel: "denied"}, {ActualModel: "next"},
	}}}, req)
	require.Len(t, candidates, 1)
	require.Equal(t, []biz.ChannelModelEntry{{ActualModel: "first"}, {ActualModel: "next"}}, candidates[0].Models)
	p := &PersistentOutboundTransformer{state: &PersistenceState{
		ChannelModelsCandidates: candidates, CurrentCandidate: candidates[0],
		OriginalModel: "public", LlmRequest: req, RequestExec: execution, RequestService: requestService,
	}}
	_, err := p.TransformRequest(ctx, req)
	require.NoError(t, err)
	require.NoError(t, p.PrepareForRetry(ctx))
	require.Equal(t, 1, p.state.CurrentModelIndex)
	_, err = p.TransformRequest(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "next", req.Model)
	// Even a stale candidate or transformer emitting a different protocol cannot
	// escape the policy at the point where the HTTP request is returned for sending.
	outbound.requestAPIFormat = llm.APIFormatAnthropicMessage
	result, err := p.TransformRequest(ctx, req)
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	require.Nil(t, result)
	outbound.requestAPIFormat = llm.APIFormatOpenAIChatCompletion
	ch.Settings.ProtocolPolicy = &objects.ChannelProtocolPolicy{AllowedProtocols: []string{llm.APIFormatAnthropicMessage.String()}}
	result, err = p.TransformRequest(ctx, req)
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	require.Nil(t, result)
}

func TestChannelProtocolChatExcludesNonChatEndpoints(t *testing.T) {
	req := &llm.Request{Model: "m", RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion}
	ch := &biz.Channel{Channel: &ent.Channel{Endpoints: []objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAIImageGeneration.String()}}}}
	format, reason := selectChannelAPIFormat(t.Context(), ch, nil, req, "m")
	require.Empty(t, format)
	require.Equal(t, "no_compatible_endpoint", reason)
	require.Empty(t, populateAPIFormat(t.Context(), []*ChannelModelsCandidate{{Channel: ch, Models: []biz.ChannelModelEntry{{ActualModel: "m"}}}}, req))
	ch.Type = channel.TypeAnthropic
	for _, policy := range []*objects.ChannelProtocolPolicy{nil, {}} {
		ch.Settings = &objects.ChannelSettings{ProtocolPolicy: policy}
		format, reason = selectChannelAPIFormat(t.Context(), ch, nil, req, "m")
		require.Empty(t, reason)
		require.Equal(t, llm.APIFormatAnthropicMessage.String(), format)
	}
}
