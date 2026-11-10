package orchestrator

import (
	"context"
	"net/http"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
)

func testHTTPStream(events []*httpclient.StreamEvent) streams.Stream[*httpclient.StreamEvent] {
	return streams.SliceStream(events)
}

// === captureRawProviderResponse tests ===

func TestCaptureRawProviderResponse_StoresResponse(t *testing.T) {
	ctx := context.Background()
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "test",
					Settings: &objects.ChannelSettings{
						PassThroughBody: lo.ToPtr(true),
					},
				},
			},
		},
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	mw := captureRawProviderResponse(outbound)
	resp := &httpclient.Response{StatusCode: 200, Body: []byte("ok")}

	result, err := mw.OnOutboundRawResponse(ctx, resp)
	require.NoError(t, err)
	assert.Equal(t, resp, result)
	assert.Equal(t, resp, state.RawProviderResponse)
}

// === applyPassThroughResponse tests ===

func TestApplyPassThroughResponse_Disabled(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{ID: 1, Name: "test"},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	mw := applyPassThroughResponse(outbound)
	transformed := &httpclient.Response{StatusCode: 200, Body: []byte("transformed")}
	state.RawProviderResponse = &httpclient.Response{StatusCode: 200, Body: []byte("raw")}

	result, err := mw.OnInboundRawResponse(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, transformed, result)
}

func TestApplyPassThroughResponse_Enabled_ReturnsRaw(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: nil,
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatOpenAIChatCompletion},
		state:   state,
	}

	mw := applyPassThroughResponse(outbound)
	transformed := &httpclient.Response{StatusCode: 200, Body: []byte("transformed")}
	rawResp := &httpclient.Response{
		StatusCode: 200,
		Body:       []byte("raw"),
	}
	state.RawProviderResponse = rawResp

	result, err := mw.OnInboundRawResponse(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, rawResp, result)
}

func TestApplyPassThroughResponse_ResponsePolicy(t *testing.T) {
	tests := []struct {
		name       string
		allow      bool
		expectBody string
	}{
		{name: "allowed restores raw response", allow: true, expectBody: "raw"},
		{name: "vetoed keeps transformed response", allow: false, expectBody: "transformed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "test",
					Settings: &objects.ChannelSettings{
						PassThroughBody: lo.ToPtr(true),
					},
				},
			}
			llmReq := &llm.Request{
				APIFormat: llm.APIFormatTypeSafeSystemOne,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatTypeSafeSystemOne),
					Body:      []byte(`{"model":"@cf/cloudflare/clef","questions":{}}`),
				},
			}
			rawProviderReq := &httpclient.Request{
				APIFormat: string(llm.APIFormatTypeSafeSystemOne),
			}
			state := &PersistenceState{
				CurrentCandidate:   &ChannelModelsCandidate{Channel: channel},
				LlmRequest:         llmReq,
				RawProviderRequest: rawProviderReq,
				RawProviderResponse: &httpclient.Response{
					StatusCode: http.StatusOK,
					Body:       []byte("raw"),
				},
			}
			policy := &passthroughResponsePolicyOutbound{
				passthroughOutbound: passthroughOutbound{format: llm.APIFormatTypeSafeSystemOne},
				allow:               tt.allow,
			}
			outbound := &PersistentOutboundTransformer{
				wrapped: policy,
				state:   state,
			}

			transformed := &httpclient.Response{StatusCode: http.StatusOK, Body: []byte("transformed")}
			result, err := applyPassThroughResponse(outbound).OnInboundRawResponse(ctx, transformed)
			require.NoError(t, err)
			assert.Equal(t, tt.expectBody, string(result.Body))
			assert.Same(t, llmReq, policy.gotLlmReq)
			assert.Same(t, rawProviderReq, policy.gotProviderReq)
		})
	}
}

func TestApplyPassThroughResponse_MismatchedAPIFormat(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest:       &llm.Request{APIFormat: llm.APIFormatOpenAIChatCompletion},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatAnthropicMessage),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatOpenAIChatCompletion},
		state:   state,
	}

	mw := applyPassThroughResponse(outbound)
	transformed := &httpclient.Response{StatusCode: 200, Body: []byte("transformed")}
	rawResp := &httpclient.Response{
		StatusCode: 200,
		Body:       []byte("raw"),
	}
	state.RawProviderResponse = rawResp

	result, err := mw.OnInboundRawResponse(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, transformed, result, "return transformed when formats mismatch")
}

func TestApplyPassThroughResponse_NilLlmRequest(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	mw := applyPassThroughResponse(outbound)
	transformed := &httpclient.Response{StatusCode: 200, Body: []byte("transformed")}
	state.RawProviderResponse = &httpclient.Response{
		StatusCode: 200,
		Body:       []byte("raw"),
	}

	result, err := mw.OnInboundRawResponse(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, transformed, result)
}

func TestApplyPassThroughResponse_UsesRawProviderRequestAPIFormat(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: nil,
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{apiFormat: llm.APIFormatAnthropicMessage},
		state:   state,
	}

	mw := applyPassThroughResponse(outbound)
	transformed := &httpclient.Response{StatusCode: 200, Body: []byte("transformed")}
	rawResp := &httpclient.Response{
		StatusCode: 200,
		Body:       []byte("raw"),
		Request:    &httpclient.Request{APIFormat: string(llm.APIFormatAnthropicMessage)},
	}
	state.RawProviderResponse = rawResp

	result, err := mw.OnInboundRawResponse(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, rawResp, result)
}

