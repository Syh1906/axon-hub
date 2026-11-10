package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestOverrideParametersWithTemplate(t *testing.T) {
	ctx := context.Background()

	// Create test request with some data for template
	llmRequest := &llm.Request{
		Model: "gpt-4",
		Metadata: map[string]string{
			"user_id": "user-123",
		},
		ReasoningEffort: "high",
	}

	// Create mock channel with override parameters using templates
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
			Settings: &objects.ChannelSettings{
				OverrideParameters: `{"custom_field": "model-{{.Model}}", "effort_field": "effort-{{.ReasoningEffort}}", "user_field": "user-{{index .Metadata \"user_id\"}}", "json_field": "{\"id\": \"{{.Model}}\", \"val\": 123}"}`,
				OverrideHeaders: []objects.HeaderEntry{
					{Key: "X-Custom-Model", Value: "header-{{.Model}}"},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	// Create processor
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	// Test Body Override
	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{
		Body: []byte("{}"),
	}

	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(processedRequest.Body)
	require.Equal(t, "model-gpt-4", gjson.Get(bodyStr, "custom_field").String())
	require.Equal(t, "effort-high", gjson.Get(bodyStr, "effort_field").String())
	require.Equal(t, "user-user-123", gjson.Get(bodyStr, "user_field").String())

	// Verify JSON field was correctly parsed and set as object
	jsonField := gjson.Get(bodyStr, "json_field")
	require.True(t, jsonField.IsObject())
	require.Equal(t, "gpt-4", jsonField.Get("id").String())
	require.Equal(t, int64(123), jsonField.Get("val").Int())

	// Test Header Override
	headerMiddleware := applyOverrideRequestHeaders(outbound)
	rawRequestWithHeaders := &httpclient.Request{
		Headers: make(http.Header),
	}

	processedRequestWithHeaders, err := headerMiddleware.OnOutboundRawRequest(ctx, rawRequestWithHeaders)
	require.NoError(t, err)

	require.Equal(t, "header-gpt-4", processedRequestWithHeaders.Headers.Get("X-Custom-Model"))
}

func TestOverrideParametersWithMappedRequestModel(t *testing.T) {
	ctx := context.Background()
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "mapped-model-test",
			Settings: &objects.ChannelSettings{
				BodyOverrideOperations: []objects.OverrideOperation{{
					Op:        "set",
					Path:      "service_tier",
					Value:     "priority",
					Condition: `{{or (eq .RequestModel "gpt-4-fast") (eq .RequestModel "gpt-4-priority")}}`,
				}},
			},
		},
		Outbound: &mockTransformer{},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       &llm.Request{Model: "gpt-4"},
			OriginalModel:    "gpt-4-fast",
		},
	}

	processedRequest, err := applyOverrideRequestBody(outbound).OnOutboundRawRequest(ctx, &httpclient.Request{Body: []byte(`{"model":"gpt-4"}`)})
	require.NoError(t, err)
	require.Equal(t, "priority", gjson.GetBytes(processedRequest.Body, "service_tier").String())
}

