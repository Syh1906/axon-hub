package contexts

import (
	"context"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

type streamFormatKey struct{}

// WithStreamFormat identifies the client protocol that will actually be written.
func WithStreamFormat(ctx context.Context, format llm.APIFormat) context.Context {
	return context.WithValue(ctx, streamFormatKey{}, format)
}

// StreamFormat returns the protocol of a measured generation endpoint.
func StreamFormat(ctx context.Context) llm.APIFormat {
	if shared.IsResponsesAPI(ctx) {
		return llm.APIFormatOpenAIResponse
	}
	f, _ := ctx.Value(streamFormatKey{}).(llm.APIFormat)
	return f
}

func hasStreamText(v gjson.Result) bool { return v.Type == gjson.String && len(v.Raw) > 2 }

// WroteStreamContent measures the first usable content only after a successful writer call.
func (c *RequestClock) WroteStreamContent(format llm.APIFormat, e *httpclient.StreamEvent) {
	if c == nil || e == nil {
		return
	}
	if format == llm.APIFormatOpenAIResponse {
		c.WroteResponsesEvent(e)
		return
	}
	if c.firstWriteContent.Load() != nil {
		return
	}
	kind := ""
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		gjson.GetBytes(e.Data, "choices").ForEach(func(_, choice gjson.Result) bool {
			d := choice.Get("delta")
			if !d.IsObject() {
				d = choice.Get("message")
			}
			for _, f := range [...]string{"content", "reasoning_content", "reasoning", "refusal"} {
				v := d.Get(f)
				if v.Type == gjson.String && v.String() != "" {
					kind = "chat." + f
					return false
				}
				if f == "content" && v.IsArray() {
					v.ForEach(func(_, part gjson.Result) bool {
						if text := part.Get("text"); text.Type == gjson.String && text.String() != "" {
							kind = "chat.content"
						}
						return kind == ""
					})
					if kind != "" {
						return false
					}
				}
			}
			d.Get("tool_calls").ForEach(func(_, tool gjson.Result) bool {
				if hasStreamText(tool.Get("function.arguments")) || hasStreamText(tool.Get("custom.input")) {
					kind = "chat.tool"
				}
				return kind == ""
			})
			if hasStreamText(d.Get("function_call.arguments")) {
				kind = "chat.tool"
			}
			return kind == ""
		})
	case llm.APIFormatAnthropicMessage:
		event := gjson.GetBytes(e.Data, "type").String()
		if event == "content_block_delta" || event == "content_block_start" {
			d := gjson.GetBytes(e.Data, "delta")
			if event == "content_block_start" {
				d = gjson.GetBytes(e.Data, "content_block")
				if d.Get("type").String() == "tool_use" && d.Get("input").IsObject() {
					d.Get("input").ForEach(func(_, _ gjson.Result) bool {
						kind = "anthropic.tool"
						return false
					})
				}
			}
			for _, f := range [...]string{"text", "thinking", "partial_json"} {
				v := d.Get(f)
				if v.Type == gjson.String && v.String() != "" {
					kind = "anthropic." + f
					break
				}
			}
		}
	case llm.APIFormatGeminiContents:
		gjson.GetBytes(e.Data, "candidates").ForEach(func(_, candidate gjson.Result) bool {
			candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
				switch {
				case hasStreamText(part.Get("text")) && part.Get("thought").Bool():
					kind = "gemini.thought"
				case hasStreamText(part.Get("text")):
					kind = "gemini.text"
				case hasStreamText(part.Get("functionCall.name")):
					kind = "gemini.function_call"
				case hasStreamText(part.Get("inlineData.data")):
					kind = "gemini.inline_data"
				}
				return kind == ""
			})
			return kind == ""
		})
	}
	if kind != "" {
		c.firstWriteContent.CompareAndSwap(nil, &TimedStreamEvent{At: time.Now(), Type: kind, Bytes: len(e.Data)})
	}
}