func TestResponsesCrossTransportBodyPreservation(t *testing.T) {
	tests := []struct {
		name   string
		source llm.APIFormat
		target llm.APIFormat
	}{
		{name: "http to http", source: llm.APIFormatOpenAIResponse, target: llm.APIFormatOpenAIResponse},
		{name: "websocket to websocket", source: llm.APIFormatOpenAIResponseWebSocket, target: llm.APIFormatOpenAIResponseWebSocket},
		{name: "websocket to http", source: llm.APIFormatOpenAIResponseWebSocket, target: llm.APIFormatOpenAIResponse},
		{name: "http to websocket", source: llm.APIFormatOpenAIResponse, target: llm.APIFormatOpenAIResponseWebSocket},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &biz.Channel{Channel: &ent.Channel{
				ID:       1,
				Name:     "responses",
				Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(true)},
			}}
			state := &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest: &llm.Request{
					APIFormat:  tt.source,
					Model:      "mapped-model",
					RawRequest: &httpclient.Request{APIFormat: tt.source.String(), Body: []byte(`{"model":"client-model","input":[{"type":"function_call_output","call_id":"call_provider","output":"tool-value","item_id":"item_provider"}],"provider_canary":"keep"}`)},
				},
				RawProviderRequest: &httpclient.Request{APIFormat: tt.target.String()},
			}
			outbound := &PersistentOutboundTransformer{state: state}
			request := &httpclient.Request{APIFormat: tt.target.String()}

			body, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(context.Background(), request)
			require.NoError(t, err)
			adapted, err := applyResponsesTransportRequestBody(outbound).OnOutboundRawRequest(context.Background(), body)
			require.NoError(t, err)
			expectedBody := `{"model":"mapped-model","input":[{"type":"function_call_output","call_id":"call_provider","output":"tool-value","item_id":"item_provider"}],"provider_canary":"keep"}`
			if tt.source != tt.target && tt.target == llm.APIFormatOpenAIResponseWebSocket {
				expectedBody = `{"model":"mapped-model","input":[{"type":"function_call_output","call_id":"call_provider","output":"tool-value","item_id":"item_provider"}],"provider_canary":"keep","type":"response.create"}`
			}
			require.JSONEq(t, expectedBody,
				string(adapted.Body))

			providerResponse := &httpclient.Response{Body: []byte(`{"id":"resp_provider","status":"completed","output":[{"id":"item_provider","type":"message","provider_canary":"keep"}]}`)}
			_, err = captureRawProviderResponse(outbound).OnOutboundRawResponse(context.Background(), providerResponse)
			require.NoError(t, err)
			clientResponse, err := applyPassThroughResponse(outbound).OnInboundRawResponse(context.Background(), &httpclient.Response{Body: []byte(`{"id":"converted"}`)})
			require.NoError(t, err)
			require.JSONEq(t, string(providerResponse.Body), string(clientResponse.Body))
		})
	}
}

func TestResponsesCrossTransportBodySkipsCodexBlockedPassThroughFields(t *testing.T) {
	codexOutbound, err := codex.NewOutboundTransformer(codex.Params{
		BaseURL:       "https://chatgpt.com/backend-api/codex",
		TokenProvider: oauth.NewStaticTokenProvider(&oauth.OAuthCredentials{AccessToken: "test-token"}),
	})
	require.NoError(t, err)
	state := &PersistenceState{
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIResponseWebSocket,
			Model:     "mapped-model",
			RawRequest: &httpclient.Request{
				APIFormat: llm.APIFormatOpenAIResponseWebSocket.String(),
				Body:      []byte(`{"model":"client-model","max_output_tokens":32,"input":"hello"}`),
			},
		},
		RawProviderRequest: &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String()},
	}
	outbound := &PersistentOutboundTransformer{state: state, wrapped: codexOutbound}
	request := &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String(), Body: []byte(`{"model":"mapped-model","input":"hello"}`)}

	result, err := applyResponsesTransportRequestBody(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.Same(t, request, result)
	require.JSONEq(t, `{"model":"mapped-model","input":"hello"}`, string(result.Body))
}

func TestPassThroughRequestHeadersSupportsResponsesWebSocket(t *testing.T) {
	state := &PersistenceState{
		PassThroughApplied: true,
		LlmRequest: &llm.Request{
			APIFormat:  llm.APIFormatOpenAIResponseWebSocket,
			RawRequest: &httpclient.Request{Headers: http.Header{"X-Codex-Turn-Metadata": {"metadata"}}},
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}
	result, err := applyPassThroughRequestHeaders(outbound).OnOutboundRawRequest(context.Background(), &httpclient.Request{Headers: make(http.Header)})
	require.NoError(t, err)
	require.Equal(t, []string{"metadata"}, result.Headers.Values("X-Codex-Turn-Metadata"))
}

func TestApplyPassThroughResponse_PreservesIdentityAndBackMapsModel(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{
		ID:       1,
		Name:     "responses",
		Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(true)},
	}}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIResponse,
			Model:     "mapped-model",
			RawRequest: &httpclient.Request{
				APIFormat: llm.APIFormatOpenAIResponse.String(),
				Body:      []byte(`{"model":"client-model","input":"hello"}`),
			},
		},
		RawProviderRequest: &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String()},
	}
	outbound := &PersistentOutboundTransformer{state: state}
	providerResponse := &httpclient.Response{Body: []byte(`{"id":"provider-response","model":"mapped-model","status":"completed","vendor_unknown":{"keep":true}}`)}
	state.RawProviderResponse = providerResponse

	result, err := applyPassThroughResponse(outbound).OnInboundRawResponse(
		context.Background(),
		&httpclient.Response{Body: []byte(`{"id":"converted"}`)},
	)

	require.NoError(t, err)
	require.JSONEq(t, `{"id":"provider-response","model":"client-model","status":"completed","vendor_unknown":{"keep":true}}`, string(result.Body))
}

func TestApplyPassThroughResponse_CrossFormatBackMapsModel(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{
		ID:       1,
		Name:     "responses",
		Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(true)},
	}}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIResponse,
			Model:     "mapped-model",
			RawRequest: &httpclient.Request{
				APIFormat: llm.APIFormatOpenAIResponse.String(),
				Body:      []byte(`{"model":"client-model","input":"hello"}`),
			},
		},
		RawProviderRequest: &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponseWebSocket.String()},
	}
	outbound := &PersistentOutboundTransformer{state: state}
	state.RawProviderResponse = &httpclient.Response{Body: []byte(`{"id":"provider-response","model":"mapped-model","status":"completed","vendor_unknown":{"keep":true}}`)}

	result, err := applyPassThroughResponse(outbound).OnInboundRawResponse(
		context.Background(),
		&httpclient.Response{Body: []byte(`{"id":"converted"}`)},
	)

	require.NoError(t, err)
	require.JSONEq(t, `{"id":"provider-response","model":"client-model","status":"completed","vendor_unknown":{"keep":true}}`, string(result.Body))
}

func TestApplyPassThroughResponse_RestoresModelFromJSONBody(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{
		ID:       1,
		Name:     "responses",
		Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(true)},
	}}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIResponse,
			Model:     "mapped-model",
			RawRequest: &httpclient.Request{
				APIFormat: llm.APIFormatOpenAIResponse.String(),
				JSONBody:  []byte(`{"model":"client-json-model","input":"hello"}`),
			},
		},
		RawProviderRequest: &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String()},
	}
	outbound := &PersistentOutboundTransformer{state: state}
	state.RawProviderResponse = &httpclient.Response{Body: []byte(`{"id":"provider-response","model":"mapped-model","status":"completed"}`)}

	result, err := applyPassThroughResponse(outbound).OnInboundRawResponse(
		context.Background(),
		&httpclient.Response{Body: []byte(`{"id":"converted"}`)},
	)

	require.NoError(t, err)
	require.JSONEq(t, `{"id":"provider-response","model":"client-json-model","status":"completed"}`, string(result.Body))
}