func TestOverrideParametersWithRequestHeaderTemplate(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{
		Model: "gpt-4",
		RawRequest: &httpclient.Request{
			Headers: http.Header{
				"X-Trace-Id":       []string{"trace-123"},
				"X-Multi-Value":    []string{"first", "second"},
				"Authorization":    []string{"Bearer secret"},
				"Api-Key":          []string{"secret-api-key"},
				"X-Google-Api-Key": []string{"google-secret"},
			},
		},
	}

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "request-header-template-test",
			Settings: &objects.ChannelSettings{
				OverrideParameters: `{
					"trace_id_lower": "{{index .RequestHeader \"x-trace-id\"}}",
					"trace_id_canonical": "{{index .RequestHeader \"X-Trace-Id\"}}",
					"multi_value": "{{index .RequestHeader \"x-multi-value\"}}",
					"authorization": "{{index .RequestHeader \"authorization\"}}",
					"api_key": "{{index .RequestHeader \"api-key\"}}",
					"x_google_api_key": "{{index .RequestHeader \"x-google-api-key\"}}"
				}`,
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{Body: []byte("{}")}

	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(processedRequest.Body)
	require.Equal(t, "trace-123", gjson.Get(bodyStr, "trace_id_lower").String())
	require.Equal(t, "trace-123", gjson.Get(bodyStr, "trace_id_canonical").String())
	require.Equal(t, "first", gjson.Get(bodyStr, "multi_value").String())
	require.Empty(t, gjson.Get(bodyStr, "authorization").String())
	require.Empty(t, gjson.Get(bodyStr, "api_key").String())
	require.Empty(t, gjson.Get(bodyStr, "x_google_api_key").String())
}

func TestOverrideParametersWithRequestHeaderTemplate_LowercaseSensitiveHeaders(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{
		Model: "gpt-4",
		RawRequest: &httpclient.Request{
			Headers: http.Header{
				"authorization":  []string{"Bearer secret"},
				"api-key":        []string{"secret-api-key"},
				"x-goog-api-key": []string{"goog-secret"},
				"x-trace-id":     []string{"trace-lowercase"},
			},
		},
	}

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "request-header-lowercase-sensitive-test",
			Settings: &objects.ChannelSettings{
				OverrideParameters: `{
					"trace_id": "{{index .RequestHeader \"x-trace-id\"}}",
					"authorization": "{{index .RequestHeader \"authorization\"}}",
					"api_key": "{{index .RequestHeader \"api-key\"}}",
					"x_goog_api_key": "{{index .RequestHeader \"x-goog-api-key\"}}"
				}`,
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{Body: []byte("{}")}

	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(processedRequest.Body)
	require.Equal(t, "trace-lowercase", gjson.Get(bodyStr, "trace_id").String())
	require.Empty(t, gjson.Get(bodyStr, "authorization").String())
	require.Empty(t, gjson.Get(bodyStr, "api_key").String())
	require.Empty(t, gjson.Get(bodyStr, "x_goog_api_key").String())
}

func TestOverrideParametersComplex(t *testing.T) {
	ctx := context.Background()

	// Create test request with data for template
	llmRequest := &llm.Request{
		Model: "gpt-4",
		Metadata: map[string]string{
			"env": "prod",
		},
		ReasoningEffort: "low",
	}

	// Create mock channel with complex override parameters
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "complex-test",
			Settings: &objects.ChannelSettings{
				// Test if/else and nested JSON
				OverrideParameters: `{
					"logic_field": "{{if eq .Model \"gpt-4\"}}is-gpt-4{{else}}not-gpt-4{{end}}",
					"effort_logic": "{{if eq .ReasoningEffort \"high\"}}high-effort{{else}}low-effort{{end}}",
					"json_complex": "{\"array\": [1, 2, \"{{.Model}}\"], \"nested\": {\"key\": \"val\"}}",
					"clear_me": "__AXONHUB_CLEAR__"
				}`,
				OverrideHeaders: []objects.HeaderEntry{
					{Key: "X-Logic-Header", Value: "{{if .Metadata.env}}env-{{.Metadata.env}}{{else}}no-env{{end}}"},
					{Key: "X-Clear-Header", Value: "__AXONHUB_CLEAR__"},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	// Create processor
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	// 1. Test Body Logic & Complex JSON & Clear
	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{
		Body: []byte(`{"clear_me": "to-be-deleted", "keep_me": "stay"}`),
	}

	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(processedRequest.Body)
	require.Equal(t, "is-gpt-4", gjson.Get(bodyStr, "logic_field").String())
	require.Equal(t, "low-effort", gjson.Get(bodyStr, "effort_logic").String())

	// Verify nested JSON and array
	jsonComplex := gjson.Get(bodyStr, "json_complex")
	require.True(t, jsonComplex.IsObject())
	require.Equal(t, "gpt-4", jsonComplex.Get("array.2").String())
	require.Equal(t, "val", jsonComplex.Get("nested.key").String())

	// Verify field was cleared
	require.False(t, gjson.Get(bodyStr, "clear_me").Exists())
	require.Equal(t, "stay", gjson.Get(bodyStr, "keep_me").String())

	// 2. Test Header Clear & Logic
	headerMiddleware := applyOverrideRequestHeaders(outbound)
	headers := make(http.Header)
	headers.Set("X-Clear-Header", "to-be-deleted")
	rawRequestWithHeaders := &httpclient.Request{
		Headers: headers,
	}

	processedRequestWithHeaders, err := headerMiddleware.OnOutboundRawRequest(ctx, rawRequestWithHeaders)
	require.NoError(t, err)

	// Verify header was cleared
	require.Empty(t, processedRequestWithHeaders.Headers.Get("X-Clear-Header"))
	// Verify header logic
	require.Equal(t, "env-prod", processedRequestWithHeaders.Headers.Get("X-Logic-Header"))
}

func TestOverrideParametersNumeric(t *testing.T) {
	ctx := context.Background()

	// Create test request
	llmRequest := &llm.Request{
		Model: "gpt-4",
		Metadata: map[string]string{
			"temp": "0.8",
			"max":  "500",
		},
	}

	// Create mock channel with numeric override parameters
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "numeric-test",
			Settings: &objects.ChannelSettings{
				OverrideParameters: `{
					"direct_int": 100,
					"direct_float": 0.5,
					"template_int": "{{.Metadata.max}}",
					"template_float": "{{.Metadata.temp}}",
					"mixed_json": "{\"val\": {{.Metadata.max}}, \"active\": true}"
				}`,
				OverrideHeaders: []objects.HeaderEntry{
					{Key: "X-Int-Header", Value: "{{.Metadata.max}}"},
					{Key: "X-Float-Header", Value: "{{.Metadata.temp}}"},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	// Create processor
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	// 1. Test Body Numeric Parsing
	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{
		Body: []byte("{}"),
	}

	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(processedRequest.Body)

	// Verify direct values
	require.Equal(t, int64(100), gjson.Get(bodyStr, "direct_int").Int())
	require.Equal(t, 0.5, gjson.Get(bodyStr, "direct_float").Float())

	// Verify template values parsed as numbers
	require.Equal(t, int64(500), gjson.Get(bodyStr, "template_int").Int())
	require.Equal(t, 0.8, gjson.Get(bodyStr, "template_float").Float())

	// Verify mixed JSON parsed correctly
	mixedJson := gjson.Get(bodyStr, "mixed_json")
	require.True(t, mixedJson.IsObject())
	require.Equal(t, int64(500), mixedJson.Get("val").Int())
	require.Equal(t, true, mixedJson.Get("active").Bool())

	// 2. Test Header Numeric (should be stringified)
	headerMiddleware := applyOverrideRequestHeaders(outbound)
	rawRequestWithHeaders := &httpclient.Request{
		Headers: make(http.Header),
	}

	processedRequestWithHeaders, err := headerMiddleware.OnOutboundRawRequest(ctx, rawRequestWithHeaders)
	require.NoError(t, err)

	require.Equal(t, "500", processedRequestWithHeaders.Headers.Get("X-Int-Header"))
	require.Equal(t, "0.8", processedRequestWithHeaders.Headers.Get("X-Float-Header"))
}

func TestOverrideHeadersKeepJSONLikeString(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{Model: "gpt-4"}

	expectedValue := `{"session_id":"843634473","camelCase":"AbC","xTraceId":"XyZ-001"}`

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "header-json-string-test",
			Settings: &objects.ChannelSettings{
				HeaderOverrideOperations: []objects.OverrideOperation{
					{Op: objects.OverrideOpSet, Path: "Extra", Value: expectedValue},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	headerMiddleware := applyOverrideRequestHeaders(outbound)
	rawRequest := &httpclient.Request{Headers: make(http.Header)}

	processedRequest, err := headerMiddleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)
	require.Equal(t, expectedValue, processedRequest.Headers.Get("Extra"))
}

func TestOverrideHeadersWithPromptCacheKeyTemplate(t *testing.T) {
	ctx := context.Background()
	promptCacheKey := `cache-key-123","admin":true`
	topLevelPromptCacheKey := "top-level-cache-key"

	tests := []struct {
		name           string
		request        *llm.Request
		expectedHeader string
	}{
		{
			name: "JSON-escapes prompt cache key",
			request: &llm.Request{
				Model:          "gpt-5.5",
				PromptCacheKey: &promptCacheKey,
			},
			expectedHeader: `{"session_id":"cache-key-123\",\"admin\":true"}`,
		},
		{
			name:           "skips header when prompt cache key is absent",
			request:        &llm.Request{Model: "gpt-5.5"},
			expectedHeader: "",
		},
		{
			name: "uses prompt cache key from compact request",
			request: &llm.Request{
				Model:   "gpt-5.5",
				Compact: &llm.CompactRequest{PromptCacheKey: "compact-cache-key"},
			},
			expectedHeader: `{"session_id":"compact-cache-key"}`,
		},
		{
			name: "prefers top-level prompt cache key",
			request: &llm.Request{
				Model:          "gpt-5.5",
				PromptCacheKey: &topLevelPromptCacheKey,
				Compact:        &llm.CompactRequest{PromptCacheKey: "compact-cache-key"},
			},
			expectedHeader: `{"session_id":"top-level-cache-key"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "prompt-cache-key-template-test",
					Settings: &objects.ChannelSettings{
						HeaderOverrideOperations: []objects.OverrideOperation{
							{
								Op:        objects.OverrideOpSet,
								Path:      "Extra",
								Value:     `{"session_id":{{toJSON .PromptCacheKey}}}`,
								Condition: `{{if .PromptCacheKey}}true{{end}}`,
							},
						},
					},
				},
				Outbound: &mockTransformer{},
			}
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					LlmRequest:       tt.request,
				},
			}

			headerMiddleware := applyOverrideRequestHeaders(outbound)
			processedRequest, err := headerMiddleware.OnOutboundRawRequest(ctx, &httpclient.Request{Headers: make(http.Header)})
			require.NoError(t, err)
			actualHeader := processedRequest.Headers.Get("Extra")
			require.Equal(t, tt.expectedHeader, actualHeader)
			if tt.expectedHeader != "" {
				var headerValue map[string]any
				require.NoError(t, json.Unmarshal([]byte(actualHeader), &headerValue))
				require.Len(t, headerValue, 1)
			}
		})
	}
}

func TestOverrideParametersWithRequestHeaderTemplate_NoRawRequest(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{Model: "gpt-4.1"}

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "body-request-header-no-raw-request-test",
			Settings: &objects.ChannelSettings{
				BodyOverrideOperations: []objects.OverrideOperation{
					{Op: objects.OverrideOpSet, Path: "missing_header", Value: `{{index .RequestHeader "x-trace-id"}}`},
					{Op: objects.OverrideOpSet, Path: "model_value", Value: `{{.Model}}`},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{Body: []byte(`{}`)}

	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(processedRequest.Body)
	require.Equal(t, "", gjson.Get(bodyStr, "missing_header").String())
	require.Equal(t, "gpt-4.1", gjson.Get(bodyStr, "model_value").String())
}

func TestOverrideHeadersWithRequestHeaderTemplate(t *testing.T) {
	ctx := context.Background()

	llmRequest := &llm.Request{
		Model: "gpt-4.1",
		RawRequest: &httpclient.Request{
			Headers: http.Header{
				"X-Trace-Id": []string{"trace-123"},
				"X-Api-Key":  []string{"secret-key"},
			},
		},
	}

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "header-request-header-template-test",
			Settings: &objects.ChannelSettings{
				HeaderOverrideOperations: []objects.OverrideOperation{
					{Op: objects.OverrideOpSet, Path: "X-Upstream-Trace", Value: `{{index .RequestHeader "X-Trace-Id"}}`},
					{Op: objects.OverrideOpSet, Path: "X-Filtered-Api-Key", Value: `{{index .RequestHeader "x-api-key"}}`},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	headerMiddleware := applyOverrideRequestHeaders(outbound)
	rawRequest := &httpclient.Request{Headers: make(http.Header)}

	processedRequest, err := headerMiddleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)
	require.Equal(t, "trace-123", processedRequest.Headers.Get("X-Upstream-Trace"))
	require.Equal(t, "", processedRequest.Headers.Get("X-Filtered-Api-Key"))
}

// TestOverrideParameters tests that TransformRequest works correctly.
// Note: Override parameters are now applied via OnRawRequest middleware,
// so this test only verifies the base transformation without overrides.
func TestOverrideParameters(t *testing.T) {
	ctx := context.Background()

	// Create mock channel
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:              1,
			Name:            "test-channel",
			SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
			Settings:        nil,
		},
		Outbound: &mockTransformer{},
	}

	// Create processor
	processor := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate:        &ChannelModelsCandidate{Channel: channel},
			ChannelModelsCandidates: []*ChannelModelsCandidate{{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}}},
			CurrentCandidateIndex:   0,
			RequestExec:             &ent.RequestExecution{ID: 1}, // Dummy to skip creation
		},
	}

	// Create test request
	text := "Hello"
	llmRequest := &llm.Request{
		Model: "gpt-4",
		Messages: []llm.Message{
			{
				Role: "user",
				Content: llm.MessageContent{
					Content: &text,
				},
			},
		},
	}

	// Transform request
	channelRequest, err := processor.TransformRequest(ctx, llmRequest)
	require.NoError(t, err)
	require.NotNil(t, channelRequest)

	// Verify base transformation works
	bodyStr := string(channelRequest.Body)
	temperature := gjson.Get(bodyStr, "temperature")
	require.Equal(t, 0.5, temperature.Float())
}

func TestOverrideParametersMiddleware(t *testing.T) {
	tests := []struct {
		name               string
		overrideParameters map[string]any
		expectedValues     map[string]any
		unexpectedKeys     []string
	}{
		{
			name: "override temperature",
			overrideParameters: map[string]any{
				"temperature": 0.9,
			},
			expectedValues: map[string]any{
				"temperature": 0.9,
				"max_tokens":  float64(1000),
			},
		},
		{
			name: "override multiple parameters",
			overrideParameters: map[string]any{
				"temperature": 0.7,
				"max_tokens":  2000,
				"top_p":       0.95,
			},
			expectedValues: map[string]any{
				"temperature": 0.7,
				"max_tokens":  float64(2000),
				"top_p":       0.95,
			},
		},
		{
			name: "override nested parameter",
			overrideParameters: map[string]any{
				"response_format.type": "json_object",
			},
			expectedValues: map[string]any{
				"response_format.type": "json_object",
			},
		},
		{
			name: "stream override ignored",
			overrideParameters: map[string]any{
				"stream":      true,
				"temperature": 0.8,
			},
			expectedValues: map[string]any{
				"temperature": 0.8,
			},
			unexpectedKeys: []string{"stream"},
		},
		{
			name:               "no override parameters",
			overrideParameters: nil,
			expectedValues: map[string]any{
				"temperature": 0.5,
				"max_tokens":  float64(1000),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create override parameters JSON
			var overrideParamsStr string

			if tt.overrideParameters != nil {
				data, err := json.Marshal(tt.overrideParameters)
				require.NoError(t, err)

				overrideParamsStr = string(data)
			}

			// Create mock channel with override parameters
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:              1,
					Name:            "test-channel",
					SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
					Settings: &objects.ChannelSettings{
						OverrideParameters: overrideParamsStr,
					},
				},
				Outbound: &mockTransformer{},
			}

			// Create outbound transformer
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					ChannelModelsCandidates: []*ChannelModelsCandidate{
						{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}},
					},
					CurrentCandidateIndex: 0,
					CurrentModelIndex:     0,
					LlmRequest: &llm.Request{
						Model: "gpt-4",
					},
				},
			}

			// Create the middleware
			middleware := applyOverrideRequestBody(outbound)

			// Create a test request
			requestBody, err := json.Marshal(map[string]any{
				"model":       "gpt-4",
				"temperature": 0.5,
				"max_tokens":  1000,
			})
			require.NoError(t, err)

			httpRequest := &httpclient.Request{
				Method: "POST",
				URL:    "https://api.example.com/v1/chat/completions",
				Body:   requestBody,
			}

			// Apply the middleware
			modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
			require.NoError(t, err)
			require.NotNil(t, modifiedRequest)

			// Verify expected values
			bodyStr := string(modifiedRequest.Body)
			for key, expectedValue := range tt.expectedValues {
				result := gjson.Get(bodyStr, key)
				require.True(t, result.Exists(), "key %s should exist", key)

				switch v := expectedValue.(type) {
				case float64:
					require.Equal(t, v, result.Float(), "key %s should have value %v", key, v)
				case string:
					require.Equal(t, v, result.String(), "key %s should have value %v", key, v)
				default:
					require.Equal(t, v, result.Value(), "key %s should have value %v", key, v)
				}
			}

			for _, key := range tt.unexpectedKeys {
				result := gjson.Get(bodyStr, key)
				require.False(t, result.Exists(), "key %s should not exist", key)
			}
		})
	}
}

func TestOverrideParametersMiddleware_InvalidJSON(t *testing.T) {
	ctx := context.Background()

	// Create channel with invalid JSON
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:              1,
			Name:            "test-channel",
			SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
			Settings: &objects.ChannelSettings{
				OverrideParameters: "invalid json",
			},
		},
		Outbound: &mockTransformer{},
	}

	// Create outbound transformer
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate:        &ChannelModelsCandidate{Channel: channel},
			ChannelModelsCandidates: []*ChannelModelsCandidate{{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}}},
			CurrentCandidateIndex:   0,
			CurrentModelIndex:       0,
			LlmRequest: &llm.Request{
				Model: "gpt-4",
			},
		},
	}

	// Create the middleware
	middleware := applyOverrideRequestBody(outbound)

	// Create a test request
	requestBody, err := json.Marshal(map[string]any{
		"model":       "gpt-4",
		"temperature": 0.5,
	})
	require.NoError(t, err)

	httpRequest := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
		Body:   requestBody,
	}

	// Apply the middleware - should not modify the request due to invalid JSON
	modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, modifiedRequest)

	// Verify original values are preserved
	bodyStr := string(modifiedRequest.Body)
	temperature := gjson.Get(bodyStr, "temperature")
	require.Equal(t, 0.5, temperature.Float())
}

func TestOverrideParametersMiddleware_EmptySettings(t *testing.T) {
	ctx := context.Background()

	// Create channel without settings
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:              1,
			Name:            "test-channel",
			SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
			Settings:        nil,
		},
		Outbound: &mockTransformer{},
	}

	// Create outbound transformer
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate:        &ChannelModelsCandidate{Channel: channel},
			ChannelModelsCandidates: []*ChannelModelsCandidate{{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}}},
			CurrentCandidateIndex:   0,
			CurrentModelIndex:       0,
			LlmRequest: &llm.Request{
				Model: "gpt-4",
			},
		},
	}

	// Create the middleware
	middleware := applyOverrideRequestBody(outbound)

	// Create a test request
	requestBody, err := json.Marshal(map[string]any{
		"model":       "gpt-4",
		"temperature": 0.5,
	})
	require.NoError(t, err)

	httpRequest := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
		Body:   requestBody,
	}

	// Apply the middleware - should not modify the request
	modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, modifiedRequest)

	// Verify original values
	bodyStr := string(modifiedRequest.Body)
	temperature := gjson.Get(bodyStr, "temperature")
	require.Equal(t, 0.5, temperature.Float())
}

func TestOverrideParametersMiddleware_AxonHubClear(t *testing.T) {
	tests := []struct {
		name               string
		overrideParameters map[string]any
		initialBody        map[string]any
		expectedRemoved    []string
		expectedPreserved  map[string]any
	}{
		{
			name: "remove single parameter with __AXONHUB_CLEAR__",
			overrideParameters: map[string]any{
				"temperature": "__AXONHUB_CLEAR__",
			},
			initialBody: map[string]any{
				"model":       "gpt-4",
				"temperature": 0.5,
				"max_tokens":  1000,
			},
			expectedRemoved:   []string{"temperature"},
			expectedPreserved: map[string]any{"model": "gpt-4", "max_tokens": float64(1000)},
		},
		{
			name: "remove multiple parameters with __AXONHUB_CLEAR__",
			overrideParameters: map[string]any{
				"temperature": "__AXONHUB_CLEAR__",
				"max_tokens":  "__AXONHUB_CLEAR__",
				"top_p":       0.95,
			},
			initialBody: map[string]any{
				"model":             "gpt-4",
				"temperature":       0.5,
				"max_tokens":        1000,
				"frequency_penalty": 0.1,
			},
			expectedRemoved: []string{"temperature", "max_tokens"},
			expectedPreserved: map[string]any{
				"model":             "gpt-4",
				"top_p":             0.95,
				"frequency_penalty": 0.1,
			},
		},
		{
			name: "remove nested parameter with __AXONHUB_CLEAR__",
			overrideParameters: map[string]any{
				"response_format.type": "__AXONHUB_CLEAR__",
			},
			initialBody: map[string]any{
				"model": "gpt-4",
				"response_format": map[string]any{
					"type":   "json_object",
					"schema": map[string]any{},
				},
			},
			expectedRemoved:   []string{"response_format.type"},
			expectedPreserved: map[string]any{"model": "gpt-4"},
		},
		{
			name: "mix of removal and override",
			overrideParameters: map[string]any{
				"temperature": "__AXONHUB_CLEAR__",
				"max_tokens":  2000,
				"top_p":       0.95,
			},
			initialBody: map[string]any{
				"model":       "gpt-4",
				"temperature": 0.5,
				"max_tokens":  1000,
			},
			expectedRemoved: []string{"temperature"},
			expectedPreserved: map[string]any{
				"model":      "gpt-4",
				"max_tokens": float64(2000),
				"top_p":      0.95,
			},
		},
		{
			name: "attempt to remove non-existent parameter",
			overrideParameters: map[string]any{
				"non_existent": "__AXONHUB_CLEAR__",
			},
			initialBody: map[string]any{
				"model":       "gpt-4",
				"temperature": 0.5,
			},
			expectedRemoved: []string{"non_existent"},
			expectedPreserved: map[string]any{
				"model":       "gpt-4",
				"temperature": 0.5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create override parameters JSON
			data, err := json.Marshal(tt.overrideParameters)
			require.NoError(t, err)

			overrideParamsStr := string(data)

			// Create mock channel with override parameters
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:              1,
					Name:            "test-channel",
					SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
					Settings: &objects.ChannelSettings{
						OverrideParameters: overrideParamsStr,
					},
				},
				Outbound: &mockTransformer{},
			}

			// Create outbound transformer
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					ChannelModelsCandidates: []*ChannelModelsCandidate{
						{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}},
					},
					CurrentCandidateIndex: 0,
					CurrentModelIndex:     0,
					LlmRequest: &llm.Request{
						Model: "gpt-4",
					},
				},
			}

			// Create the middleware
			middleware := applyOverrideRequestBody(outbound)

			// Create a test request with initial body
			requestBody, err := json.Marshal(tt.initialBody)
			require.NoError(t, err)

			httpRequest := &httpclient.Request{
				Method: "POST",
				URL:    "https://api.example.com/v1/chat/completions",
				Body:   requestBody,
			}

			// Apply the middleware
			modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
			require.NoError(t, err)
			require.NotNil(t, modifiedRequest)

			// Verify removed keys don't exist
			bodyStr := string(modifiedRequest.Body)
			for _, key := range tt.expectedRemoved {
				result := gjson.Get(bodyStr, key)
				require.False(t, result.Exists(), "key %s should not exist", key)
			}

			// Verify preserved keys exist with correct values
			for key, expectedValue := range tt.expectedPreserved {
				result := gjson.Get(bodyStr, key)
				require.True(t, result.Exists(), "key %s should exist", key)

				switch v := expectedValue.(type) {
				case float64:
					require.Equal(t, v, result.Float(), "key %s should have value %v", key, v)
				case string:
					require.Equal(t, v, result.String(), "key %s should have value %v", key, v)
				default:
					require.Equal(t, v, result.Value(), "key %s should have value %v", key, v)
				}
			}
		})
	}
}

func TestOverrideHeadersMiddleware(t *testing.T) {
	tests := []struct {
		name            string
		overrideHeaders []objects.HeaderEntry
		existingHeaders http.Header
		expectedHeaders http.Header
	}{
		{
			name:            "override single header",
			overrideHeaders: []objects.HeaderEntry{{Key: "User-Agent", Value: "AxonHub/1.0"}},
			existingHeaders: http.Header{
				"Content-Type": []string{"application/json"},
			},
			expectedHeaders: http.Header{
				"Content-Type": []string{"application/json"},
				"User-Agent":   []string{"AxonHub/1.0"},
			},
		},
		{
			name: "override multiple headers",
			overrideHeaders: []objects.HeaderEntry{
				{Key: "User-Agent", Value: "AxonHub/1.0"},
				{Key: "X-Custom-Header", Value: "custom-value"},
				{Key: "Authorization", Value: "Bearer token123"},
			},
			existingHeaders: http.Header{
				"Content-Type": []string{"application/json"},
				"User-Agent":   []string{"Original-Agent"},
			},
			expectedHeaders: http.Header{
				"Content-Type":    []string{"application/json"},
				"User-Agent":      []string{"AxonHub/1.0"},
				"X-Custom-Header": []string{"custom-value"},
			},
		},
		{
			name:            "no override headers",
			overrideHeaders: []objects.HeaderEntry{},
			existingHeaders: http.Header{
				"Content-Type": []string{"application/json"},
			},
			expectedHeaders: http.Header{
				"Content-Type": []string{"application/json"},
			},
		},
		{
			name: "override with empty path should be ignored",
			overrideHeaders: []objects.HeaderEntry{
				{Key: "", Value: "should-be-ignored"},
				{Key: "Valid-Header", Value: "valid-value"},
			},
			existingHeaders: http.Header{
				"Content-Type": []string{"application/json"},
			},
			expectedHeaders: http.Header{
				"Content-Type": []string{"application/json"},
				"Valid-Header": []string{"valid-value"},
			},
		},
		{
			name: "no existing headers",
			overrideHeaders: []objects.HeaderEntry{
				{Key: "User-Agent", Value: "AxonHub/1.0"},
				{Key: "X-API-Key", Value: "secret-key"},
			},
			existingHeaders: nil,
			expectedHeaders: http.Header{
				"User-Agent": []string{"AxonHub/1.0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create channel with override headers
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:              1,
					Name:            "test-channel",
					SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
					Settings: &objects.ChannelSettings{
						OverrideHeaders: tt.overrideHeaders,
					},
				},
				Outbound: &mockTransformer{},
			}

			// Create outbound transformer
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					ChannelModelsCandidates: []*ChannelModelsCandidate{
						{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}},
					},
					CurrentCandidateIndex: 0,
					CurrentModelIndex:     0,
					RequestExec:           &ent.RequestExecution{ID: 1}, // Dummy to skip creation
					LlmRequest: &llm.Request{
						Model: "gpt-4",
					},
				},
			}

			// Create middleware
			middleware := applyOverrideRequestHeaders(outbound)

			// Create HTTP request with existing headers
			httpRequest := &httpclient.Request{
				Method:  "POST",
				URL:     "https://api.example.com/v1/chat/completions",
				Body:    []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`),
				Headers: tt.existingHeaders,
			}

			// Apply middleware
			modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
			require.NoError(t, err)
			require.NotNil(t, modifiedRequest)

			// Verify headers
			require.NotNil(t, modifiedRequest.Headers)

			for key, expectedValue := range tt.expectedHeaders {
				require.Equal(t, expectedValue, modifiedRequest.Headers[key],
					"Header %s should have value %s", key, expectedValue)
			}
		})
	}
}

func TestOverrideHeadersMiddleware_EmptySettings(t *testing.T) {
	ctx := context.Background()

	// Create channel without settings
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:              1,
			Name:            "test-channel",
			SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
			Settings:        nil,
		},
		Outbound: &mockTransformer{},
	}

	// Create outbound transformer
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate:        &ChannelModelsCandidate{Channel: channel},
			ChannelModelsCandidates: []*ChannelModelsCandidate{{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}}},
			CurrentCandidateIndex:   0,
			CurrentModelIndex:       0,
			RequestExec:             &ent.RequestExecution{ID: 1}, // Dummy to skip creation
			LlmRequest: &llm.Request{
				Model: "gpt-4",
			},
		},
	}

	// Create middleware
	middleware := applyOverrideRequestHeaders(outbound)

	// Create HTTP request
	httpRequest := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
		Body:   []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`),
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
	}

	// Apply middleware
	modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, modifiedRequest)

	// Verify request is unchanged
	require.Equal(t, httpRequest.Headers, modifiedRequest.Headers)
}

func TestOverrideHeadersMiddleware_EmptyOverrideHeaders(t *testing.T) {
	ctx := context.Background()

	// Create channel with empty override headers
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:              1,
			Name:            "test-channel",
			SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
			Settings: &objects.ChannelSettings{
				OverrideHeaders: []objects.HeaderEntry{},
			},
		},
		Outbound: &mockTransformer{},
	}

	// Create outbound transformer
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate:        &ChannelModelsCandidate{Channel: channel},
			ChannelModelsCandidates: []*ChannelModelsCandidate{{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}}},
			CurrentCandidateIndex:   0,
			CurrentModelIndex:       0,
			RequestExec:             &ent.RequestExecution{ID: 1}, // Dummy to skip creation
			LlmRequest: &llm.Request{
				Model: "gpt-4",
			},
		},
	}

	// Create middleware
	middleware := applyOverrideRequestHeaders(outbound)

	// Create HTTP request
	httpRequest := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
		Body:   []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`),
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
	}

	// Apply middleware
	modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, modifiedRequest)

	// Verify request is unchanged
	require.Equal(t, httpRequest.Headers, modifiedRequest.Headers)
}

