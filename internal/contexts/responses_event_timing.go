package contexts

import (
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/httpclient"
)

// These observations complement the normalized model-content clock. A stream
// that only returns completed items has no delta observation.
func (a *AttemptClock) ObserveResponsesEvent(event *httpclient.StreamEvent) {
	if a == nil || event == nil || a.firstDelta.Load() != nil {
		return
	}
	kind, delta := responsesEventKind(event)
	if a.firstEvent.Load() != nil && !delta {
		return
	}
	observation := &TimedStreamEvent{At: time.Now(), Type: kind, Bytes: len(event.Data)}
	a.firstEvent.CompareAndSwap(nil, observation)
	if delta {
		a.firstDelta.CompareAndSwap(nil, observation)
	}
}

func (c *RequestClock) WroteResponsesEvent(event *httpclient.StreamEvent) {
	if c == nil || event == nil || c.firstWriteDelta.Load() != nil {
		return
	}
	if kind, delta := responsesEventKind(event); delta {
		c.firstWriteDelta.CompareAndSwap(nil, &TimedStreamEvent{At: time.Now(), Type: kind, Bytes: len(event.Data)})
	}
}

func responsesEventKind(event *httpclient.StreamEvent) (string, bool) {
	kind := gjson.GetBytes(event.Data, "type").String()
	if kind == "" {
		kind = event.Type
	}
	switch kind {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
		"response.function_call_arguments.delta", "response.custom_tool_call_input.delta", "response.refusal.delta":
		delta := gjson.GetBytes(event.Data, "delta")
		return kind, delta.Type == gjson.String && len(delta.Raw) > 2
	case "response.created", "response.metadata", "response.queued", "response.in_progress", "response.completed", "response.failed",
		"response.done", "response.cancelled", "response.canceled",
		"response.incomplete", "response.content_part.added", "response.output_item.added", "response.output_item.done", "error":
		return kind, false
	default:
		return "other", false
	}
}