func TestApplyPassThroughStream_PreservesIdentityAndBackMapsModel(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{
		ID:       1,
		Name:     "responses",
		Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(true)},
	}}
	event := &httpclient.StreamEvent{Data: []byte(`{"type":"response.completed","response":{"id":"provider-response","model":"mapped-model","vendor_unknown":{"keep":true}}}`)}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(true),
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIResponse,
			Model:     "mapped-model",
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: llm.APIFormatOpenAIResponse.String(),
				Body:      []byte(`{"model":"client-model","stream":true,"input":"hello"}`),
			},
		},
		RawProviderRequest: &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String()},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	captured, err := captureRawProviderStream(outbound).OnOutboundRawStream(context.Background(), testHTTPStream([]*httpclient.StreamEvent{event}))
	require.NoError(t, err)
	result, err := applyPassThroughStream(outbound).OnInboundRawStream(context.Background(), captured)
	require.NoError(t, err)
	require.True(t, result.Next())
	require.JSONEq(t, `{"type":"response.completed","response":{"id":"provider-response","model":"client-model","vendor_unknown":{"keep":true}}}`, string(result.Current().Data))
	require.False(t, result.Next())
}

func TestApplyPassThroughStream_TransformsPreReadAndLiveEvents(t *testing.T) {
	for _, tt := range []struct {
		name       string
		source     llm.APIFormat
		target     llm.APIFormat
		clientBody string
		provider   string
		wantTypes  []string
		wantModels []string
	}{
		{
			name:       "same format restores client model",
			source:     llm.APIFormatOpenAIResponse,
			target:     llm.APIFormatOpenAIResponse,
			clientBody: `{"model":"client-model"}`,
			provider:   `{"type":"response.created","response":{"id":"held","model":"mapped-model"}}`,
			wantTypes:  []string{"response.created", "response.completed"},
			wantModels: []string{"client-model", "client-model"},
		},
		{
			name:       "cross format adapts held and attached events",
			source:     llm.APIFormatOpenAIResponse,
			target:     llm.APIFormatOpenAIResponseWebSocket,
			clientBody: `{"model":"client-model"}`,
			provider:   `{"type":"response.created","response":{"id":"held","model":"mapped-model"}}`,
			wantTypes:  []string{"response.created", "response.completed"},
			wantModels: []string{"client-model", "client-model"},
		},
		{
			name:       "websocket same format restores client model",
			source:     llm.APIFormatOpenAIResponseWebSocket,
			target:     llm.APIFormatOpenAIResponseWebSocket,
			clientBody: `{"model":"client-model"}`,
			provider:   `{"type":"response.created","response":{"id":"held","model":"mapped-model"}}`,
			wantTypes:  []string{"", ""},
			wantModels: []string{"client-model", "client-model"},
		},
		{
			name:       "http provider to websocket client restores model",
			source:     llm.APIFormatOpenAIResponseWebSocket,
			target:     llm.APIFormatOpenAIResponse,
			clientBody: `{"model":"client-model"}`,
			provider:   `{"type":"response.created","response":{"id":"held","model":"mapped-model"}}`,
			wantTypes:  []string{"response.created", "response.completed"},
			wantModels: []string{"client-model", "client-model"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			channel := &biz.Channel{Channel: &ent.Channel{
				ID:       1,
				Name:     "responses",
				Settings: &objects.ChannelSettings{PassThroughBody: lo.ToPtr(true)},
			}}
			state := &PersistenceState{
				CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
				OriginalRequestStream: lo.ToPtr(true),
				LlmRequest: &llm.Request{
					APIFormat: tt.source,
					Model:     "mapped-model",
					Stream:    lo.ToPtr(true),
					RawRequest: &httpclient.Request{
						APIFormat: tt.source.String(),
						Body:      []byte(tt.clientBody),
					},
				},
				RawProviderRequest: &httpclient.Request{APIFormat: tt.target.String()},
			}
			outbound := &PersistentOutboundTransformer{state: state}
			events := []*httpclient.StreamEvent{
				{Data: []byte(tt.provider), LastEventID: "held-id"},
				{Data: []byte(`{"type":"response.completed","response":{"id":"attached","model":"mapped-model","status":"completed","output":[{"id":"provider-item","vendor_unknown":{"keep":true}}]},"vendor_unknown":{"keep":true}}`), LastEventID: "attached-id"},
			}
			if tt.source == tt.target {
				for i, event := range events {
					event.Type = tt.wantTypes[i]
				}
			}
			ctx := t.Context()
			captured, err := captureRawProviderStream(outbound).OnOutboundRawStream(ctx, testHTTPStream(events))
			require.NoError(t, err)
			recorder := state.PassThroughRecorder
			require.NotNil(t, recorder)

			// The pipeline pre-reads the first event before the consumer exists.
			require.True(t, captured.Next())
			preRead := captured.Current()
			require.Same(t, events[0], preRead)
			recorder.mu.Lock()
			heldCount, heldBytes := len(recorder.pending), recorder.bytes
			recorder.mu.Unlock()
			require.Equal(t, 1, heldCount)
			require.Equal(t, rawStreamEventSize(events[0]), heldBytes)

			result, err := applyPassThroughStream(outbound).OnInboundRawStream(ctx, streams.PrependStream(captured, preRead))
			require.NoError(t, err)
			defer result.Close()

			for i := range tt.wantTypes {
				require.True(t, result.Next())
				event := result.Current()
				assert.Equal(t, tt.wantTypes[i], event.Type)
				assert.Equal(t, tt.wantModels[i], gjson.GetBytes(event.Data, "response.model").String())
				require.Equal(t, events[i].LastEventID, event.LastEventID)
				require.Equal(t, gjson.GetBytes(events[i].Data, "response.id").String(), gjson.GetBytes(event.Data, "response.id").String())
				require.Equal(t, "mapped-model", gjson.GetBytes(events[i].Data, "response.model").String())
				require.Same(t, event, result.Current())
				if i == 1 {
					require.Equal(t, "provider-item", gjson.GetBytes(event.Data, "response.output.0.id").String())
					require.True(t, gjson.GetBytes(event.Data, "response.output.0.vendor_unknown.keep").Bool())
					require.True(t, gjson.GetBytes(event.Data, "vendor_unknown.keep").Bool())
				}
			}
			require.False(t, result.Next())
			require.NoError(t, result.Err())
			recorder.mu.Lock()
			retainedBytes, retainedCount := recorder.bytes, len(recorder.pending)
			recorder.mu.Unlock()
			require.Zero(t, retainedBytes)
			require.Zero(t, retainedCount)
		})
	}
}

func TestIsPassThroughEnabled_DisablesWhenSupportedStreamParameterChanges(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"my-alias","stream":false,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	assert.False(t, outbound.isPassThroughEnabled())
}

func TestIsPassThroughEnabled_DisablesWhenSupportedStreamParameterMissingButStreamingRequested(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"my-alias","messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	assert.False(t, outbound.isPassThroughEnabled())
}

