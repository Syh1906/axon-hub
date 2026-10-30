package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/pkg/xjson"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/pipeline/stream"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

const testChannelAPIKeysMaxConcurrency = 8

const responsesWebSocketTestPrompt = "ping"

// ChannelTestTimeout bounds a channel test independently of the admin request
// deadline. Zero leaves the caller's context untouched.
type ChannelTestTimeout time.Duration

// TestChannelOrchestrator handles channel testing functionality.
// It is stateless and can be reused across multiple test requests.
type TestChannelOrchestrator struct {
	channelService              *biz.ChannelService
	requestService              *biz.RequestService
	systemService               *biz.SystemService
	usageLogService             *biz.UsageLogService
	promptProtectionRuleService *biz.PromptProtectionRuleService
	httpClient                  *httpclient.HttpClient
	testTimeout                 time.Duration
	modelCircuitBreaker         *biz.ModelCircuitBreaker
	modelMapper                 *ModelMapper
	loadBalancer                *LoadBalancer
	channelLimiterManager       *ChannelLimiterManager
}

// NewTestChannelOrchestrator creates a new TestChannelOrchestrator.
func NewTestChannelOrchestrator(
	channelService *biz.ChannelService,
	requestService *biz.RequestService,
	systemService *biz.SystemService,
	usageLogService *biz.UsageLogService,
	promptProtectionRuleService *biz.PromptProtectionRuleService,
	httpClient *httpclient.HttpClient,
	testTimeout ChannelTestTimeout,
) *TestChannelOrchestrator {
	return &TestChannelOrchestrator{
		channelService:              channelService,
		requestService:              requestService,
		systemService:               systemService,
		usageLogService:             usageLogService,
		promptProtectionRuleService: promptProtectionRuleService,
		httpClient:                  httpClient,
		testTimeout:                 time.Duration(testTimeout),
		modelCircuitBreaker:         biz.NewModelCircuitBreaker(),
		modelMapper:                 NewModelMapper(),
		loadBalancer:                NewLoadBalancer(systemService, channelService, NewWeightStrategy()),
		channelLimiterManager:       NewChannelLimiterManager(),
	}
}

// testContext detaches a channel test from the admin request deadline and
// applies the configured test timeout, so slow upstreams such as image
// generation are not cut off at the console's RequestTimeout. The test then
// runs to completion or timeout even if the console disconnects.
func (processor *TestChannelOrchestrator) testContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if processor.testTimeout <= 0 {
		return ctx, func() {}
	}

	return xcontext.DetachWithTimeout(ctx, processor.testTimeout)
}

// TestChannelRequest represents a channel test request.
type TestChannelRequest struct {
	ChannelID objects.GUID
	ModelID   *string
}

// buildChannelTestRequest creates the request used by channel tests.
func buildChannelTestRequest(model string, useStream bool, systemPrompt string, userPrompt string, responsesWebSocket bool) *llm.Request {
	req := &llm.Request{
		Model: model,
		Messages: []llm.Message{
			{
				Role:    "system",
				Content: llm.MessageContent{Content: lo.ToPtr(systemPrompt)},
			},
			{
				Role:    "user",
				Content: llm.MessageContent{Content: lo.ToPtr(userPrompt)},
			},
		},
		MaxCompletionTokens: lo.ToPtr(int64(256)),
		Stream:              lo.ToPtr(useStream),
	}

	if responsesWebSocket {
		req.Messages = []llm.Message{{
			Role:    "user",
			Content: llm.MessageContent{Content: lo.ToPtr(responsesWebSocketTestPrompt)},
		}}
		req.MaxCompletionTokens = nil
		req.Stream = lo.ToPtr(true)
	}

	return req
}

// usesResponsesWebSocket reports whether a channel routes Responses requests over WebSocket.
func usesResponsesWebSocket(channel *biz.Channel) bool {
	if channel == nil {
		return false
	}

	for _, endpoint := range channel.ResolveEndpoints() {
		if endpoint.APIFormat != llm.APIFormatOpenAIResponse.String() && endpoint.APIFormat != llm.APIFormatOpenAIResponseCompact.String() {
			continue
		}

		if endpoint.BaseURL == "" {
			endpoint.BaseURL = channel.BaseURL
		}
		if endpoint.WebSocketsSupported() {
			return true
		}
	}

	return false
}

