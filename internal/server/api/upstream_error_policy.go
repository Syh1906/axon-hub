package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

func writeOrchestratorError(c *gin.Context, err error, orch *orchestrator.ChatCompletionOrchestrator) {
	httpErr := transformOrchestratorError(c.Request.Context(), err, orch)
	if httpErr.StatusCode == http.StatusTooManyRequests {
		var original *httpclient.Error
		if errors.As(err, &original) && original.StatusCode == http.StatusTooManyRequests {
			if retryAfter := original.Headers.Get("Retry-After"); retryAfter != "" {
				c.Header("Retry-After", retryAfter)
			}
		}
	}
	c.JSON(httpErr.StatusCode, json.RawMessage(httpErr.Body))
}

func transformOrchestratorError(ctx context.Context, err error, orch *orchestrator.ChatCompletionOrchestrator) *httpclient.Error {
	if isCanceledRequest(ctx, err) {
		return &httpclient.Error{StatusCode: 499, Status: "Client Closed Request", Body: []byte(`{"error":{"code":"request_canceled","type":"request_canceled","message":"The request was canceled before a response was delivered."}}`)}
	}
	if errors.Is(err, httpclient.ErrContinuationUnavailable) {
		return &httpclient.Error{StatusCode: http.StatusBadRequest, Status: http.StatusText(http.StatusBadRequest), Body: []byte(`{"error":{"code":"previous_response_not_found","type":"invalid_request_error","param":"previous_response_id","message":"Previous response context is unavailable. Send complete input to resume."}}`)}
	}
	err = wrapQuotaExhaustedAsResponseError(err)
	err = orchestrator.ClassifyUpstreamTransportError(err)

	if orch != nil {
		err = applyUpstreamErrorPolicy(ctx, err, orch.SystemService)
		return orch.Inbound.TransformError(ctx, err)
	}

	return &httpclient.Error{
		StatusCode: http.StatusInternalServerError,
		Status:     http.StatusText(http.StatusInternalServerError),
		Body:       []byte(`{"error":{"message":"internal server error","type":"internal_server_error"}}`),
	}
}

func isCanceledRequest(ctx context.Context, err error) bool {
	return errors.Is(ctx.Err(), context.Canceled) && errors.Is(err, context.Canceled)
}

func applyUpstreamErrorPolicy(ctx context.Context, err error, systemService *biz.SystemService) error {
	if err == nil || systemService == nil {
		return err
	}

	var quotaErr *orchestrator.QuotaExhaustedError
	if errors.As(err, &quotaErr) {
		return err
	}

	policy := loadUpstreamErrorPolicy(ctx, systemService)
	if policy.Mode == biz.UpstreamErrorModePassthrough || policy.Mode == "" {
		return err
	}

	if !pipeline.IsUpstreamError(err) {
		return err
	}

	message := biz.DefaultUpstreamErrorMessage
	if policy.Mode == biz.UpstreamErrorModeCustom {
		message = strings.TrimSpace(policy.CustomMessage)
		if message == "" {
			message = biz.DefaultUpstreamErrorMessage
		}
	}

	var respErr *llm.ResponseError
	if errors.As(err, &respErr) {
		if respErr.Detail.Code == errCodeQuotaExhausted || respErr.Detail.Type == errTypeQuotaExhausted {
			return err
		}

		return &llm.ResponseError{
			StatusCode: respErr.StatusCode,
			Cause:      err,
			Detail: llm.ErrorDetail{
				Message:   message,
				Type:      firstNonEmpty(respErr.Detail.Type, "upstream_error"),
				Code:      respErr.Detail.Code,
				RequestID: respErr.Detail.RequestID,
			},
		}
	}

	var httpErr *httpclient.Error
	if errors.As(err, &httpErr) {
		return &llm.ResponseError{
			StatusCode: httpErr.StatusCode,
			Cause:      err,
			Detail: llm.ErrorDetail{
				Message:   message,
				Type:      upstreamErrorTypeFromHTTP(httpErr),
				Code:      upstreamErrorCodeFromHTTP(httpErr),
				RequestID: upstreamRequestIDFromHTTP(httpErr),
			},
		}
	}

	return &llm.ResponseError{
		StatusCode: http.StatusBadGateway,
		Cause:      err,
		Detail: llm.ErrorDetail{
			Message: message,
			Type:    "upstream_error",
		},
	}
}

func loadUpstreamErrorPolicy(ctx context.Context, systemService *biz.SystemService) biz.UpstreamErrorPolicy {
	// Error delivery runs outside the orchestrator's privileged context, and the
	// caller may already be canceled. Only this bounded settings read is privileged.
	policyCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()
	policyCtx = authz.WithSystemBypass(policyCtx, "read-upstream-error-policy")
	return systemService.RetryPolicyOrDefault(policyCtx).UpstreamErrorPolicy
}

type upstreamErrorStream struct {
	stream        streams.Stream[*httpclient.StreamEvent]
	ctx           context.Context
	systemService *biz.SystemService
}

func newUpstreamErrorStream(ctx context.Context, stream streams.Stream[*httpclient.StreamEvent], systemService *biz.SystemService) streams.Stream[*httpclient.StreamEvent] {
	if stream == nil || systemService == nil {
		return stream
	}

	return &upstreamErrorStream{
		stream:        stream,
		ctx:           ctx,
		systemService: systemService,
	}
}

func (s *upstreamErrorStream) Next() bool {
	return s.stream.Next()
}

func (s *upstreamErrorStream) Current() *httpclient.StreamEvent {
	return s.stream.Current()
}

func (s *upstreamErrorStream) ExpectedStreamChoices() int {
	return streamExpectedChoices(s.stream)
}

func (s *upstreamErrorStream) Err() error {
	err := s.stream.Err()
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	// Classify transport-level interruptions before the policy runs so the stable
	// code and 502 semantics survive a hidden/custom message rewrite.
	if orchestrator.IsUpstreamTransportError(err) {
		err = orchestrator.ClassifyUpstreamTransportError(pipeline.WrapUpstreamError(err))
	}

	policy := loadUpstreamErrorPolicy(s.ctx, s.systemService)
	if policy.Mode != "" && policy.Mode != biz.UpstreamErrorModePassthrough {
		return applyUpstreamErrorPolicy(s.ctx, pipeline.WrapUpstreamError(err), s.systemService)
	}

	return err
}

func (s *upstreamErrorStream) Close() error {
	return s.stream.Close()
}

func upstreamErrorTypeFromHTTP(httpErr *httpclient.Error) string {
	if httpErr != nil && len(httpErr.Body) > 0 {
		if t := gjson.GetBytes(httpErr.Body, "error.type"); t.Exists() && t.Type == gjson.String && t.String() != "" {
			return t.String()
		}
	}

	return "upstream_error"
}

func upstreamErrorCodeFromHTTP(httpErr *httpclient.Error) string {
	if httpErr != nil && len(httpErr.Body) > 0 {
		if c := gjson.GetBytes(httpErr.Body, "error.code"); c.Exists() && c.Type == gjson.String && c.String() != "" {
			return c.String()
		}
	}

	return ""
}

func upstreamRequestIDFromHTTP(httpErr *httpclient.Error) string {
	if httpErr != nil && len(httpErr.Body) > 0 {
		if rid := gjson.GetBytes(httpErr.Body, "request_id"); rid.Exists() && rid.Type == gjson.String && rid.String() != "" {
			return rid.String()
		}
	}

	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}