func TestIsPassThroughEnabled_AllowsNilAndFalseStreamToAlign(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: nil,
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(false),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"my-alias","stream":false,"messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	assert.True(t, outbound.isPassThroughEnabled())
}

func TestIsPassThroughEnabled_DisablesWhenRequestStreamSemanticsDoNotMatchCurrentRequirement(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatGeminiContents,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatGeminiContents),
				Body:      []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatGeminiContents),
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	assert.False(t, outbound.isPassThroughEnabled())
}

func TestIsPassThroughEnabled_DisablesWhenOriginalRequestWasNonStreamingButExecutionIsForcedStreaming(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	state := &PersistenceState{
		CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
		OriginalRequestStream: lo.ToPtr(false),
		LlmRequest: &llm.Request{
			APIFormat: llm.APIFormatOpenAIChatCompletion,
			Stream:    lo.ToPtr(true),
			RawRequest: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIChatCompletion),
				Body:      []byte(`{"model":"my-alias","messages":[{"role":"user","content":"hi"}]}`),
			},
		},
		RawProviderRequest: &httpclient.Request{
			APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	assert.False(t, outbound.isPassThroughEnabled())
}

func TestApplyPassThroughResponse_NilSettings(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:       1,
			Name:     "test",
			Settings: nil,
		},
	}
	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
	}
	outbound := &PersistentOutboundTransformer{state: state}

	mw := applyPassThroughResponse(outbound)
	transformed := &httpclient.Response{StatusCode: 200, Body: []byte("transformed")}

	result, err := mw.OnInboundRawResponse(ctx, transformed)
	require.NoError(t, err)
	assert.Equal(t, transformed, result)
}

func TestApplyPassThroughBodyPreservesMappedModel(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-model-mapping",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				Model:     "gpt-4o",
				APIFormat: llm.APIFormatOpenAIChatCompletion,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatOpenAIChatCompletion),
					Body:      []byte(`{"model":"my-alias","messages":[{"role":"user","content":"hi"}],"temperature":0.4}`),
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		Body:      []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, outbound.state.PassThroughApplied)
	require.Equal(t, "gpt-4o", gjson.GetBytes(processed.Body, "model").String())
	require.Equal(t, 0.4, gjson.GetBytes(processed.Body, "temperature").Float())
	require.Equal(t, "my-alias", gjson.GetBytes(outbound.state.LlmRequest.RawRequest.Body, "model").String())

	processed.Body[0] = '['
	require.Equal(t, `{"model":"my-alias","messages":[{"role":"user","content":"hi"}],"temperature":0.4}`, string(outbound.state.LlmRequest.RawRequest.Body))
}

func TestOpenAIJSONRequestBodyRoutingContracts(t *testing.T) {
	formats := []llm.APIFormat{
		llm.APIFormatOpenAIChatCompletion,
		llm.APIFormatOpenAICompletion,
		llm.APIFormatOpenAIResponse,
		llm.APIFormatOpenAIResponseCompact,
		llm.APIFormatOpenAIEmbedding,
		llm.APIFormatOpenAIModeration,
		llm.APIFormatOpenAIAlphaSearch,
		llm.APIFormatOpenAIImageGeneration,
		llm.APIFormatOpenAIVideo,
		llm.APIFormatOpenAISpeech,
	}

	for _, format := range formats {
		t.Run(format.String()+" pass-through", func(t *testing.T) {
			channel := &biz.Channel{Channel: &ent.Channel{
				ID:   1,
				Name: "openai-pass-through",
				Settings: &objects.ChannelSettings{
					PassThroughBody: lo.ToPtr(true),
				},
			}}
			outbound := &PersistentOutboundTransformer{state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest: &llm.Request{
					Model:     "provider-model",
					APIFormat: format,
					RawRequest: &httpclient.Request{
						APIFormat: format.String(),
						Body:      []byte(`{"model":"client-alias","official_future_field":{"enabled":true}}`),
					},
				},
			}}
			transformed := &httpclient.Request{
				APIFormat: format.String(),
				Body:      []byte(`{"model":"provider-model","converted_only":true}`),
			}

			processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(t.Context(), transformed)
			require.NoError(t, err)
			require.True(t, outbound.state.PassThroughApplied)
			require.Equal(t, "provider-model", gjson.GetBytes(processed.Body, "model").String())
			require.True(t, gjson.GetBytes(processed.Body, "official_future_field.enabled").Bool())
			require.False(t, gjson.GetBytes(processed.Body, "converted_only").Exists())
		})

		t.Run(format.String()+" conversion", func(t *testing.T) {
			channel := &biz.Channel{Channel: &ent.Channel{
				ID:   1,
				Name: "openai-conversion",
				Settings: &objects.ChannelSettings{
					PassThroughBody: lo.ToPtr(true),
				},
			}}
			outbound := &PersistentOutboundTransformer{state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
				LlmRequest: &llm.Request{
					Model:     "provider-model",
					APIFormat: format,
					RawRequest: &httpclient.Request{
						APIFormat: format.String(),
						Body:      []byte(`{"model":"client-alias","original_only":true}`),
					},
				},
			}}
			transformed := &httpclient.Request{
				APIFormat: llm.APIFormatAnthropicMessage.String(),
				Body:      []byte(`{"model":"provider-model","converted_only":true}`),
			}

			processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(t.Context(), transformed)
			require.NoError(t, err)
			require.False(t, outbound.state.PassThroughApplied)
			require.Equal(t, transformed, processed)
			require.True(t, gjson.GetBytes(processed.Body, "converted_only").Bool())
			require.False(t, gjson.GetBytes(processed.Body, "original_only").Exists())
		})
	}
}