func buildChannelTestImageRequest(model string, prompt string) openai.ImageGenerationRequest {
	return openai.ImageGenerationRequest{
		Prompt:         prompt,
		Model:          model,
		N:              lo.ToPtr(int64(1)),
		Size:           "1024x1024",
		Quality:        "low",
		ResponseFormat: "b64_json",
	}
}

func channelUsesImageTest(settings *objects.ChannelSettings) bool {
	return objects.IsImageGenerationPrimary(settings)
}

func marshalChannelTestBody(settings *objects.ChannelSettings, model string, useStream bool, systemPrompt string, userPrompt string, responsesWebSocket bool) ([]byte, error) {
	if channelUsesImageTest(settings) {
		return json.Marshal(buildChannelTestImageRequest(model, userPrompt))
	}

	return json.Marshal(buildChannelTestRequest(model, useStream, systemPrompt, userPrompt, responsesWebSocket))
}

// TestChannelResult represents the result of a channel test.
type TestChannelResult struct {
	Latency float64
	Success bool
	Message *string
	Error   *string
}

// TestChannel tests a specific channel with a simple request.
func (processor *TestChannelOrchestrator) TestChannel(
	ctx context.Context,
	channelID objects.GUID,
	modelID *string,
	proxy *httpclient.ProxyConfig,
) (*TestChannelResult, error) {
	ctx, cancel := processor.testContext(ctx)
	defer cancel()

	channel, err := processor.channelService.GetChannel(ctx, channelID.ID)
	if err != nil {
		return nil, err
	}

	var inbound transformer.Inbound = openai.NewInboundTransformer()
	if channelUsesImageTest(channel.Settings) {
		inbound = openai.NewImageGenerationInboundTransformer()
	}

	chatProcessor := &ChatCompletionOrchestrator{
		channelSelector: NewSpecifiedChannelSelector(processor.channelService, channelID),
		RequestService:  processor.requestService,
		ChannelService:  processor.channelService,
		PromptProvider:  &stubPromptProvider{},
		PromptProtecter: processor.promptProtectionRuleService,
		PipelineFactory: pipeline.NewFactory(processor.httpClient),
		Middlewares: []pipeline.Middleware{
			stream.EnsureUsage(),
		},
		Inbound:                    inbound,
		SystemService:              processor.systemService,
		UsageLogService:            processor.usageLogService,
		proxy:                      proxy,
		ModelMapper:                processor.modelMapper,
		adaptiveLoadBalancer:       processor.loadBalancer,
		failoverLoadBalancer:       processor.loadBalancer,
		circuitBreakerLoadBalancer: processor.loadBalancer,
		channelLimiterManager:      processor.channelLimiterManager,
		modelCircuitBreaker:        processor.modelCircuitBreaker,
	}

	testModel := lo.FromPtr(modelID)
	if testModel == "" {
		testModel = channel.DefaultTestModel
	}
	systemPrompt, userPrompt, err := processor.systemService.ChannelTestPrompts(ctx)
	if err != nil {
		return nil, err
	}

	useStream := channel != nil && channel.Policies.Stream == objects.CapabilityPolicyRequire && !channelUsesImageTest(channel.Settings)

	body, err := marshalChannelTestBody(channel.Settings, testModel, useStream, systemPrompt, userPrompt, usesResponsesWebSocket(channel))
	if err != nil {
		return nil, err
	}

	// Measure latency
	if usesResponsesWebSocket(channel) {
		ctx = shared.WithResponsesWebSocket(ctx)
	}
	startTime := time.Now()
	rawResponse, err := chatProcessor.Process(ctx, &httpclient.Request{
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: body,
	})

	rawErr := inbound.TransformError(ctx, err)
	message := gjson.GetBytes(rawErr.Body, "error.message").String()

	if err != nil {
		return &TestChannelResult{
			Latency: time.Since(startTime).Seconds(),
			Success: false,
			Message: new(""),
			Error:   new(message),
		}, nil
	}

	// Handle streaming response
	if rawResponse.ChatCompletionStream != nil {
		return processor.handleStreamResponse(ctx, rawResponse.ChatCompletionStream, startTime)
	}

	latency := time.Since(startTime).Seconds()

	return interpretTestChannelBody(rawResponse.ChatCompletion.Body, latency, channelUsesImageTest(channel.Settings))
}

