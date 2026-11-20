package contexts

import (
	"bytes"
	"crypto/sha256"
	"hash"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/tidwall/gjson"
)

// Measure only text written to one answer. Repeated full-text summaries do
// not count as more batches. Ambiguous or multi-answer streams expose no rate.
type answerDelivery struct {
	batches                  int
	first, last              time.Time
	afterFirstTokens         int
	missingTokens, ambiguous bool
	output, part             int64
	bytes                    int
	digest                   hash.Hash
}

func (c *RequestClock) observeAnswer(format llm.APIFormat, e *httpclient.StreamEvent, kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := &c.answerDelivery
	if d.ambiguous || d.batches > 1 && d.missingTokens {
		return
	}
	now := time.Now()
	data := gjson.ParseBytes(e.Data)
	switch format {
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseWebSocket:
		output, part := data.Get("output_index").Int(), data.Get("content_index").Int()
		observePart := func(p gjson.Result, o, i int64) {
			if p.Get("type").String() == "output_text" {
				d.observe(p.Get("text"), p.Get("logprobs"), o, i, true, now)
			}
		}
		observeItem := func(item gjson.Result, o int64) {
			if item.Get("type").String() != "message" {
				return
			}
			item.Get("content").ForEach(func(i, p gjson.Result) bool { observePart(p, o, i.Int()); return !d.ambiguous })
		}
		switch kind {
		case "response.output_text.delta", "response.output_text.done":
			field := "delta"
			if kind == "response.output_text.done" {
				field = "text"
			}
			d.observe(data.Get(field), data.Get("logprobs"), output, part, field == "text", now)
		case "response.content_part.added", "response.content_part.done":
			observePart(data.Get("part"), output, part)
		case "response.output_item.added", "response.output_item.done":
			observeItem(data.Get("item"), output)
		case "response.completed", "response.done", "response.incomplete":
			data.Get("response.output").ForEach(func(i, v gjson.Result) bool { observeItem(v, i.Int()); return !d.ambiguous })
		}
	case llm.APIFormatOpenAIChatCompletion:
		data.Get("choices").ForEach(func(_, ch gjson.Result) bool {
			msg := ch.Get("delta")
			full := !msg.IsObject()
			if full {
				msg = ch.Get("message")
			}
			v := msg.Get("content")
			if v.IsArray() {
				d.ambiguous = true
				return false
			}
			if !v.Exists() {
				v = ch.Get("text")
				full = false
			}
			d.observe(v, ch.Get("logprobs.content"), ch.Get("index").Int(), 0, full, now)
			return !d.ambiguous
		})
	case llm.APIFormatAnthropicMessage:
		if kind == "content_block_delta" {
			d.observe(data.Get("delta.text"), gjson.Result{}, data.Get("index").Int(), 0, false, now)
		}
		if kind == "content_block_start" {
			d.observe(data.Get("content_block.text"), gjson.Result{}, data.Get("index").Int(), 0, false, now)
		}
	case llm.APIFormatGeminiContents:
		data.Get("candidates").ForEach(func(_, candidate gjson.Result) bool {
			// All text parts in this candidate are written together in this frame.
			var hasText bool
			candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
				if v := part.Get("text"); !part.Get("thought").Bool() && hasStreamText(v) {
					hasText = true
				}
				return !hasText
			})
			if hasText {
				i := candidate.Get("index").Int()
				if d.batches > 0 && d.output != i {
					d.ambiguous = true
					return false
				}
				d.output = i
				d.add(now, 0)
			}
			return !d.ambiguous
		})
	}
}

func (d *answerDelivery) observe(text, probs gjson.Result, output, part int64, full bool, now time.Time) {
	if !hasStreamText(text) || d.ambiguous {
		return
	}
	if d.batches > 0 && (output != d.output || part != d.part) {
		d.ambiguous = true
		return
	}
	d.output, d.part = output, part
	wire := []byte(text.String())
	if d.digest == nil {
		d.digest = sha256.New()
	}
	if full && d.bytes > 0 {
		if len(wire) < d.bytes {
			d.ambiguous = true
			return
		}
		prefix := sha256.Sum256(wire[:d.bytes])
		if !bytes.Equal(prefix[:], d.digest.Sum(nil)) {
			d.ambiguous = true
			return
		}
		wire = wire[d.bytes:]
		if len(wire) == 0 {
			return
		}
		probs = gjson.Result{} // full-item logprobs include the earlier prefix
	}
	d.digest.Write(wire)
	d.bytes += len(wire)
	// Require the reported token pieces to cover exactly the text of this batch.
	count, offset := 0, 0
	probs.ForEach(func(_, entry gjson.Result) bool {
		token := entry.Get("token")
		if token.Type != gjson.String || token.String() == "" || !bytes.HasPrefix(wire[offset:], []byte(token.String())) {
			count = 0
			return false
		}
		offset += len(token.String())
		count++
		return true
	})
	if offset != len(wire) || !probs.IsArray() {
		count = 0
	}
	d.add(now, count)
}

func (d *answerDelivery) add(now time.Time, tokens int) {
	d.batches++
	if d.batches == 1 {
		d.first = now
	} else {
		d.afterFirstTokens += tokens
	}
	d.last = now
	d.missingTokens = d.missingTokens || tokens == 0
}

func (d *answerDelivery) result() (bool, *float64) {
	if d.ambiguous {
		return false, nil
	}
	if d.batches == 1 {
		return true, nil
	}
	if d.batches < 2 || d.missingTokens || d.last.Sub(d.first) < time.Millisecond {
		return false, nil
	}
	rate := float64(d.afterFirstTokens) / d.last.Sub(d.first).Seconds()
	return false, &rate
}