func TestOpenAIResponseBodyRoutingContracts(t *testing.T) {
	formats := []llm.APIFormat{
		llm.APIFormatOpenAIChatCompletion,
		llm.APIFormatOpenAICompletion,
		llm.APIFormatOpenAIResponse,
		llm.APIFormatOpenAIResponseCompact,
		llm.APIFormatOpenAIEmbedding,
		llm.APIFormatOpenAIModeration,
		llm.APIFormatOpenAIAlphaSearch,
		llm.APIFormatOpenAIImageGeneration,
		llm.APIFormatOpenAIImageEdit,
		llm.APIFormatOpenAIVideo,
		llm.APIFormatOpenAISpeech,
		llm.APIFormatOpenAITranscription,
		llm.APIFormatOpenAITranslation,
	}

	for _, format := range formats {
		t.Run(format.String()+" pass-through", func(t *testing.T) {
			channel := &biz.Channel{Channel: &ent.Channel{
				ID:   1,
				Name: "openai-response-pass-through",
				Settings: &objects.ChannelSettings{
					PassThroughBody: lo.ToPtr(true),
				},
			}}
			raw := &httpclient.Response{StatusCode: http.StatusOK, Body: []byte(`{"raw":true}`)}
			outbound := &PersistentOutboundTransformer{state: &PersistenceState{
				CurrentCandidate:    &ChannelModelsCandidate{Channel: channel},
				LlmRequest:          &llm.Request{APIFormat: format},
				RawProviderRequest:  &httpclient.Request{APIFormat: format.String()},
				RawProviderResponse: raw,
			}}
			transformed := &httpclient.Response{StatusCode: http.StatusOK, Body: []byte(`{"transformed":true}`)}

			processed, err := applyPassThroughResponse(outbound).OnInboundRawResponse(t.Context(), transformed)
			require.NoError(t, err)
			require.Equal(t, raw, processed)
		})

		t.Run(format.String()+" conversion", func(t *testing.T) {
			channel := &biz.Channel{Channel: &ent.Channel{
				ID:   1,
				Name: "openai-response-conversion",
				Settings: &objects.ChannelSettings{
					PassThroughBody: lo.ToPtr(true),
				},
			}}
			outbound := &PersistentOutboundTransformer{state: &PersistenceState{
				CurrentCandidate:   &ChannelModelsCandidate{Channel: channel},
				LlmRequest:         &llm.Request{APIFormat: format},
				RawProviderRequest: &httpclient.Request{APIFormat: llm.APIFormatAnthropicMessage.String()},
				RawProviderResponse: &httpclient.Response{
					StatusCode: http.StatusOK,
					Body:       []byte(`{"raw":true}`),
				},
			}}
			transformed := &httpclient.Response{StatusCode: http.StatusOK, Body: []byte(`{"transformed":true}`)}

			processed, err := applyPassThroughResponse(outbound).OnInboundRawResponse(t.Context(), transformed)
			require.NoError(t, err)
			require.Equal(t, transformed, processed)
		})
	}
}

func TestApplyPassThroughBodySkipsWhenOutboundPolicyRejects(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "policy-pass-through",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &passthroughPolicyOutbound{
			passthroughOutbound: passthroughOutbound{format: llm.APIFormatOpenAIResponse},
			allow:               false,
		},
		state: &PersistenceState{
			CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
			OriginalRequestStream: lo.ToPtr(true),
			LlmRequest: &llm.Request{
				Model:     "gpt-5.4-mini",
				APIFormat: llm.APIFormatOpenAIResponse,
				Stream:    lo.ToPtr(true),
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatOpenAIResponse),
					Body:      []byte(`{"model":"gpt-5.4-mini","input":"hi","stream":true,"temperature":0.4}`),
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"model":"gpt-5.4-mini","input":[{"role":"user","content":"hi"}],"stream":true}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.False(t, outbound.state.PassThroughApplied)
	require.Equal(t, request, processed)
	require.False(t, gjson.GetBytes(processed.Body, "temperature").Exists())
}

func TestApplyPassThroughRequestHeaders(t *testing.T) {
	inboundHeaders := http.Header{
		"X-Codex-Turn-Metadata":                  {`{"session_id":"session-123","turn_id":"turn-456"}`},
		"X-Codex-Turn-State":                     {"ts-1"},
		"X-Codex-Window-Id":                      {"window-123"},
		"X-Client-Request-Id":                    {"request-123"},
		"X-Codex-Beta-Features":                  {"js_repl"},
		"Session-Id":                             {"session-123"},
		"Originator":                             {"codex_desktop_rs"},
		"X-Openai-Internal-Codex-Responses-Lite": {"true"},
		"Thread-Id":                              {"thread-123"},
		"Authorization":                          {"Bearer inbound-secret"},
		"Cookie":                                 {"session=inbound-secret"},
		"Host":                                   {"client.example"},
		"Content-Length":                         {"12345"},
		"X-Untrusted-Custom-Header":              {"do-not-forward"},
	}
	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			PassThroughApplied: true,
			LlmRequest: &llm.Request{
				APIFormat:  llm.APIFormatOpenAIResponse,
				RawRequest: &httpclient.Request{Headers: inboundHeaders},
			},
		},
	}
	request := &httpclient.Request{Headers: http.Header{
		"Authorization": {"Bearer provider-secret"},
		"Originator":    {"axonhub"},
	}}

	processed, err := applyPassThroughRequestHeaders(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)

	for _, header := range codexResponsesPassThroughHeaders {
		require.Equal(t, inboundHeaders.Values(header), processed.Headers.Values(header), header)
	}
	require.Equal(t, "Bearer provider-secret", processed.Headers.Get("Authorization"))
	require.Empty(t, processed.Headers.Get("X-Openai-Internal-Codex-Responses-Lite"))
	require.Empty(t, processed.Headers.Get("Cookie"))
	require.Empty(t, processed.Headers.Get("Host"))
	require.Empty(t, processed.Headers.Get("Content-Length"))
	require.Empty(t, processed.Headers.Get("X-Untrusted-Custom-Header"))
}

func TestApplyPassThroughRequestHeadersRequiresResponsesBodyPassThrough(t *testing.T) {
	tests := []struct {
		name               string
		passThroughApplied bool
		apiFormat          llm.APIFormat
	}{
		{name: "body pass-through not applied", apiFormat: llm.APIFormatOpenAIResponse},
		{name: "different API format", passThroughApplied: true, apiFormat: llm.APIFormatOpenAIChatCompletion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outbound := &PersistentOutboundTransformer{
				state: &PersistenceState{
					PassThroughApplied: tt.passThroughApplied,
					LlmRequest: &llm.Request{
						APIFormat: tt.apiFormat,
						RawRequest: &httpclient.Request{Headers: http.Header{
							"X-Codex-Turn-Metadata": {"must-not-forward"},
						}},
					},
				},
			}
			request := &httpclient.Request{Headers: make(http.Header)}

			processed, err := applyPassThroughRequestHeaders(outbound).OnOutboundRawRequest(context.Background(), request)
			require.NoError(t, err)
			require.Empty(t, processed.Headers.Get("X-Codex-Turn-Metadata"))
		})
	}
}