func interpretTestChannelBody(body []byte, latency float64, image bool) (*TestChannelResult, error) {
	if image {
		imageResponse, err := xjson.To[llm.ImageResponse](body)
		if err != nil {
			return &TestChannelResult{
				Latency: latency,
				Success: false,
				Message: new(""),
				Error:   new(err.Error()),
			}, nil
		}
		if len(imageResponse.Data) == 0 {
			return &TestChannelResult{
				Latency: latency,
				Success: false,
				Message: new(""),
				Error:   new("No image in response"),
			}, nil
		}
		message := imageResponse.Data[0].RevisedPrompt
		if message == "" {
			message = fmt.Sprintf("generated %d image(s)", len(imageResponse.Data))
		}
		return &TestChannelResult{
			Latency: latency,
			Success: true,
			Message: &message,
			Error:   nil,
		}, nil
	}

	response, err := xjson.To[llm.Response](body)
	if err != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: new(""),
			Error:   new(err.Error()),
		}, nil
	}

	if len(response.Choices) == 0 {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: new(""),
			Error:   new("No message in response"),
		}, nil
	}

	return &TestChannelResult{
		Latency: latency,
		Success: true,
		Message: response.Choices[0].Message.Content.Content,
		Error:   nil,
	}, nil
}

// handleStreamResponse processes a streaming response and accumulates the content.
func (processor *TestChannelOrchestrator) handleStreamResponse(
	ctx context.Context,
	stream streams.Stream[*httpclient.StreamEvent],
	startTime time.Time,
) (*TestChannelResult, error) {
	defer func() {
		_ = stream.Close()
	}()

	// Accumulate stream chunks
	var accumulatedContent string

	for stream.Next() {
		select {
		case <-ctx.Done():
			return &TestChannelResult{
				Latency: time.Since(startTime).Seconds(),
				Success: false,
				Message: lo.ToPtr(accumulatedContent),
				Error:   lo.ToPtr(ctx.Err().Error()),
			}, nil
		default:
		}

		event := stream.Current()
		if event == nil {
			continue
		}

		// The stream may end with a "[DONE]" message which is not valid JSON.
		if string(event.Data) == "[DONE]" {
			continue
		}

		// Parse the stream event data
		var chunk llm.Response
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			log.Warn(ctx, "failed to unmarshal stream event data", log.Cause(err), log.ByteString("data", event.Data))
			continue
		}

		// Accumulate content from the first choice
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil && chunk.Choices[0].Delta.Content.Content != nil {
			accumulatedContent += *chunk.Choices[0].Delta.Content.Content
		}
	}

	// Calculate latency after processing all stream events
	latency := time.Since(startTime).Seconds()

	if err := ctx.Err(); err != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(accumulatedContent),
			Error:   lo.ToPtr(err.Error()),
		}, nil
	}

	if stream.Err() != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr(stream.Err().Error()),
		}, nil
	}

	if accumulatedContent == "" {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr("No content in stream response"),
		}, nil
	}

	return &TestChannelResult{
		Latency: latency,
		Success: true,
		Message: lo.ToPtr(accumulatedContent),
		Error:   nil,
	}, nil
}

// TestAPIKeyResult represents the result of testing a single API key.
type TestAPIKeyResult struct {
	KeyPrefix string
	Success   bool
	Latency   float64
	Error     *string
	Disabled  bool
}

// TestChannelAPIKeysResult represents the aggregated result of testing all API keys.
type TestChannelAPIKeysResult struct {
	ChannelID    objects.GUID
	Total        int
	SuccessCount int
	FailedCount  int
	Results      []*TestAPIKeyResult
}

