package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcontext"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/pipeline"
)

var errStreamDeliveryUnconfirmed = errors.New("stream terminal delivery was not confirmed")

// Preserve delivery errors separately from upstream completion and accounting.
// This runs while the inbound stream's state lock is held during finalization.
func (ts *InboundPersistentStream) persistStreamFailure(ctx context.Context, cause error) error {
	if !errors.Is(cause, errStreamDeliveryUnconfirmed) &&
		(ts.outcome.deliveryErr == nil || !errors.Is(cause, ts.outcome.deliveryErr)) {
		return ts.requestService.UpdateRequestStatusFromError(ctx, ts.request.ID, cause)
	}
	failure := &objects.RequestFailure{
		Stage: "delivery", Type: "gateway_error", Code: "stream_delivery_failed",
		Message: "Response delivery to the client failed before completion was confirmed.",
	}
	switch {
	case errors.Is(cause, context.Canceled):
		failure.Code = "stream_delivery_canceled"
		failure.Message = "The client connection was canceled before response delivery was confirmed."
	case errors.Is(cause, context.DeadlineExceeded):
		failure.Code = "stream_delivery_timeout"
		failure.Message = "The request deadline expired before response delivery was confirmed."
	case errors.Is(cause, errStreamDeliveryUnconfirmed):
		failure.Code = "stream_delivery_unconfirmed"
		failure.Message = "The response completed, but delivery of its terminal event to the client was not confirmed."
	}
	key, _ := contexts.GetChannelAPIKey(ts.ctx)
	failure.Message += " " + sanitizeDiagnosticText(ExtractErrorMessage(cause), key)
	return ts.requestService.UpdateRequestStatusFromError(ctx, ts.request.ID, cause, failure)
}

func persistRequestFailure(ctx context.Context, state *PersistenceState, err error) {
	stage := state.FailureStage
	if stage == "" {
		stage = "admission"
	}
	key, _ := contexts.GetChannelAPIKey(ctx)
	failure := &objects.RequestFailure{Stage: stage, Message: sanitizeDiagnosticText(ExtractErrorMessage(err), key)}
	if response, ok := errors.AsType[*llm.ResponseError](err); ok {
		failure.Code, failure.Type = response.Detail.Code, response.Detail.Type
		if failure.Code == "continuation_origin_persistence_failed" {
			failure.Stage = "origin_persistence"
		} else if strings.HasPrefix(failure.Code, "continuation_") {
			failure.Stage = "continuation"
		}
	}
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()
	if updateErr := state.RequestService.UpdateRequestStatusFromError(persistCtx, state.Request.ID, err, failure); updateErr != nil {
		log.Warn(persistCtx, "Failed to persist request failure", log.Cause(updateErr))
	}
}

// A provider-specific 400 must match the observed size rejection, rather than
// treating all invalid requests as oversized. The original error stays intact.
func isRequestSizeLimitError(err error) bool {
	status := ExtractStatusCodeFromError(err)
	if status == http.StatusRequestEntityTooLarge {
		return true
	}
	if status != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(ExtractErrorMessage(err))
	return strings.Contains(message, "image relay inline images exceed the configured") &&
		strings.Contains(message, "request limit")
}

func isUpstreamTimeoutError(err error) bool {
	status := ExtractStatusCodeFromError(err)
	return status == http.StatusGatewayTimeout || status == 524
}

func requestFailureCode(err error) string {
	switch {
	case isRequestSizeLimitError(err):
		return "upstream_request_too_large"
	case errors.Is(err, pipeline.ErrStreamFirstEventTimeout):
		return "upstream_first_event_timeout"
	case errors.Is(err, pipeline.ErrNonStreamResponseTimeout):
		return "upstream_response_timeout"
	case isUpstreamTimeoutError(err):
		return "upstream_timeout"
	default:
		return ""
	}
}

// Classify for persistence and the final client error, never as input to retry
// decisions. A canceled client is not an upstream timeout.
func classifyRequestFailure(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded) {
		code := "request_deadline_exceeded"
		message := "The request exceeded the gateway's overall time limit."
		// Non-stream attempts use their own deadline context. Its explicit cause
		// distinguishes that limit from the parent request deadline before the
		// pipeline converts the error for its retry loop.
		if errors.Is(context.Cause(ctx), pipeline.ErrNonStreamResponseTimeout) {
			code = "upstream_response_timeout"
			message = "The upstream provider did not respond within the configured time limit."
		}
		return &llm.ResponseError{StatusCode: http.StatusGatewayTimeout, Cause: err, Detail: llm.ErrorDetail{
			Code: code, Type: "timeout_error", Message: message,
		}}
	}
	code := requestFailureCode(err)
	if code == "" {
		return err
	}
	if response, ok := errors.AsType[*llm.ResponseError](err); ok {
		timeoutMessage := code == "upstream_timeout" && (response.Detail.Message == "" || strings.HasPrefix(http.DetectContentType([]byte(response.Detail.Message)), "text/html"))
		if response.Detail.Code != "" && !timeoutMessage {
			return err
		}
		copy := *response
		if copy.Detail.Code == "" {
			copy.Detail.Code = code
		}
		if timeoutMessage {
			copy.Detail.Message = "The upstream provider timed out before returning a response."
		}
		copy.Cause = err
		return &copy
	}
	if errors.Is(err, pipeline.ErrStreamFirstEventTimeout) || errors.Is(err, pipeline.ErrNonStreamResponseTimeout) {
		return &llm.ResponseError{StatusCode: http.StatusGatewayTimeout, Cause: err, Detail: llm.ErrorDetail{
			Code: code, Type: "timeout_error", Message: "The upstream provider did not respond within the configured time limit.",
		}}
	}
	return err
}