// TestApplyPassThroughBodyMasksPromptProtectedOpenAIContent verifies that pass-through
// retains the client's JSON structure while applying prompt-protection replacements.
func TestApplyPassThroughBodyMasksPromptProtectedOpenAIContent(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-prompt-protection",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	rule := &ent.PromptProtectionRule{
		Name:    "mask-secret",
		Pattern: "secret-[0-9]+",
		Settings: &objects.PromptProtectionSettings{
			Action:      objects.PromptProtectionActionMask,
			Replacement: "[MASKED]",
			Scopes:      []objects.PromptProtectionScope{objects.PromptProtectionScopeUser},
		},
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate:          &ChannelModelsCandidate{Channel: channel},
			PromptProtectionMaskRules: []*ent.PromptProtectionRule{rule},
			LlmRequest: &llm.Request{
				Model:     "gpt-4o",
				APIFormat: llm.APIFormatOpenAIChatCompletion,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatOpenAIChatCompletion),
					Body:      []byte(`{"model":"my-alias","messages":[{"role":"system","content":"secret-001"},{"role":"user","content":"secret-002"},{"role":"user","content":[{"type":"text","text":"secret-003"},{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}],"provider_option":{"preserve":true}}`),
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		Body:      []byte(`{"model":"gpt-4o","messages":[]}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", gjson.GetBytes(processed.Body, "model").String())
	assert.Equal(t, "secret-001", gjson.GetBytes(processed.Body, "messages.0.content").String())
	assert.Equal(t, "[MASKED]", gjson.GetBytes(processed.Body, "messages.1.content").String())
	assert.Equal(t, "[MASKED]", gjson.GetBytes(processed.Body, "messages.2.content.0.text").String())
	assert.Equal(t, "https://example.com/image.png", gjson.GetBytes(processed.Body, "messages.2.content.1.image_url.url").String())
	assert.True(t, gjson.GetBytes(processed.Body, "provider_option.preserve").Bool())
}

// TestApplyPassThroughBodyKeepsProtectedBodyForUnsupportedPromptLayout verifies
// that an unknown raw JSON layout cannot cause protected text to be replayed.
func TestApplyPassThroughBodyKeepsProtectedBodyForUnsupportedPromptLayout(t *testing.T) {
	ctx := context.Background()
	format := llm.APIFormatAiSDKText
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-prompt-protection-fallback",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}
	rule := &ent.PromptProtectionRule{
		Name:    "mask-secret",
		Pattern: "secret",
		Settings: &objects.PromptProtectionSettings{
			Action:      objects.PromptProtectionActionMask,
			Replacement: "[MASKED]",
		},
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate:          &ChannelModelsCandidate{Channel: channel},
			PromptProtectionMaskRules: []*ent.PromptProtectionRule{rule},
			LlmRequest: &llm.Request{
				APIFormat: format,
				RawRequest: &httpclient.Request{
					APIFormat: string(format),
					Body:      []byte(`{"messages":[{"role":"user","content":"secret"}]}`),
				},
			},
		},
	}
	request := &httpclient.Request{
		APIFormat: string(format),
		Body:      []byte(`{"messages":[{"role":"user","content":"[MASKED]"}]}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, string(request.Body), string(processed.Body))
	assert.NotContains(t, string(processed.Body), `"content":"secret"`)
}

func TestApplyPassThroughBodyPreservesMappedModelForJinaRerank(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-jina-rerank-model-mapping",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				Model:     "Qwen/Qwen3-Reranker-8B",
				APIFormat: llm.APIFormatJinaRerank,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatJinaRerank),
					Body:      []byte(`{"model":"Qwen-3-Rerank-8B","query":"what is ai","documents":["a","b"],"top_n":2}`),
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatJinaRerank),
		Body:      []byte(`{"model":"Qwen/Qwen3-Reranker-8B","query":"what is ai","documents":["a","b"]}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "Qwen/Qwen3-Reranker-8B", gjson.GetBytes(processed.Body, "model").String())
	require.Equal(t, float64(2), gjson.GetBytes(processed.Body, "top_n").Float())
	require.Equal(t, "Qwen-3-Rerank-8B", gjson.GetBytes(outbound.state.LlmRequest.RawRequest.Body, "model").String())
}

func TestApplyPassThroughBodyPreservesMappedModelForJinaEmbedding(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-jina-embedding-model-mapping",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				Model:     "jina-embeddings-v3",
				APIFormat: llm.APIFormatJinaEmbedding,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatJinaEmbedding),
					Body:      []byte(`{"model":"my-embedding-alias","input":"hello","task":"retrieval.query"}`),
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatJinaEmbedding),
		Body:      []byte(`{"model":"jina-embeddings-v3","input":"hello"}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "jina-embeddings-v3", gjson.GetBytes(processed.Body, "model").String())
	require.Equal(t, "retrieval.query", gjson.GetBytes(processed.Body, "task").String())
	require.Equal(t, "my-embedding-alias", gjson.GetBytes(outbound.state.LlmRequest.RawRequest.Body, "model").String())
}

func TestApplyPassThroughBodySkipsPassThroughWhenSupportedStreamParameterChanges(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-stream-upgrade",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				Model:     "gpt-4o",
				Stream:    lo.ToPtr(true),
				APIFormat: llm.APIFormatOpenAIChatCompletion,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatOpenAIChatCompletion),
					Body:      []byte(`{"model":"my-alias","stream":false,"messages":[{"role":"user","content":"hi"}],"temperature":0.4}`),
				},
			},
		},
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, string(request.Body), string(processed.Body))
	require.Equal(t, "gpt-4o", gjson.GetBytes(processed.Body, "model").String())
	require.True(t, gjson.GetBytes(processed.Body, "stream").Bool())
	require.False(t, gjson.GetBytes(processed.Body, "temperature").Exists())
}

func TestApplyPassThroughBodyPreservesAlignedStreamWithoutPatchingIt(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-aligned-stream",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate:      &ChannelModelsCandidate{Channel: channel},
			OriginalRequestStream: lo.ToPtr(true),
			LlmRequest: &llm.Request{
				Model:     "gpt-4o",
				Stream:    lo.ToPtr(true),
				APIFormat: llm.APIFormatOpenAIChatCompletion,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatOpenAIChatCompletion),
					Body:      []byte(`{"model":"my-alias","stream":true,"messages":[{"role":"user","content":"hi"}],"temperature":0.4}`),
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		Body:      []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", gjson.GetBytes(processed.Body, "model").String())
	require.True(t, gjson.GetBytes(processed.Body, "stream").Bool())
	require.Equal(t, 0.4, gjson.GetBytes(processed.Body, "temperature").Float())
}

func TestMergePassThroughBodySkipsFormatsWithoutTopLevelModel(t *testing.T) {
	rawBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)

	merged, err := mergePassThroughRequestBody(rawBody, llm.APIFormatGeminiContents, "gemini-2.5-pro")
	require.NoError(t, err)
	require.Equal(t, string(rawBody), string(merged))
}

func TestMergePassThroughBodyPatchesModerationModel(t *testing.T) {
	rawBody := []byte(`{"model":"omni-moderation-latest","input":"hello"}`)

	merged, err := mergePassThroughRequestBody(rawBody, llm.APIFormatOpenAIModeration, "provider-moderation-v1")
	require.NoError(t, err)
	require.Equal(t, "provider-moderation-v1", gjson.GetBytes(merged, "model").String())
	require.Equal(t, "hello", gjson.GetBytes(merged, "input").String())
}