func TestOverrideHeadersMiddleware_OverrideExistingAuth(t *testing.T) {
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:              1,
			Name:            "auth-channel",
			SupportedModels: []string{"gpt-4"},
			Settings: &objects.ChannelSettings{
				OverrideHeaders: []objects.HeaderEntry{
					{Key: "Authorization", Value: "Bearer override-token"},
					{Key: "Api-Key", Value: "override-key"},
					{Key: "X-Api-Key", Value: "override-x-key"},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate:        &ChannelModelsCandidate{Channel: channel},
			ChannelModelsCandidates: []*ChannelModelsCandidate{{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}}},
			CurrentCandidateIndex:   0,
			CurrentModelIndex:       0,
			RequestExec:             &ent.RequestExecution{ID: 1},
			LlmRequest: &llm.Request{
				Model: "gpt-4",
			},
		},
	}

	middleware := applyOverrideRequestHeaders(outbound)

	httpRequest := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
		Body:   []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`),
		Headers: http.Header{
			"Authorization": []string{"Bearer original-token"},
			"Api-Key":       []string{"original-key"},
		},
	}

	modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, modifiedRequest)

	// Authorization should be overridden by channel override headers.
	require.Equal(t, "Bearer override-token", modifiedRequest.Headers.Get("Authorization"))
	// Api-Key should be overridden.
	require.Equal(t, "override-key", modifiedRequest.Headers.Get("Api-Key"))
	// X-Api-Key should be added because it was not present originally.
	require.Equal(t, "override-x-key", modifiedRequest.Headers.Get("X-Api-Key"))
}

func TestOverrideHeadersMiddleware_BlockedHeaders(t *testing.T) {
	tests := []struct {
		name            string
		overrideHeaders []objects.HeaderEntry
		existingHeaders http.Header
		expectedHeaders http.Header
		shouldBlock     []string
	}{
		{
			name: "transport headers are forwarded, sensitive allowed",
			overrideHeaders: []objects.HeaderEntry{
				{Key: "Content-Length", Value: "123"},
				{Key: "Authorization", Value: "Bearer token"},
				{Key: "User-Agent", Value: "CustomAgent"},
				{Key: "X-Custom-Header", Value: "custom-value"},
			},
			existingHeaders: http.Header{
				"Content-Type": []string{"application/json"},
			},
			expectedHeaders: http.Header{
				"Content-Type":    []string{"application/json"},
				"Content-Length":  []string{"123"},
				"User-Agent":      []string{"CustomAgent"},
				"X-Custom-Header": []string{"custom-value"},
				"Authorization":   []string{"Bearer token"},
			},
			shouldBlock: []string{},
		},
		{
			name: "transport headers remain forwarded",
			overrideHeaders: []objects.HeaderEntry{
				{Key: "Content-Length", Value: "123"},
				{Key: "Transfer-Encoding", Value: "chunked"},
				{Key: "Accept-Encoding", Value: "gzip"},
				{Key: "Authorization", Value: "Bearer token"},
				{Key: "Api-Key", Value: "secret"},
				{Key: "X-Api-Key", Value: "secret"},
				{Key: "X-Api-Secret", Value: "secret"},
				{Key: "X-Api-Token", Value: "secret"},
			},
			existingHeaders: http.Header{
				"Content-Type": []string{"application/json"},
			},
			expectedHeaders: http.Header{
				"Content-Type":      []string{"application/json"},
				"Content-Length":    []string{"123"},
				"Transfer-Encoding": []string{"chunked"},
				"Accept-Encoding":   []string{"gzip"},
				"Authorization":     []string{"Bearer token"},
				"Api-Key":           []string{"secret"},
				"X-Api-Key":         []string{"secret"},
				"X-Api-Secret":      []string{"secret"},
				"X-Api-Token":       []string{"secret"},
			},
			shouldBlock: []string{},
		},
		{
			name: "mixed case sensitive headers allowed",
			overrideHeaders: []objects.HeaderEntry{
				{Key: "authorization", Value: "Bearer token"},
				{Key: "API-KEY", Value: "secret"},
				{Key: "x-api-key", Value: "secret"},
				{Key: "Valid-Header", Value: "valid-value"},
			},
			existingHeaders: http.Header{
				"Content-Type": []string{"application/json"},
			},
			expectedHeaders: http.Header{
				"Content-Type":  []string{"application/json"},
				"Valid-Header":  []string{"valid-value"},
				"Authorization": []string{"Bearer token"},
				"Api-Key":       []string{"secret"},
				"X-Api-Key":     []string{"secret"},
			},
			shouldBlock: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create channel with override headers
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:              1,
					Name:            "test-channel",
					SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
					Settings: &objects.ChannelSettings{
						OverrideHeaders: tt.overrideHeaders,
					},
				},
				Outbound: &mockTransformer{},
			}

			// Create outbound transformer
			outbound := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					ChannelModelsCandidates: []*ChannelModelsCandidate{
						{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}},
					},
					CurrentCandidateIndex: 0,
					CurrentModelIndex:     0,
					RequestExec:           &ent.RequestExecution{ID: 1}, // Dummy to skip creation
					LlmRequest: &llm.Request{
						Model: "gpt-4",
					},
				},
			}

			// Create middleware
			middleware := applyOverrideRequestHeaders(outbound)

			// Create HTTP request with existing headers
			httpRequest := &httpclient.Request{
				Method:  "POST",
				URL:     "https://api.example.com/v1/chat/completions",
				Body:    []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}`),
				Headers: tt.existingHeaders,
			}

			// Apply middleware
			modifiedRequest, err := middleware.OnOutboundRawRequest(ctx, httpRequest)
			require.NoError(t, err)
			require.NotNil(t, modifiedRequest)

			// Verify headers
			require.NotNil(t, modifiedRequest.Headers)

			for key, expectedValue := range tt.expectedHeaders {
				require.Equal(t, expectedValue, modifiedRequest.Headers[key],
					"Header %s should have value %s", key, expectedValue)
			}

			// Verify blocked headers are not present
			for _, blockedHeader := range tt.shouldBlock {
				_, exists := modifiedRequest.Headers[blockedHeader]
				require.False(t, exists, "Blocked header %s should not be present", blockedHeader)
			}
		})
	}
}

