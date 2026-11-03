package responses

import "github.com/looplj/axonhub/llm"

const responsesToolCallCompletedMetadataKey = "responses_tool_call_completed"

func failedResponseError(response *Response) *llm.ResponseError {
	detail := llm.ErrorDetail{Type: "upstream_response_error", Code: "failed", Message: "upstream response ended with status failed"}
	if response != nil && response.Error != nil {
		if response.Error.Type != "" {
			detail.Type = response.Error.Type
		}
		if response.Error.Code != "" {
			detail.Code = response.Error.Code
		}
		if response.Error.Message != "" {
			detail.Message = response.Error.Message
		}
		detail.Param = response.Error.Param
	}
	return &llm.ResponseError{StatusCode: 502, Detail: detail}
}

// The terminal event must agree with the response status, including when the
// upstream response passed through another API format before reaching us.
func responseTerminalEventType(status *string) StreamEventType {
	if status != nil {
		switch *status {
		case "failed":
			return StreamEventTypeResponseFailed
		case "incomplete":
			return StreamEventTypeResponseIncomplete
		case "canceled", "cancelled":
			return StreamEventTypeResponseCancelled
		}
	}
	return StreamEventTypeResponseCompleted
}
