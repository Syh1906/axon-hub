package orchestrator

import (
	"context"
	"errors"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// responseStatusCanceledAlternate preserves an accepted provider wire value.
const responseStatusCanceledAlternate = "cancelled" //nolint:misspell // Preserve the provider protocol spelling.

func (s *PersistenceState) observeStreamTerminal(event *httpclient.StreamEvent) {
	if err := streamTerminalError(event); err != nil {
		s.StreamTerminalError = err
		s.StreamCompleted = false
	} else if s.StreamTerminalError == nil && IsTerminalStreamEvent(event) {
		s.StreamCompleted = true
	}
}

func streamTerminalError(event *httpclient.StreamEvent) error {
	if event == nil {
		return nil
	}
	kind := event.Type
	if kind == "" {
		kind = gjson.GetBytes(event.Data, "type").String()
	}
	status := gjson.GetBytes(event.Data, "response.status").String()
	if status == "" {
		switch kind {
		case "response.failed":
			status = "failed"
		case "response.incomplete":
			status = "incomplete"
		case "response." + responseStatusCanceledAlternate, "response.canceled":
			status = responseStatusCanceledAlternate
		}
	}
	// Converted chat-completion streams also carry explicit failure reasons.
	if status == "" {
		for _, choice := range gjson.GetBytes(event.Data, "choices").Array() {
			switch choice.Get("finish_reason").String() {
			case "error":
				status = "failed"
			case responseStatusCanceledAlternate, "canceled":
				status = responseStatusCanceledAlternate
			}
		}
	}
	message := gjson.GetBytes(event.Data, "response.error.message").String()
	return responseStatusError(status, llm.ErrorDetail{
		Message: message,
		Code:    gjson.GetBytes(event.Data, "response.error.code").String(),
		Type:    gjson.GetBytes(event.Data, "response.error.type").String(),
		Param:   gjson.GetBytes(event.Data, "response.error.param").String(),
	})
}

// Read provider status before protocol conversion can replace it with a tool-call
// or length finish reason. Other APIs keep their existing completion semantics.
func nonStreamingResponseError(format llm.APIFormat, body []byte) error {
	if !isResponsesFormat(format) {
		return nil
	}
	return responseStatusError(gjson.GetBytes(body, "status").String(), llm.ErrorDetail{
		Message: gjson.GetBytes(body, "error.message").String(),
		Code:    gjson.GetBytes(body, "error.code").String(),
		Type:    gjson.GetBytes(body, "error.type").String(),
		Param:   gjson.GetBytes(body, "error.param").String(),
	})
}

func responseStatusError(status string, detail llm.ErrorDetail) error {
	if status != "failed" && status != "incomplete" && status != "canceled" && status != responseStatusCanceledAlternate {
		return nil
	}
	if detail.Message == "" {
		detail.Message = "upstream response ended with status " + status
	}
	if detail.Code == "" {
		detail.Code = status
	}
	if detail.Type == "" {
		detail.Type = "upstream_response_error"
	}
	err := &llm.ResponseError{StatusCode: 502, Detail: detail}
	if status == "canceled" || status == responseStatusCanceledAlternate {
		return errors.Join(context.Canceled, err)
	}
	return err
}