// TestChannelAPIKeys tests all API keys for a specific channel individually.
func (processor *TestChannelOrchestrator) TestChannelAPIKeys(
	ctx context.Context,
	channelID objects.GUID,
	modelID *string,
	proxy *httpclient.ProxyConfig,
) (*TestChannelAPIKeysResult, error) {
	ctx, cancel := processor.testContext(ctx)
	defer cancel()

	ch, err := processor.channelService.GetChannel(ctx, channelID.ID)
	if err != nil {
		return nil, err
	}

	allKeys := ch.Credentials.GetAllAPIKeys()
	if len(allKeys) == 0 {
		return nil, fmt.Errorf("no API keys configured for channel")
	}

	// Build disabled set
	disabledSet := make(map[string]struct{}, len(ch.DisabledAPIKeys))
	for _, dk := range ch.DisabledAPIKeys {
		disabledSet[dk.Key] = struct{}{}
	}

	testModel := lo.FromPtr(modelID)
	if testModel == "" {
		testModel = ch.DefaultTestModel
	}

	useStream := ch.Policies.Stream == objects.CapabilityPolicyRequire && !channelUsesImageTest(ch.Settings)
	responsesWebSocket := usesResponsesWebSocket(ch)
	systemPrompt, userPrompt, err := processor.systemService.ChannelTestPrompts(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]*TestAPIKeyResult, len(allKeys))

	var (
		successCount int32
		failedCount  int32
	)

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(min(testChannelAPIKeysMaxConcurrency, len(allKeys)))

	for i, key := range allKeys {
		index := i
		apiKey := key

		group.Go(func() error {
			select {
			case <-groupCtx.Done():
				errMsg := groupCtx.Err().Error()
				results[index] = &TestAPIKeyResult{
					KeyPrefix: maskAPIKey(apiKey),
					Success:   false,
					Error:     &errMsg,
				}

				atomic.AddInt32(&failedCount, 1)

				return nil
			default:
			}

			result := processor.testSingleKey(groupCtx, ch, apiKey, testModel, useStream, responsesWebSocket, proxy, systemPrompt, userPrompt)
			_, isDisabled := disabledSet[apiKey]
			result.Disabled = isDisabled
			results[index] = result

			if result.Success {
				atomic.AddInt32(&successCount, 1)
				return nil
			}

			atomic.AddInt32(&failedCount, 1)

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}

	return &TestChannelAPIKeysResult{
		ChannelID:    channelID,
		Total:        len(allKeys),
		SuccessCount: int(successCount),
		FailedCount:  int(failedCount),
		Results:      results,
	}, nil
}

// TestSingleAPIKey tests a single API key for a channel.
// It verifies that the provided key belongs to the channel before testing.
func (processor *TestChannelOrchestrator) TestSingleAPIKey(
	ctx context.Context,
	channelID objects.GUID,
	key string,
	modelID *string,
	proxy *httpclient.ProxyConfig,
) (*TestAPIKeyResult, error) {
	ctx, cancel := processor.testContext(ctx)
	defer cancel()

	ch, err := processor.channelService.GetChannel(ctx, channelID.ID)
	if err != nil {
		return nil, err
	}

	// Verify the provided key is actually configured for this channel.
	channelKeys := ch.Credentials.GetAllAPIKeys()
	if len(channelKeys) == 0 {
		return nil, fmt.Errorf("no API keys configured for channel")
	}

	keyBelongsToChannel := lo.Contains(channelKeys, key)
	if !keyBelongsToChannel {
		return nil, fmt.Errorf("the provided API key is not configured for this channel")
	}

	testModel := lo.FromPtr(modelID)
	if testModel == "" {
		testModel = ch.DefaultTestModel
	}

	useStream := ch.Policies.Stream == objects.CapabilityPolicyRequire && !channelUsesImageTest(ch.Settings)
	responsesWebSocket := usesResponsesWebSocket(ch)
	systemPrompt, userPrompt, err := processor.systemService.ChannelTestPrompts(ctx)
	if err != nil {
		return nil, err
	}

	disabledSet := make(map[string]struct{}, len(ch.DisabledAPIKeys))
	for _, dk := range ch.DisabledAPIKeys {
		disabledSet[dk.Key] = struct{}{}
	}

	result := processor.testSingleKey(ctx, ch, key, testModel, useStream, responsesWebSocket, proxy, systemPrompt, userPrompt)
	_, isDisabled := disabledSet[key]
	result.Disabled = isDisabled

	return result, nil
}

// testSingleKey tests a single API key by forcing the use of a specific key via SetAPIKey.
func (processor *TestChannelOrchestrator) testSingleKey(
	ctx context.Context,
	ch *biz.Channel,
	key string,
	testModel string,
	useStream bool,
	responsesWebSocket bool,
	proxy *httpclient.ProxyConfig,
	systemPrompt string,
	userPrompt string,
) *TestAPIKeyResult {
	if responsesWebSocket {
		ctx = shared.WithResponsesWebSocket(ctx)
	}
	keyPrefix := maskAPIKey(key)

	var inbound transformer.Inbound = openai.NewInboundTransformer()
	if channelUsesImageTest(ch.Settings) {
		inbound = openai.NewImageGenerationInboundTransformer()
	}

	chatProcessor := &ChatCompletionOrchestrator{
		channelSelector: &SpecifiedChannelSelector{
			ChannelService: processor.channelService,
			ChannelID:      objects.GUID{ID: ch.ID},
			SelectedAPIKey: key,
		},
		RequestService:  processor.requestService,
		ChannelService:  processor.channelService,
		PromptProvider:  &stubPromptProvider{},
		PromptProtecter: processor.promptProtectionRuleService,
		PipelineFactory: pipeline.NewFactory(processor.httpClient),
		Middlewares: []pipeline.Middleware{
			stream.EnsureUsage(),
		},
		Inbound:                    inbound,
		SystemService:              processor.systemService,
		UsageLogService:            processor.usageLogService,
		proxy:                      proxy,
		ModelMapper:                processor.modelMapper,
		adaptiveLoadBalancer:       processor.loadBalancer,
		failoverLoadBalancer:       processor.loadBalancer,
		circuitBreakerLoadBalancer: processor.loadBalancer,
		channelLimiterManager:      processor.channelLimiterManager,
		modelCircuitBreaker:        processor.modelCircuitBreaker,
	}

	body, err := marshalChannelTestBody(ch.Settings, testModel, useStream, systemPrompt, userPrompt, responsesWebSocket)
	if err != nil {
		errMsg := err.Error()

		return &TestAPIKeyResult{
			KeyPrefix: keyPrefix,
			Success:   false,
			Error:     &errMsg,
		}
	}

	startTime := time.Now()

	rawResponse, err := chatProcessor.Process(ctx, &httpclient.Request{
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: body,
	})
	if err != nil {
		rawErr := inbound.TransformError(ctx, err)
		message := gjson.GetBytes(rawErr.Body, "error.message").String()

		return &TestAPIKeyResult{
			KeyPrefix: keyPrefix,
			Success:   false,
			Latency:   time.Since(startTime).Seconds(),
			Error:     new(message),
		}
	}

	// Handle streaming response
	if rawResponse.ChatCompletionStream != nil {
		streamResult, _ := processor.handleStreamResponse(ctx, rawResponse.ChatCompletionStream, startTime)

		return &TestAPIKeyResult{
			KeyPrefix: keyPrefix,
			Success:   streamResult.Success,
			Latency:   streamResult.Latency,
			Error:     streamResult.Error,
		}
	}

	latency := time.Since(startTime).Seconds()
	interpreted, _ := interpretTestChannelBody(rawResponse.ChatCompletion.Body, latency, channelUsesImageTest(ch.Settings))

	return &TestAPIKeyResult{
		KeyPrefix: keyPrefix,
		Success:   interpreted.Success,
		Latency:   interpreted.Latency,
		Error:     interpreted.Error,
	}
}

// maskAPIKey returns a masked version of the API key for display.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}

	return key[:4] + "****" + key[len(key)-4:]
}
