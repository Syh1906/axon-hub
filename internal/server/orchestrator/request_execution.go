package orchestrator

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/pkg/xerrors"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

// Precompiled regex patterns for sanitizeResponseBody to avoid recompiling on each call.
var (
	tokenRegex  = regexp.MustCompile(`(?i)(bearer[\s:=]+)[a-zA-Z0-9_\-\.]+`)
	apiKeyRegex = regexp.MustCompile(`(api[keyK]ey|API[keyK]ey)["']?\s*[:=]\s*["']?([a-zA-Z0-9_\-\.]{8,})["']?`)
	emailRegex  = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
)

// sanitizeResponseBody redacts obvious secrets and truncates the body for safe logging.
func sanitizeResponseBody(body []byte, maxLen int) []byte {
	if len(body) == 0 {
		return body
	}

	str := string(body)

	// Redact bearer tokens (case-insensitive), preserving the bearer prefix
	str = tokenRegex.ReplaceAllString(str, "${1}[REDACTED]")

	// Redact API keys (common patterns)
	str = apiKeyRegex.ReplaceAllString(str, "$1=[REDACTED]")

	// Redact email addresses
	str = emailRegex.ReplaceAllString(str, "[EMAIL REDACTED]")

	// Truncate if too long
	if len(str) > maxLen {
		str = str[:maxLen] + "..."
	}

	return []byte(str)
}

// persistRequestExecutionMiddleware ensures a request execution exists and handles error updates.
type persistRequestExecutionMiddleware struct {
	pipeline.DummyMiddleware

	outbound *PersistentOutboundTransformer

	rawResponse *httpclient.Response
}

func persistRequestExecution(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return &persistRequestExecutionMiddleware{
		outbound: outbound,
	}
}

func (m *persistRequestExecutionMiddleware) Name() string {
	return "persist-request-execution"
}

func (m *persistRequestExecutionMiddleware) OnOutboundRawRequest(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	state := m.outbound.state
	if state == nil || state.RequestExec != nil {
		return request, nil
	}

	channel := m.outbound.GetCurrentChannel()
	if channel == nil {
		return request, nil
	}

	candidate := state.ChannelModelsCandidates[state.CurrentCandidateIndex]
	entry := candidate.Models[state.CurrentModelIndex]

	// Prefer the API format of the actual outbound request: transformers may emit
	// multiple formats (e.g. OpenAI outbound also builds audio speech/transcription
	// requests) while APIFormat() only reports the primary one.
	format := m.outbound.APIFormat()
	if request.APIFormat != "" {
		format = llm.APIFormat(request.APIFormat)
	}

	var cacheSample *usageanalytics.CacheSample
	if state.RawRequest != nil && state.Request != nil {
		cacheSample = usageanalytics.CacheSampleFromRequest(state.RawRequest.Body, state.Request.Format, state.Request.ProjectID, state.Request.APIKeyID, state.Request.ModelID)
	}
	requestExec, err := state.RequestService.CreateRequestExecution(
		ctx,
		channel,
		entry.ActualModel,
		state.Request,
		*request,
		format,
		state.PassThroughApplied,
		cacheSample,
		executionSelection(ctx, state),
		candidate.ModelCard,
	)
	if err != nil {
		return nil, err
	}

	// Update request with channel ID after channel selection
	if state.Request != nil && state.Request.ChannelID != channel.ID {
		var err error
		if state.RoutingPolicy.LoadBalancerStrategy == biz.LoadBalancerStrategyHealthGated {
			err = state.RequestService.UpdateRequestChannelIDWithoutSticky(ctx, state.Request.ID, channel.ID)
		} else {
			err = state.RequestService.UpdateRequestChannelID(ctx, state.Request.ID, channel.ID)
		}
		if err != nil {
			return nil, err
		}
		// Update the in-memory state to prevent duplicate updates and ensure consistency
		state.Request.ChannelID = channel.ID
	}

	state.RequestExec = requestExec

	return request, nil
}