func TestMergePassThroughBodyPatchesDecisionsModel(t *testing.T) {
	rawBody := []byte(`{"model":"client-model","input":"context","x_beta":{"choice":true}}`)

	merged, err := mergePassThroughRequestBody(rawBody, llm.APIFormatOpenAIDecisions, "provider-model")
	require.NoError(t, err)
	require.Equal(t, "provider-model", gjson.GetBytes(merged, "model").String())
	require.Equal(t, "context", gjson.GetBytes(merged, "input").String())
	require.True(t, gjson.GetBytes(merged, "x_beta.choice").Bool())
}

// TestApplyUserAgentPassThrough tests the User-Agent pass-through middleware.
func TestApplyUserAgentPassThrough(t *testing.T) {
	tests := []struct {
		name             string
		channelUASetting *bool // Channel-level override
		globalUAEnabled  bool  // System-level setting
		clientUA         string
		outboundUA       string // User-Agent already set by the outbound transformer
		wantUAHeader     string
	}{
		{
			name:             "channel_disabled_ignores_global",
			channelUASetting: new(false),
			globalUAEnabled:  true,
			clientUA:         "Client/1.0",
			wantUAHeader:     "axonhub/1.0", // Pass-through disabled: middleware sets default UA
		},
		{
			name:             "channel_enabled_ignores_global",
			channelUASetting: new(true),
			globalUAEnabled:  false,
			clientUA:         "Client/1.0",
			wantUAHeader:     "Client/1.0",
		},
		{
			name:             "channel_nil_inherits_global_disabled",
			channelUASetting: nil,
			globalUAEnabled:  false,
			clientUA:         "Client/1.0",
			wantUAHeader:     "axonhub/1.0", // Pass-through disabled: middleware sets default UA
		},
		{
			name:             "channel_nil_inherits_global_enabled",
			channelUASetting: nil,
			globalUAEnabled:  true,
			clientUA:         "Client/1.0",
			wantUAHeader:     "Client/1.0",
		},
		{
			name:             "enabled_but_no_client_ua",
			channelUASetting: new(true),
			globalUAEnabled:  true,
			clientUA:         "",
			wantUAHeader:     "",
		},
		{
			// Issue #2303: provider-required UA set by the outbound transformer
			// (e.g. GitHubCopilotChat for Copilot channels) must survive when
			// pass-through is disabled; otherwise real upstream calls go out
			// with the axonhub default while channel checks use the correct UA.
			name:             "disabled_preserves_transformer_set_ua",
			channelUASetting: nil,
			globalUAEnabled:  false,
			clientUA:         "",
			outboundUA:       "GitHubCopilotChat/0.26.7",
			wantUAHeader:     "GitHubCopilotChat/0.26.7",
		},
		{
			name:             "enabled_still_overrides_transformer_set_ua",
			channelUASetting: new(true),
			globalUAEnabled:  false,
			clientUA:         "Client/1.0",
			outboundUA:       "GitHubCopilotChat/0.26.7",
			wantUAHeader:     "Client/1.0",
		},
		{
			// Issue #2635: with the switch off the codex outbound transformer
			// presents a codex-client-like default UA instead of copying the
			// client's. That transformer-owned value must survive here, so the
			// client UA never reaches the upstream. (The literal mirrors the
			// unexported codexDefaultUserAgent in the codex transformer.)
			name:             "disabled_preserves_codex_transformer_default_ua",
			channelUASetting: nil,
			globalUAEnabled:  false,
			clientUA:         "ZCode/3.14.4 (darwin arm64)",
			outboundUA:       "codex_cli_rs/0.159.0",
			wantUAHeader:     "codex_cli_rs/0.159.0",
		},
		{
			// Issue #2635: with the switch on the client UA overrides the codex
			// transformer default, which is what makes the switch effective.
			name:             "enabled_overrides_codex_transformer_default_ua",
			channelUASetting: new(true),
			globalUAEnabled:  false,
			clientUA:         "ZCode/3.14.4 (darwin arm64)",
			outboundUA:       "codex_cli_rs/0.159.0",
			wantUAHeader:     "ZCode/3.14.4 (darwin arm64)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create mock channel with optional pass-through setting
			channelSettings := &objects.ChannelSettings{}
			if tt.channelUASetting != nil {
				channelSettings.PassThroughUserAgent = tt.channelUASetting
			}

			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:       1,
					Name:     "test-channel",
					Settings: channelSettings,
				},
				Outbound: &mockTransformer{},
			}

			// Create raw request with client UA - RawRequest is *httpclient.Request in llm.Request
			rawHeaders := make(http.Header)
			if tt.clientUA != "" {
				rawHeaders.Set("User-Agent", tt.clientUA)
			}

			llmRequest := &llm.Request{
				Model: "gpt-4",
				RawRequest: &httpclient.Request{
					Headers: rawHeaders,
				},
			}

			// Create outbound transformer
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					LlmRequest:       llmRequest,
					Settings:         &RequestSettings{UserAgentPassThrough: tt.globalUAEnabled},
				},
			}

			// Create middleware
			middleware := applyUserAgentPassThrough(outbound)

			// Execute middleware
			rawRequest := &httpclient.Request{
				Headers: make(http.Header),
			}
			if tt.outboundUA != "" {
				rawRequest.Headers.Set("User-Agent", tt.outboundUA)
			}
			processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)

			require.NoError(t, err)
			require.NotNil(t, processedRequest)

			// Verify User-Agent header is set correctly
			if tt.wantUAHeader != "" {
				require.Equal(t, tt.wantUAHeader, processedRequest.Headers.Get("User-Agent"))
			} else {
				// When no User-Agent expected, header should be empty
				require.Empty(t, processedRequest.Headers.Get("User-Agent"))
			}
		})
	}
}

// TestApplyUserAgentPassThrough_NoChannel tests the middleware when no channel is selected.
func TestApplyUserAgentPassThrough_NoChannel(t *testing.T) {
	ctx := context.Background()

	// Create outbound without a channel or settings snapshot
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state:   &PersistenceState{},
	}

	// Create middleware
	middleware := applyUserAgentPassThrough(outbound)

	// Execute middleware
	rawRequest := &httpclient.Request{
		Headers: make(http.Header),
	}
	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)
	require.NotNil(t, processedRequest)
}

