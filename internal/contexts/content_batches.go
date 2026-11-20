package contexts

import (
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// ContentBatches measures upstream events carrying new output on one execution.
// Observe is called serially by the stream reader, including during tail collection.
type ContentBatches struct {
	count       int
	first, last time.Time
	sizes       map[contentPart]int
}

type contentPart struct {
	output, part int64
	field        string
}

func (b *ContentBatches) Result() (*int, *int64) {
	if b.count == 0 {
		return nil, nil
	}
	count, span := b.count, b.last.Sub(b.first).Milliseconds()
	return &count, &span
}

func (b *ContentBatches) Observe(format llm.APIFormat, e *httpclient.StreamEvent, at time.Time) {
	if e == nil {
		return
	}
	var changed bool
	switch format {
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket:
		changed = b.responses(e)
	case llm.APIFormatOpenAIChatCompletion:
		// Copilot accepts Chat requests but can return Responses wire events.
		if strings.HasPrefix(gjson.GetBytes(e.Data, "type").String(), "response.") {
			changed = b.responses(e)
		} else {
			changed = b.chat(e)
		}
	case llm.APIFormatGeminiContents, llm.APIFormatAnthropicMessage:
		kind := streamContentKind(format, e)
		changed = kind != "" && kind != "gemini.inline_data"
	}
	if changed {
		if b.count == 0 {
			b.first = at
		}
		b.last = at
		b.count++
	}
}

// Protocol deltas append bytes. Completed items and terminal snapshots repeat
// earlier fields. Only a longer snapshot can add content to the same field.
func (b *ContentBatches) part(key contentPart, value gjson.Result, full bool) bool {
	if !hasStreamText(value) {
		return false
	}
	return b.addBytes(key, len(value.String()), full)
}

func (b *ContentBatches) addBytes(key contentPart, n int, full bool) bool {
	if b.sizes == nil {
		b.sizes = make(map[contentPart]int)
	}
	if full {
		if n <= b.sizes[key] {
			return false
		}
		b.sizes[key] = n
	} else {
		b.sizes[key] += n
	}
	return true
}

func (b *ContentBatches) responses(e *httpclient.StreamEvent) bool {
	d := gjson.ParseBytes(e.Data)
	kind := d.Get("type").String()
	if kind == "" {
		kind = e.Type
	}
	output, part := d.Get("output_index").Int(), d.Get("content_index").Int()
	var changed bool
	add := func(o, p int64, field string, v gjson.Result, full bool) {
		if b.part(contentPart{o, p, field}, v, full) {
			changed = true
		}
	}
	addPart := func(o, p int64, v gjson.Result) {
		field := v.Get("type").String()
		if field == "summary_text" {
			field = "reasoning_summary_text"
		}
		switch field {
		case "reasoning_text", "reasoning_summary_text", "output_text":
			add(o, p, field, v.Get("text"), true)
		case "refusal":
			add(o, p, field, v.Get("refusal"), true)
		}
	}
	addItem := func(o int64, item gjson.Result) {
		switch item.Get("type").String() {
		case "function_call":
			add(o, 0, "function_call_arguments", item.Get("arguments"), true)
		case "custom_tool_call":
			add(o, 0, "custom_tool_call_input", item.Get("input"), true)
		case "message", "reasoning":
			for _, field := range []string{"content", "summary"} {
				item.Get(field).ForEach(func(i, v gjson.Result) bool { addPart(o, i.Int(), v); return true })
			}
		}
	}
	switch kind {
	case "response.content_part.added", "response.content_part.done":
		addPart(output, part, d.Get("part"))
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		addPart(output, d.Get("summary_index").Int(), d.Get("part"))
	case "response.output_item.added", "response.output_item.done":
		addItem(output, d.Get("item"))
	case "response.completed", "response.done", "response.incomplete":
		d.Get("response.output").ForEach(func(i, v gjson.Result) bool { addItem(i.Int(), v); return true })
	default:
		fields := strings.Split(kind, ".")
		if len(fields) != 3 || fields[0] != "response" || (fields[2] != "delta" && fields[2] != "done") {
			return false
		}
		key := "text"
		switch fields[1] {
		case "reasoning_summary_text":
			part = d.Get("summary_index").Int()
		case "output_text", "reasoning_text":
		case "function_call_arguments":
			key = "arguments"
			part = 0
		case "custom_tool_call_input":
			key = "input"
			part = 0
		case "refusal":
			key = "refusal"
		default:
			return false
		}
		full := fields[2] == "done"
		if !full {
			key = "delta"
		}
		add(output, part, fields[1], d.Get(key), full)
	}
	return changed
}

func (b *ContentBatches) chat(e *httpclient.StreamEvent) bool {
	var changed bool
	gjson.GetBytes(e.Data, "choices").ForEach(func(_, choice gjson.Result) bool {
		d := choice.Get("delta")
		full := !d.IsObject()
		if full {
			d = choice.Get("message")
		}
		output := choice.Get("index").Int()
		add := func(part int64, field string, v gjson.Result, full bool) {
			if b.part(contentPart{output, part, field}, v, full) {
				changed = true
			}
		}
		for _, field := range []string{"content", "refusal", "function_call.arguments"} {
			v := d.Get(field)
			if field == "content" && v.IsArray() {
				v.ForEach(func(i, p gjson.Result) bool { add(i.Int(), field, p.Get("text"), full); return true })
			} else {
				add(0, field, v, full)
			}
		}
		// Providers may repeat the same reasoning under several aliases, including
		// a final message in a different representation. Track one logical field.
		detailBytes := 0
		d.Get("reasoning_details").ForEach(func(_, detail gjson.Result) bool {
			detailBytes += len(detail.Get("text").String())
			return true
		})
		if detailBytes > 0 {
			if b.addBytes(contentPart{output, 0, "reasoning"}, detailBytes, full) {
				changed = true
			}
		} else {
			for _, field := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
				if v := d.Get(field); hasStreamText(v) {
					add(0, "reasoning", v, full)
					break
				}
			}
		}
		d.Get("tool_calls").ForEach(func(i, t gjson.Result) bool {
			index := t.Get("index")
			if !index.Exists() {
				index = i
			}
			for _, field := range []string{"function.arguments", "custom.input"} {
				add(index.Int(), field, t.Get(field), full)
			}
			return true
		})
		add(0, "text", choice.Get("text"), false)
		return true
	})
	return changed
}
