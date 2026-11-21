package biz

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestEndpointDetectionRequiresProtocolResponse(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{}`, `{"error":{"message":"bad request"}}`, `{"object":"response","output":[],"status":"failed"}`} {
		for _, format := range []string{"openai/chat_completions", "openai/responses", "anthropic/messages"} {
			require.False(t, validEndpointDetectResponse(format, []byte(body)))
		}
	}
	for format, body := range map[string]string{
		llm.APIFormatOpenAIChatCompletion.String(): `{"object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}]}`,
		llm.APIFormatOpenAIResponse.String():       `{"object":"response","output":[],"status":"incomplete"}`,
		llm.APIFormatAnthropicMessage.String():     `{"type":"message","role":"assistant","content":[]}`,
	} {
		require.True(t, validEndpointDetectResponse(format, []byte(body)))
	}
}

func TestDeepSeekCustomEndpointPreservesProviderControls(t *testing.T) {
	for _, path := range []string{"", "/custom/chat/completions"} {
		ch := &Channel{Channel: &ent.Channel{Type: channel.TypeDeepseek, BaseURL: "https://unused.invalid"}, apiKeyOverride: "fixture"}
		svc := &ChannelService{}
		out, err := svc.buildNonDefaultEndpointOutbound(ch.Channel, ch, objects.ChannelEndpoint{APIFormat: llm.APIFormatOpenAIChatCompletion.String(), Path: path})
		require.NoError(t, err)
		req := &llm.Request{Model: "m", ReasoningEffort: "none", Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hello")}}}}
		wire, err := out.TransformRequest(t.Context(), req)
		require.NoError(t, err)
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(wire.Body, &body))
		require.JSONEq(t, `{"type":"disabled"}`, string(body["thinking"]))
		require.NotContains(t, body, "reasoning_effort")
		if path != "" {
			require.Equal(t, "https://unused.invalid"+path, wire.URL)
		}
		req.ResponseFormat = &llm.ResponseFormat{Type: "json_schema", JSONSchema: json.RawMessage(`{"name":"a","schema":{"type":"object"}}`)}
		_, err = out.TransformRequest(t.Context(), req)
		require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	}
}

func TestProtocolPolicyPatchPreservesOtherSettings(t *testing.T) {
	base := &objects.ChannelSettings{ExtraModelPrefix: "keep", ProtocolPolicy: &objects.ChannelProtocolPolicy{SameProtocolOnly: true}}
	updated := (&UpdateChannelSettingsInput{DisableSessionReuse: lo.ToPtr(true)}).Apply(base)
	require.True(t, updated.ProtocolPolicy.SameProtocolOnly)
	updated = (&UpdateChannelSettingsInput{ProtocolPolicy: &objects.ChannelProtocolPolicy{}}).Apply(&updated)
	require.False(t, updated.ProtocolPolicy.SameProtocolOnly)
	require.Equal(t, "keep", updated.ExtraModelPrefix)
	require.Error(t, (&objects.ChannelProtocolPolicy{AllowedProtocols: []string{"made-up"}}).Validate())
}

func TestProtocolModelIdentitiesFollowPublicModelResolution(t *testing.T) {
	prefix := &ent.Model{ID: 1, Type: model.TypeChat, ModelID: "gpt-", Name: "Prefix", MatchType: objects.ModelMatchPrefix, MatchPriority: 20, PublicModelIds: []string{"gpt-a", "gpt-b"}}
	exact := &ent.Model{ID: 2, Type: model.TypeChat, ModelID: "gpt-a", Name: "Exact", MatchType: objects.ModelMatchExact}
	hidden := &ent.Model{ID: 3, Type: model.TypeChat, ModelID: "-b", Name: "Suffix", MatchType: objects.ModelMatchSuffix, MatchPriority: 1, PublicModelIds: []string{}}
	models := []*ent.Model{prefix, exact, hidden}
	require.Equal(t, []ProtocolModelIdentity{{EntityID: 2, ModelID: "gpt-a", Name: "Exact"}}, ProtocolModelIdentities(models, ""))
	require.Empty(t, ProtocolModelIdentities(models, "prefix"))
	require.Len(t, ProtocolModelIdentities(models, "EXACT"), 1)
	hidden.PublicModelIds = []string{"gpt-b"}
	identities := ProtocolModelIdentities(models, "")
	require.Len(t, identities, 2)
	require.Equal(t, "gpt-b", identities[1].ModelID)
	require.Equal(t, hidden.ID, identities[1].EntityID)
}