func TestApplyPassThroughBodySkipsMultipartFormats(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		apiFormat llm.APIFormat
	}{
		{name: "audio transcription", apiFormat: llm.APIFormatOpenAITranscription},
		{name: "audio translation", apiFormat: llm.APIFormatOpenAITranslation},
		{name: "image edit", apiFormat: llm.APIFormatOpenAIImageEdit},
		{name: "image variation", apiFormat: llm.APIFormatOpenAIImageVariation},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "pass-through-multipart",
					Settings: &objects.ChannelSettings{
						PassThroughBody: lo.ToPtr(true),
					},
				},
			}

			// The inbound multipart body uses the client boundary; the outbound transformer
			// rebuilds the multipart body with a new boundary, so the inbound bytes must not
			// replace the outbound body.
			inboundBody := []byte("--client-boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\noriginal-model\r\n--client-boundary--\r\n")
			outboundBody := []byte("--new-boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nmapped-model\r\n--new-boundary--\r\n")

			outbound := &PersistentOutboundTransformer{
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					LlmRequest: &llm.Request{
						Model:     "mapped-model",
						APIFormat: tt.apiFormat,
						RawRequest: &httpclient.Request{
							APIFormat: string(tt.apiFormat),
							Body:      inboundBody,
						},
					},
				},
			}

			request := &httpclient.Request{
				APIFormat: string(tt.apiFormat),
				Body:      outboundBody,
			}

			processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
			require.NoError(t, err)
			require.Equal(t, outboundBody, processed.Body)
			require.False(t, outbound.state.PassThroughApplied)
		})
	}
}

func TestApplyPassThroughBodySkipsMultipartVideo(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{
		ID:   1,
		Name: "pass-through-multipart-video",
		Settings: &objects.ChannelSettings{
			PassThroughBody: lo.ToPtr(true),
		},
	}}
	inboundBody := []byte("--client-boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nclient-alias\r\n--client-boundary--\r\n")
	outboundBody := []byte("--provider-boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nprovider-model\r\n--provider-boundary--\r\n")
	outbound := &PersistentOutboundTransformer{state: &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
		LlmRequest: &llm.Request{
			Model:     "provider-model",
			APIFormat: llm.APIFormatOpenAIVideo,
			RawRequest: &httpclient.Request{
				APIFormat: llm.APIFormatOpenAIVideo.String(),
				Headers:   http.Header{"Content-Type": []string{"multipart/form-data; boundary=client-boundary"}},
				Body:      inboundBody,
			},
		},
	}}
	request := &httpclient.Request{
		APIFormat: llm.APIFormatOpenAIVideo.String(),
		Headers:   http.Header{"Content-Type": []string{"multipart/form-data; boundary=provider-boundary"}},
		Body:      outboundBody,
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(t.Context(), request)
	require.NoError(t, err)
	require.False(t, outbound.state.PassThroughApplied)
	require.Equal(t, outboundBody, processed.Body)
}

func TestApplyPassThroughBodyAppliesJSONImageEdit(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-json-image-edit",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	inboundBody := []byte(`{"model":"my-edit-alias","prompt":"make it blue","image":"data:image/png;base64,aGk="}`)
	outboundBody := []byte("--new-boundary\r\nContent-Disposition: form-data; name=\"prompt\"\r\n\r\nmake it blue\r\n--new-boundary--\r\n")

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				Model:     "sensenova-u1.5-lite",
				APIFormat: llm.APIFormatOpenAIImageEdit,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatOpenAIImageEdit),
					Headers:   http.Header{"Content-Type": []string{"application/json"}},
					Body:      inboundBody,
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat:   string(llm.APIFormatOpenAIImageEdit),
		Headers:     http.Header{"Content-Type": []string{"multipart/form-data; boundary=new-boundary"}},
		ContentType: "multipart/form-data; boundary=new-boundary",
		Body:        outboundBody,
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, outbound.state.PassThroughApplied)

	// The raw JSON body replaces the rebuilt multipart body, with the mapped model patched in.
	require.Equal(t, "sensenova-u1.5-lite", gjson.GetBytes(processed.Body, "model").String())
	require.Equal(t, "make it blue", gjson.GetBytes(processed.Body, "prompt").String())

	// Content-Type must match the replayed JSON body instead of the rebuilt multipart one.
	require.Equal(t, "application/json", processed.Headers.Get("Content-Type"))
	require.Equal(t, "application/json", processed.ContentType)
}

func TestPassThroughBodySupported_ImageEditContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		supported   bool
	}{
		{name: "json", contentType: "application/json", supported: true},
		{name: "json with charset", contentType: "application/json; charset=utf-8", supported: true},
		{name: "json patch", contentType: "application/json-patch+json", supported: false},
		{name: "invalid", contentType: "application/json; charset", supported: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &llm.Request{
				APIFormat: llm.APIFormatOpenAIImageEdit,
				RawRequest: &httpclient.Request{
					Headers: http.Header{"Content-Type": []string{tt.contentType}},
				},
			}
			require.Equal(t, tt.supported, passThroughBodySupported(req))
		})
	}
}

func TestApplyPassThroughBodyAppliesSpeechModelPatch(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "pass-through-speech",
			Settings: &objects.ChannelSettings{
				PassThroughBody: lo.ToPtr(true),
			},
		},
	}

	outbound := &PersistentOutboundTransformer{
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				Model:     "tts-1-hd",
				APIFormat: llm.APIFormatOpenAISpeech,
				RawRequest: &httpclient.Request{
					APIFormat: string(llm.APIFormatOpenAISpeech),
					Body:      []byte(`{"model":"my-tts-alias","input":"hello","voice":"alloy"}`),
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAISpeech),
		Body:      []byte(`{"model":"tts-1-hd","input":"hello","voice":"alloy"}`),
	}

	processed, err := applyPassThroughRequestBody(outbound, nil).OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "tts-1-hd", gjson.GetBytes(processed.Body, "model").String())
	require.Equal(t, "alloy", gjson.GetBytes(processed.Body, "voice").String())
}

// closeOrderObserver is a minimal DeliveryAcknowledger for delivery-gate tests.
type closeOrderObserver struct {
	acknowledged bool
}

func (o *closeOrderObserver) AcknowledgeDelivery() { o.acknowledged = true }

func (o *closeOrderObserver) DeliveryAcknowledged() bool { return o.acknowledged }

type acknowledgingStream struct {
	streams.Stream[*httpclient.StreamEvent]
	closeOrderObserver
}

func TestWithPassThroughDelivery_AcknowledgesTransformedStream(t *testing.T) {
	// Persistence of the attempt lives on the transformed stream, so a delivered
	// terminal raw event must be acknowledged there.
	transformed := &acknowledgingStream{Stream: testHTTPStream(nil)}
	stream := withPassThroughDelivery(context.Background(), transformed, testHTTPStream(nil))

	boundary, ok := stream.(streams.DeliveryBoundary)
	require.True(t, ok)
	require.NoError(t, boundary.Deliver(context.Background(), func() (bool, error) { return true, nil }))
	assert.True(t, transformed.acknowledged)
	assert.True(t, stream.(streams.DeliveryAcknowledger).DeliveryAcknowledged())
}
