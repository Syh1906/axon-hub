package contexts

import (
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// Protocols without a native event classifier still expose normalized output.
// Keep Responses on its strict protocol rules even for unknown/terminal frames.
func (a *AttemptClock) ObserveNormalizedOutput(format llm.APIFormat) {
	if a == nil {
		return
	}
	switch format {
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket, llm.APIFormatAnthropicMessage, llm.APIFormatOpenAIChatCompletion, llm.APIFormatGeminiContents:
		return
	}
	a.semantic.CompareAndSwap(nil, &TimedStreamEvent{Type: "model.output", At: time.Now()})
}

// ObserveOutputProgress records an upstream event without committing the stream
// or changing delivery, retry, quota or channel health decisions.
func (a *AttemptClock) ObserveOutputProgress(format llm.APIFormat, e *httpclient.StreamEvent) {
	if a == nil || e == nil || a.semantic.Load() != nil {
		return
	}
	kind := ""
	switch format {
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket:
		event, delta := responsesEventKind(e)
		if event == "response.output_item.added" {
			switch item := gjson.GetBytes(e.Data, "item.type").String(); item {
			case "reasoning", "message", "function_call", "custom_tool_call", "web_search_call", "file_search_call", "computer_call", "code_interpreter_call", "image_generation_call":
				kind = event + "." + item
			}
		}
		if kind == "" && (delta || wholeResponsesContent(event, e.Data)) {
			kind = event
		}
	case llm.APIFormatGeminiContents:
		kind, _ = geminiStreamContent(e, true)
	case llm.APIFormatAnthropicMessage:
		if gjson.GetBytes(e.Data, "type").String() == "content_block_start" {
			switch block := gjson.GetBytes(e.Data, "content_block.type").String(); block {
			case "thinking", "text", "tool_use", "server_tool_use":
				kind = "anthropic." + block
			}
		}
	case llm.APIFormatOpenAIChatCompletion:
		gjson.GetBytes(e.Data, "choices").ForEach(func(_, c gjson.Result) bool {
			d := c.Get("delta")
			if hasStreamText(d.Get("function_call.name")) || hasStreamText(d.Get("tool_calls.0.function.name")) || hasStreamText(d.Get("tool_calls.0.custom.name")) {
				kind = "chat.tool"
			}
			return kind == ""
		})
	}
	if kind == "" && format != llm.APIFormatOpenAIResponse && format != llm.APIFormatOpenAIResponseWebSocket {
		kind = streamContentKind(format, e)
	}
	if kind != "" {
		a.semantic.CompareAndSwap(nil, &TimedStreamEvent{Type: kind, At: time.Now(), Bytes: len(e.Data)})
	}
}

func responsesAnswer(kind string, data []byte, delta bool) bool {
	switch kind {
	case "response.output_text.delta":
		return delta
	case "response.output_text.done":
		return hasStreamText(gjson.GetBytes(data, "text"))
	case "response.content_part.added", "response.content_part.done":
		return gjson.GetBytes(data, "part.type").String() == "output_text" && hasStreamText(gjson.GetBytes(data, "part.text"))
	case "response.output_item.added", "response.output_item.done":
		return itemHasAnswer(gjson.GetBytes(data, "item"))
	case "response.completed", "response.done", "response.incomplete":
		found := false
		gjson.GetBytes(data, "response.output").ForEach(func(_, item gjson.Result) bool {
			found = itemHasAnswer(item)
			return !found
		})
		return found
	}
	return false
}

func itemHasAnswer(item gjson.Result) bool {
	if item.Get("type").String() != "message" {
		return false
	}
	found := false
	item.Get("content").ForEach(func(_, part gjson.Result) bool {
		found = part.Get("type").String() == "output_text" && hasStreamText(part.Get("text"))
		return !found
	})
	return found
}
