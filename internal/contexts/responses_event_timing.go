package contexts

import (
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// ObserveResponsesEvent records the first event and first non-empty delta. A stream
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
	if c == nil || event == nil {
		return
	}
	kind, delta := responsesEventKind(event)
	c.observeAnswer(llm.APIFormatOpenAIResponse, event, kind)
	if c.firstWriteDelta.Load() != nil && c.firstAnswer.Load() != nil {
		return
	}
	if c.firstAnswer.Load() == nil && responsesAnswer(kind, event.Data, delta) {
		markClock(&c.firstAnswer)
	}
	if delta && c.firstWriteDelta.Load() == nil {
		observation := &TimedStreamEvent{At: time.Now(), Type: kind, Bytes: len(event.Data)}
		c.firstWriteContent.CompareAndSwap(nil, observation)
		c.firstWriteDelta.CompareAndSwap(nil, observation)
	} else if c.firstWriteContent.Load() == nil && wholeResponsesContent(kind, event.Data) {
		c.firstWriteContent.CompareAndSwap(nil, &TimedStreamEvent{At: time.Now(), Type: kind, Bytes: len(event.Data)})
	}
}

func wholeResponsesContent(kind string, data []byte) bool {
	switch kind {
	case "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		part := gjson.GetBytes(data, "part")
		return hasStreamText(part.Get("text")) || hasStreamText(part.Get("refusal"))
	case "response.output_text.done", "response.reasoning_text.done", "response.reasoning_summary_text.done":
		return hasStreamText(gjson.GetBytes(data, "text"))
	case "response.refusal.done":
		return hasStreamText(gjson.GetBytes(data, "refusal"))
	case "response.function_call_arguments.done":
		return hasStreamText(gjson.GetBytes(data, "arguments"))
	case "response.custom_tool_call_input.done":
		return hasStreamText(gjson.GetBytes(data, "input"))
	case "response.output_item.added", "response.output_item.done":
		return itemHasResponsesContent(gjson.GetBytes(data, "item"))
	case "response.completed", "response.done", "response.incomplete":
		found := false
		gjson.GetBytes(data, "response.output").ForEach(func(_, item gjson.Result) bool {
			found = itemHasResponsesContent(item)
			return !found
		})
		return found
	}
	return false
}

func itemHasResponsesContent(item gjson.Result) bool {
	switch item.Get("type").String() {
	case "function_call":
		return hasStreamText(item.Get("arguments"))
	case "custom_tool_call":
		return hasStreamText(item.Get("input"))
	case "message", "reasoning":
		for _, field := range []string{"content", "summary"} {
			found := false
			item.Get(field).ForEach(func(_, part gjson.Result) bool {
				found = hasStreamText(part.Get("text")) || hasStreamText(part.Get("refusal"))
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
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
	case "response.output_text.done", "response.reasoning_text.done", "response.reasoning_summary_text.done", "response.refusal.done",
		"response.function_call_arguments.done", "response.custom_tool_call_input.done",
		"response.created", "response.metadata", "response.queued", "response.in_progress", "response.completed", "response.failed",
		"response.done", "response.cancelled", "response.canceled",
		"response.incomplete", "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.output_item.added", "response.output_item.done", "error":
		return kind, false
	default:
		return "other", false
	}
}
