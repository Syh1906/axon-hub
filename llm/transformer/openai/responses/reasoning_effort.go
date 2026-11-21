package responses

import (
	"strings"

	"github.com/tidwall/gjson"
)

// EffectiveReasoningEffort reads a wire request without decoding or copying its full history.
func EffectiveReasoningEffort(body []byte) string {
	effort := ""
	if top := gjson.GetBytes(body, "reasoning.effort"); top.Type == gjson.String {
		effort = strings.TrimSpace(top.Str)
	}
	updates := gjson.GetBytes(body, `input.#(type=="configuration_update")#.reasoning.effort`)
	updates.ForEach(func(_, value gjson.Result) bool {
		if v := strings.TrimSpace(value.Str); value.Type == gjson.String && v != "" {
			effort = v
		}
		return true
	})
	if effort == "" {
		if budget := gjson.GetBytes(body, "reasoning.max_tokens"); budget.Type == gjson.Number {
			return budget.String()
		}
	}
	return effort
}

// ReportedReasoningEffort accepts only the provider's own completed response.
func ReportedReasoningEffort(body []byte, kind string, stream bool) string {
	path := ""
	if stream {
		if kind == "" {
			kind = gjson.GetBytes(body, "type").String()
		}
		if kind != "response.completed" && kind != "response.done" {
			return ""
		}
		path = "response."
	}
	if status := gjson.GetBytes(body, path+"status").String(); status != "" && status != "completed" {
		return ""
	}
	effort := gjson.GetBytes(body, path+"reasoning.effort")
	if effort.Type != gjson.String || effort.Str == "" {
		return ""
	}
	return effort.Str
}
