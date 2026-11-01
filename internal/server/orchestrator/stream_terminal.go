package orchestrator

import (
	"context"
	"errors"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

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
		case "response.cancelled", "response.canceled":
			status = "cancelled"
		}
	}
	// Converted chat-completion streams also carry explicit failure reasons.
	if status == "" {
		for _, choice := range gjson.GetBytes(event.Data, "choices").Array() {
			switch choice.Get("finish_reason").String() {
			case "error":
				status = "failed"
			case "cancelled", "canceled":
				status = "cancelled"
			}
		}
	}
	message := gjson.GetBytes(event.Data, "response.error.message").String()
	return responseStatusError(status, message)
}

// Read provider status before protocol conversion can replace it with a tool-call
// or length finish reason. Other APIs keep their existing completion semantics.
func nonStreamingResponseError(format llm.APIFormat, body []byte) error {
	if !isResponsesFormat(format) {
		return nil
	}
	return responseStatusError(gjson.GetBytes(body, "status").String(), gjson.GetBytes(body, "error.message").String())
}

func responseStatusError(status, message string) error {
	if status != "failed" && status != "incomplete" && status != "canceled" && status != "cancelled" {
		return nil
	}
	if message == "" {
		message = "upstream response ended with status " + status
	}
	err := &llm.ResponseError{StatusCode: 502, Detail: llm.ErrorDetail{Type: "upstream_response_error", Code: status, Message: message}}
	if status == "canceled" || status == "cancelled" {
		return errors.Join(context.Canceled, err)
	}
	return err
}