func (m *persistRequestExecutionMiddleware) OnOutboundRawResponse(ctx context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	m.rawResponse = response
	if response != nil {
		captureImageCostEvidence(ctx, m.outbound.state, response.Body)
		state := m.outbound.state
		if state != nil && state.RequestExec != nil {
			if err := state.RequestService.SetExecutionUpstreamStatus(ctx, state.RequestExec.ID, response.StatusCode); err != nil {
				log.Warn(ctx, "Failed to persist upstream response status", log.Cause(err))
			}
		}
	}
	return response, nil
}

func (m *persistRequestExecutionMiddleware) OnOutboundLlmResponse(ctx context.Context, llmResp *llm.Response) (*llm.Response, error) {
	state := m.outbound.state
	if state == nil || state.RequestExec == nil {
		return llmResp, nil
	}

	// Use context without cancellation to ensure persistence even if client canceled
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Build latency metrics from performance record
	var metrics *biz.LatencyMetrics

	if state.Perf != nil && !state.Perf.StartTime.IsZero() {
		var (
			firstTokenLatencyMs int64
			requestLatencyMs    int64
		)

		if state.Perf.RequestCompleted && !state.Perf.EndTime.IsZero() {
			firstTokenLatencyMs, requestLatencyMs, _ = state.Perf.Calculate()
		} else {
			requestLatencyMs = time.Since(state.Perf.StartTime).Milliseconds()
			if state.Perf.Stream && state.Perf.FirstTokenTime != nil {
				firstTokenLatencyMs = state.Perf.FirstTokenTime.Sub(state.Perf.StartTime).Milliseconds()
			}

			requestLatencyMs = biz.ClampLatency(requestLatencyMs)
			firstTokenLatencyMs = biz.ClampLatency(firstTokenLatencyMs)
		}

		metrics = &biz.LatencyMetrics{
			LatencyMs:          &requestLatencyMs,
			FirstByteLatencyMs: state.Perf.FirstByteLatencyMs(),
		}
		if state.Perf.Stream && state.Perf.FirstTokenTime != nil {
			metrics.FirstTokenLatencyMs = &firstTokenLatencyMs
		}

		if state.Perf.Stream {
			reasoningDurationMs := state.Perf.CalculateReasoningDurationMs()
			if reasoningDurationMs > 0 {
				metrics.ReasoningDurationMs = &reasoningDurationMs
			}
		}
	}

	// Audio responses (binary TTS / non-JSON STT) must be converted to JSON-safe payloads
	// before persisting into the JSON response_body column.
	respBody := audioSafeResponseBody(llmResp.RequestType, m.rawResponse.Headers.Get("Content-Type"), m.rawResponse.Body)

	err := state.RequestService.UpdateRequestExecutionCompleted(
		persistCtx,
		state.RequestExec.ID,
		llmResp.ID,
		respBody,
		metrics,
	)
	if err != nil {
		log.Warn(persistCtx, "Failed to update request execution status to completed", log.Cause(err))
	}

	return llmResp, nil
}