func TestOverrideParametersRenderClear(t *testing.T) {
	ctx := context.Background()

	// Create test request with data for template
	llmRequest := &llm.Request{
		Model: "gpt-4",
		Metadata: map[string]string{
			"clear_flag": "true",
		},
	}

	// Create mock channel with override parameters using templates that render to __AXONHUB_CLEAR__
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "clear-test",
			Settings: &objects.ChannelSettings{
				OverrideParameters: `{
					"clear_body_field": "{{if eq .Metadata.clear_flag \"true\"}}__AXONHUB_CLEAR__{{else}}keep-me{{end}}",
					"keep_body_field": "{{if eq .Metadata.clear_flag \"false\"}}__AXONHUB_CLEAR__{{else}}keep-me{{end}}"
				}`,
				OverrideHeaders: []objects.HeaderEntry{
					{Key: "X-Clear-Header", Value: "{{if eq .Metadata.clear_flag \"true\"}}__AXONHUB_CLEAR__{{else}}keep-me{{end}}"},
					{Key: "X-Keep-Header", Value: "{{if eq .Metadata.clear_flag \"false\"}}__AXONHUB_CLEAR__{{else}}keep-me{{end}}"},
				},
			},
		},
		Outbound: &mockTransformer{},
	}

	// Create processor
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       llmRequest,
		},
	}

	// 1. Test Body Render Clear
	middleware := applyOverrideRequestBody(outbound)
	rawRequest := &httpclient.Request{
		Body: []byte(`{"clear_body_field": "to-be-deleted", "keep_body_field": "to-be-overwritten", "other": "stay"}`),
	}

	processedRequest, err := middleware.OnOutboundRawRequest(ctx, rawRequest)
	require.NoError(t, err)

	bodyStr := string(processedRequest.Body)
	require.False(t, gjson.Get(bodyStr, "clear_body_field").Exists(), "field should be cleared after rendering")
	require.Equal(t, "keep-me", gjson.Get(bodyStr, "keep_body_field").String())
	require.Equal(t, "stay", gjson.Get(bodyStr, "other").String())

	// 2. Test Header Render Clear
	headerMiddleware := applyOverrideRequestHeaders(outbound)
	headers := make(http.Header)
	headers.Set("X-Clear-Header", "to-be-deleted")
	headers.Set("X-Keep-Header", "to-be-overwritten")
	rawRequestWithHeaders := &httpclient.Request{
		Headers: headers,
	}

	processedRequestWithHeaders, err := headerMiddleware.OnOutboundRawRequest(ctx, rawRequestWithHeaders)
	require.NoError(t, err)

	require.Empty(t, processedRequestWithHeaders.Headers.Get("X-Clear-Header"), "header should be cleared after rendering")
	require.Equal(t, "keep-me", processedRequestWithHeaders.Headers.Get("X-Keep-Header"))
}
