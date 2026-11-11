package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestConversionRequirementsSelectCapableEndpoint(t *testing.T) {
	req, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"test","input":"hello","tools":[{"type":"file_search","vector_store_ids":["vs_test"]}]}`)})
	require.NoError(t, err)
	endpoints := []objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAIChatCompletion.String()}, {APIFormat: llm.APIFormatOpenAIResponse.String()}}
	require.Equal(t, llm.APIFormatOpenAIResponse.String(), SelectAPIFormat(endpoints, req))
	require.Empty(t, SelectAPIFormat(endpoints[:1], req))
	ch := &biz.Channel{Channel: &ent.Channel{ID: 1, Endpoints: endpoints, Settings: &objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{
		{Model: "chat-only", APIFormats: []string{llm.APIFormatOpenAIChatCompletion.String()}}, {Model: "native", APIFormats: []string{llm.APIFormatOpenAIResponse.String()}},
	}}}}
	candidate := &ChannelModelsCandidate{Channel: ch, Models: []biz.ChannelModelEntry{{ActualModel: "chat-only"}, {ActualModel: "native"}}}
	selected := populateAPIFormat(t.Context(), []*ChannelModelsCandidate{candidate}, req)
	require.Len(t, selected, 1)
	require.Len(t, selected[0].Models, 1)
	require.Equal(t, "native", selected[0].Models[0].ActualModel)
	google := &llm.Request{RequestType: llm.RequestTypeChat, Tools: []llm.Tool{{Type: llm.ToolTypeGoogleCodeExecution}}}
	require.Empty(t, populateAPIFormat(t.Context(), []*ChannelModelsCandidate{{Channel: ch, Models: []biz.ChannelModelEntry{{ActualModel: "chat-only"}}}}, google))
	require.Equal(t, llm.APIFormatGeminiContents.String(), SelectAPIFormat(append(endpoints, objects.ChannelEndpoint{APIFormat: llm.APIFormatGeminiContents.String()}), google))
}

func TestConversionRequirementsPreserveNativeControls(t *testing.T) {
	endpoints := []objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAIChatCompletion.String()}, {APIFormat: llm.APIFormatAnthropicMessage.String()}, {APIFormat: llm.APIFormatOpenAIResponse.String()}}
	req := &llm.Request{RequestType: llm.RequestTypeChat, APIFormat: llm.APIFormatOpenAIChatCompletion, TransformerMetadata: map[string]any{"anthropic_container": "container_1"}}
	require.Equal(t, llm.APIFormatAnthropicMessage.String(), SelectAPIFormat(endpoints, req))
	state, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"test","input":"hello","conversation":"conv_1"}`)})
	require.NoError(t, err)
	require.Equal(t, llm.APIFormatOpenAIResponse.String(), SelectAPIFormat(endpoints, state))
	require.Empty(t, SelectAPIFormat(endpoints[:2], state))
	req.TransformerMetadata = map[string]any{"anthropic_native_tools": []any{map[string]any{"index": 0, "tool": map[string]any{"type": "code_execution_20250825", "name": "code_execution"}}}}
	require.Equal(t, llm.APIFormatAnthropicMessage.String(), SelectAPIFormat(endpoints, req))
	result, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"test","input":[{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]}`)})
	require.NoError(t, err)
	require.Equal(t, llm.APIFormatOpenAIResponse.String(), SelectAPIFormat(endpoints, result))
	require.Empty(t, SelectAPIFormat(endpoints[:2], result))
}

func TestConversionGuardUsesFinalProviderProtocol(t *testing.T) {
	out := &mockTransformer{apiFormat: llm.APIFormatOpenAIResponse, requestAPIFormat: llm.APIFormatOpenAIChatCompletion}
	ch := &biz.Channel{Channel: &ent.Channel{Name: "synthetic"}, Outbound: out}
	processor := &PersistentOutboundTransformer{wrapped: out, state: &PersistenceState{ChannelModelsCandidates: []*ChannelModelsCandidate{{Channel: ch, Models: []biz.ChannelModelEntry{{RequestModel: "model", ActualModel: "model"}}, APIFormat: llm.APIFormatOpenAIResponse.String()}}}}
	req := &llm.Request{ProviderExtensions: &llm.ProviderExtensions{OpenAIResponses: &llm.OpenAIResponsesProviderExtensions{Request: &llm.OpenAIResponsesRequestExtensions{RawTools: []llm.OpenAIResponsesRawFragment{{Type: "file_search", Raw: json.RawMessage(`{"type":"file_search"}`)}}}}}}
	_, err := processor.TransformRequest(t.Context(), req)
	require.ErrorIs(t, err, transformer.ErrUnsupportedConversion)
	require.ErrorContains(t, err, "file_search")
}

func TestConversionRequirementsSelectJSONModeEndpoint(t *testing.T) {
	req := &llm.Request{RequestType: llm.RequestTypeChat, ResponseFormat: &llm.ResponseFormat{Type: "json_object"}}
	endpoints := []objects.ChannelEndpoint{{APIFormat: llm.APIFormatAnthropicMessage.String()}, {APIFormat: llm.APIFormatOllamaChat.String()}, {APIFormat: llm.APIFormatOpenAIResponse.String()}}
	require.Equal(t, llm.APIFormatOpenAIResponse.String(), SelectAPIFormat(endpoints, req))
	require.Empty(t, SelectAPIFormat(endpoints[:2], req))
}

func TestConversionRequirementsPreserveResponsesTransportPreference(t *testing.T) {
	req, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: []byte(`{"model":"test","input":"hello","conversation":"conv_1"}`)})
	require.NoError(t, err)
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket} {
		t.Run(format.String(), func(t *testing.T) {
			req.APIFormat = format
			endpoints := []objects.ChannelEndpoint{{APIFormat: compatibleResponsesAPIFormat(format)}, {APIFormat: format.String()}}
			require.Equal(t, format.String(), SelectAPIFormat(endpoints, req))
			require.Equal(t, format.String(), selectForcedAPIFormat(endpoints, req))
			require.Equal(t, endpoints[0].APIFormat, SelectAPIFormat(endpoints[:1], req))
		})
	}
}
