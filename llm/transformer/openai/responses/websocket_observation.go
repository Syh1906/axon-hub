package responses

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/httpclient"
)

func reportWebSocketFailure(policy *httpclient.ResponsesWebSocketPolicy, stage string, err error) {
	if policy == nil || policy.Observe == nil {
		return
	}
	result := httpclient.WebSocketObservation{Stage: stage, Reason: "connection_failed"}
	var response *httpclient.Error
	var closed *websocket.CloseError
	switch {
	case errors.As(err, &response):
		result.Status = response.StatusCode
		switch response.StatusCode {
		case http.StatusBadRequest:
			result.Reason = "handshake_bad_request"
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusUpgradeRequired:
			result.Reason = "upgrade_rejected"
		case http.StatusUnauthorized, http.StatusForbidden:
			result.Reason = "auth_error"
		case http.StatusTooManyRequests:
			result.Reason = "rate_limited"
			result.RetryAfter, _ = httpclient.ParseRetryAfter(err)
		}
	case errors.As(err, &closed):
		result.Status = closed.Code
		if closed.Code == websocket.CloseTryAgainLater && strings.EqualFold(strings.TrimSpace(closed.Text), "no available account") {
			result.Reason = "no_available_account"
		} else {
			result.Reason = "connection_closed"
		}
	}
	policy.Observe(result)
}

type webSocketEvidence struct {
	policy      *httpclient.ResponsesWebSocketPolicy
	id          string
	content     bool
	conflicting bool
}

func (e *webSocketEvidence) event(typ string, body []byte) {
	if e.policy == nil || e.policy.Observe == nil {
		return
	}
	switch typ {
	case "response.created":
		id := gjson.GetBytes(body, "response.id").String()
		if e.id != "" && id != e.id {
			e.conflicting = true
		}
		e.id = id
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta":
		if e.content {
			return
		}
		if id := gjson.GetBytes(body, "response_id").String(); e.id != "" && (id == "" || id == e.id) && gjson.GetBytes(body, "delta").String() != "" {
			e.content = true
		}
	case "response.completed", "response.done":
		res := gjson.GetBytes(body, "response")
		id := res.Get("id").String()
		reason := "empty_output"
		if !e.conflicting && id != "" && (e.id == "" || e.id == id) && res.Get("status").String() == "completed" && (e.content || meaningfulWSOutput(res.Get("output"))) {
			reason = "verified"
		}
		e.policy.Observe(httpclient.WebSocketObservation{Stage: "model", Reason: reason})
	case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		e.policy.Observe(httpclient.WebSocketObservation{Stage: "model", Reason: "model_failed"})
	}
}

func meaningfulWSOutput(output gjson.Result) bool {
	found := false
	output.ForEach(func(_, item gjson.Result) bool {
		switch item.Get("type").String() {
		case "function_call", "custom_tool_call":
			found = item.Get("name").String() != "" && item.Get("call_id").String() != ""
		case "message", "reasoning":
			for _, field := range []string{"content", "summary"} {
				item.Get(field).ForEach(func(_, part gjson.Result) bool {
					found = part.Get("text").String() != "" || part.Get("refusal").String() != ""
					return !found
				})
				if found {
					break
				}
			}
		}
		return !found
	})
	return found
}