func (m *persistRequestExecutionMiddleware) OnOutboundRawError(ctx context.Context, err error) {
	// Update request execution with the real error message when request fails
	state := m.outbound.state
	if state == nil || state.RequestExec == nil {
		return
	}

	// Log error with channel information for better debugging
	channel := m.outbound.GetCurrentChannel()
	if channel != nil {
		logFields := []log.Field{
			log.Cause(err),
			log.Int("channel_id", channel.ID),
			log.String("channel_name", channel.Name),
		}
		if modelID := m.outbound.GetCurrentModelID(); modelID != "" {
			logFields = append(logFields, log.String("model_id", modelID))
		}
		// Add response body for HTTP errors to help debug 400 errors (sanitized for PII)
		if httpErr, ok := xerrors.As[*httpclient.Error](err); ok && len(httpErr.Body) > 0 {
			sanitizedBody := sanitizeResponseBody(httpErr.Body, 1024)
			logFields = append(logFields, log.ByteString("response_body", sanitizedBody))
		}

		log.Warn(ctx, "request process failed", logFields...)
	}

	// Use context without cancellation to ensure persistence even if client canceled
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	var metrics *biz.LatencyMetrics
	if state.Perf != nil && !state.Perf.StartTime.IsZero() {
		_, latency, _ := state.Perf.Calculate()
		metrics = &biz.LatencyMetrics{LatencyMs: &latency, FirstByteLatencyMs: state.Perf.FirstByteLatencyMs()}
	}
	updateErr := persistRequestExecutionFailure(persistCtx, state.RequestService, state.RequestExec.ID, err, metrics)
	if updateErr != nil {
		log.Warn(persistCtx, "Failed to update request execution status to failed", log.Cause(updateErr))
	}
}

// ExtractErrorInfo extracts HTTP status code and sanitized response body from error.
func ExtractErrorInfo(err error) *biz.ExecutionErrorInfo { return extractExecutionErrorInfo(err, "") }

func extractExecutionErrorInfo(err error, secret string) *biz.ExecutionErrorInfo {
	if err == nil {
		return nil
	}
	failure := &objects.RequestExecutionFailure{Source: "gateway"}
	info := &biz.ExecutionErrorInfo{Failure: failure}
	if httpErr, ok := xerrors.As[*httpclient.Error](err); ok {
		info.StatusCode = &httpErr.StatusCode
		failure.Source = "upstream_http"
		failure.Body, failure.BodyTruncated = sanitizedDiagnosticBody(httpErr.Body, secret)
		for _, path := range []string{"error.code", "errors.0.code", "code"} {
			if code := gjson.GetBytes(httpErr.Body, path); code.Exists() {
				failure.Code = sanitizeDiagnosticText(code.String(), secret)
				break
			}
		}
		failure.Type = sanitizeDiagnosticText(gjson.GetBytes(httpErr.Body, "error.type").String(), secret)
		return info
	}
	if respErr, ok := xerrors.As[*llm.ResponseError](err); ok {
		if respErr.StatusCode != 0 {
			info.StatusCode = &respErr.StatusCode
		}
		failure.Source = "structured_error"
		failure.Code = sanitizeDiagnosticText(respErr.Detail.Code, secret)
		failure.Type = sanitizeDiagnosticText(respErr.Detail.Type, secret)
		if respErr.Detail.Code == ErrCodeUpstreamStreamInterrupted {
			failure.Source = "transport"
		}
	}
	if IsUpstreamTransportError(err) {
		failure.Source = "transport"
	}
	if errors.Is(err, context.Canceled) {
		failure.Source = "client_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		failure.Source = "timeout"
	}
	return info
}

// ExtractErrorMessage extracts HTTP error message from error.
func ExtractErrorMessage(err error) string {
	httpErr, ok := xerrors.As[*httpclient.Error](err)
	if !ok {
		// Prefer the structured message over ResponseError.Error(), which also
		// concatenates the status text, code and type.
		if respErr, ok := xerrors.As[*llm.ResponseError](err); ok && respErr.Detail.Message != "" {
			return respErr.Detail.Message
		}

		return err.Error()
	}

	// Anthropic && OpenAI error format.
	message := gjson.GetBytes(httpErr.Body, "error.message")
	if message.Exists() && message.Type == gjson.String {
		return message.String()
	}

	// Other compatible error format.
	// Try errors.0.message first, then fall back to errors.message
	message1 := gjson.GetBytes(httpErr.Body, "errors.0.message")
	message2 := gjson.GetBytes(httpErr.Body, "errors.message")

	if message1.Exists() && message1.Type == gjson.String && message1.String() != "" {
		return message1.String()
	}

	if message2.Exists() && message2.Type == gjson.String {
		return message2.String()
	}

	return httpErr.Error()
}
