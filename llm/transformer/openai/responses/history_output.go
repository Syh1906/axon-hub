package responses

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// NormalizeHistoryItem converts an item to the representation accepted in HTTP
// Responses history. Web search calls retain their protocol-defined status.
// Unchanged items share the caller's bytes.
func NormalizeHistoryItem(item json.RawMessage) (json.RawMessage, bool) {
	if !bytes.Contains(item, []byte(`"status"`)) && !bytes.Contains(item, []byte(`\`)) {
		return item, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(item, &fields) != nil || fields["status"] == nil {
		return item, false
	}
	var kind string
	_ = json.Unmarshal(fields["type"], &kind)
	if kind == "web_search_call" {
		return item, false
	}
	delete(fields, "status")
	normalized, err := json.Marshal(fields)
	if err != nil {
		return item, false
	}
	return normalized, true
}

// HistoryOutput tracks output indexes actually emitted before the terminal.
// The transport needs only digests. A history owner may also retain completed
// items, within its existing budget, to repair a sparse relay terminal.
type HistoryOutput struct {
	items      map[int]historyItem
	invalid    bool
	deltaPaths int
}

type historyDelta struct {
	hash.Hash
	bytes int
}

type historyItem struct {
	digest    [32]byte
	done      bool
	raw       json.RawMessage
	itemType  string
	itemID    string
	deltas    map[string]*historyDelta
	completed map[string][32]byte
}

func (h *HistoryOutput) Observe(kind string, data []byte, retain func(int) bool) {
	if h.invalid {
		return
	}
	isItem := kind == "response.output_item.added" || kind == "response.output_item.done"
	isDelta := strings.HasSuffix(kind, ".delta")
	path, value := historyCompletedField(kind, data)
	if !isItem && !isDelta && path == "" {
		return
	}
	idx := gjson.GetBytes(data, "output_index")
	n := int(idx.Int())
	if n < 0 || n >= 1024 || (!idx.Exists() && len(h.items) > 1) {
		h.invalid = true
		return
	}
	if h.items == nil {
		h.items = make(map[int]historyItem)
	}
	item := h.items[n]
	if id := gjson.GetBytes(data, "item_id").String(); id != "" {
		if item.itemID != "" && item.itemID != id {
			h.invalid = true
			return
		}
		item.itemID = id
	}
	if kind == "response.output_item.added" || kind == "response.output_item.done" {
		i := gjson.GetBytes(data, "item")
		if id := i.Get("id").String(); id != "" {
			if item.itemID != "" && item.itemID != id {
				h.invalid = true
				return
			}
			item.itemID = id
		}
		if typ := i.Get("type").String(); typ != "" {
			if item.itemType != "" && item.itemType != typ {
				h.invalid = true
				return
			}
			item.itemType = typ
		}
	}
	if path != "" {
		if item.completed == nil {
			item.completed = make(map[string][32]byte)
		}
		digest := sha256.Sum256([]byte(value))
		if prev, exists := item.completed[path]; exists && prev != digest {
			h.invalid = true
			return
		} else if !exists {
			if h.deltaPaths >= 1024 {
				h.invalid = true
				return
			}
			h.deltaPaths++
		}
		item.completed[path] = digest
	}
	if kind == "response.output_item.done" {
		raw := gjson.GetBytes(data, "item")
		if !raw.IsObject() {
			h.invalid = true
			return
		}
		digest := sha256.Sum256([]byte(raw.Raw))
		if item.done && item.digest != digest {
			h.invalid = true
			return
		}
		if !item.done && retain != nil && retain(len(raw.Raw)) {
			item.raw = json.RawMessage(raw.Raw)
		}
		item.done, item.digest = true, digest
	} else if strings.HasSuffix(kind, ".delta") {
		// Verify a terminal that declares a different value from emitted
		// deltas, including partial multi-part answers, before reusing it.
		var path string
		index := gjson.GetBytes(data, "content_index").Int()
		switch kind {
		case "response.output_text.delta":
			path = fmt.Sprintf("content.%d.text", index)
		case "response.refusal.delta":
			path = fmt.Sprintf("content.%d.refusal", index)
		case "response.reasoning_text.delta":
			path = fmt.Sprintf("content.%d.text", index)
		case "response.reasoning_summary_text.delta":
			path = fmt.Sprintf("summary.%d.text", gjson.GetBytes(data, "summary_index").Int())
		case "response.function_call_arguments.delta":
			path = "arguments"
		case "response.custom_tool_call_input.delta":
			path = "input"
		default:
			h.invalid = true
			return
		}
		if item.deltas == nil {
			item.deltas = make(map[string]*historyDelta)
		}
		if item.deltas[path] == nil {
			if h.deltaPaths >= 1024 {
				h.invalid = true
				return
			}
			h.deltaPaths++
			item.deltas[path] = &historyDelta{Hash: sha256.New()}
		}
		delta := []byte(gjson.GetBytes(data, "delta").String())
		item.deltas[path].Write(delta)
		item.deltas[path].bytes += len(delta)
	}
	h.items[n] = item
}

// Some relays only emit a completed part or field, with no delta or item.done.
func historyCompletedField(kind string, data []byte) (string, string) {
	var field, source, section string
	indexField := "content_index"
	switch kind {
	case "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		field = "text"
		if gjson.GetBytes(data, "part.type").String() == "refusal" {
			field = "refusal"
		}
		source = "part." + field
		section = "content"
		if strings.HasPrefix(kind, "response.reasoning_summary_part.") {
			section = "summary"
			indexField = "summary_index"
		}
	case "response.output_text.done", "response.reasoning_text.done":
		section, field = "content", "text"
	case "response.reasoning_summary_text.done":
		section, field, indexField = "summary", "text", "summary_index"
	case "response.refusal.done":
		section, field = "content", "refusal"
	case "response.function_call_arguments.done":
		field = "arguments"
	case "response.custom_tool_call_input.done":
		field = "input"
	default:
		return "", ""
	}
	if source == "" {
		source = field
	}
	v := gjson.GetBytes(data, source)
	if v.Type != gjson.String || v.String() == "" {
		return "", ""
	}
	if section != "" {
		field = fmt.Sprintf("%s.%d.%s", section, gjson.GetBytes(data, indexField).Int(), field)
	}
	return field, v.String()
}

// Covers reports whether the supplied array includes everything observed on the stream.
func (h *HistoryOutput) Covers(output gjson.Result) bool {
	if h.invalid || !output.IsArray() {
		return false
	}
	items := output.Array()
	for n, item := range h.items {
		if n >= len(items) || !item.matches(items[n]) {
			return false
		}
	}
	return true
}

func (item historyItem) matches(value gjson.Result) bool {
	if !value.IsObject() || item.itemType != "" && value.Get("type").String() != item.itemType ||
		item.itemID != "" && value.Get("id").String() != item.itemID {
		return false
	}
	if item.done && sha256.Sum256([]byte(value.Raw)) != item.digest {
		return false
	}
	for path, digest := range item.completed {
		v := value.Get(path)
		if v.Type != gjson.String || sha256.Sum256([]byte(v.String())) != digest {
			return false
		}
	}
	for path, digest := range item.deltas {
		v := value.Get(path)
		value := v.String()
		_, complete := item.completed[path]
		if v.Type != gjson.String || len(value) < digest.bytes || len(value) != digest.bytes && !complete && !item.done {
			return false
		}
		sum := sha256.Sum256([]byte(value[:digest.bytes]))
		if !bytes.Equal(digest.Sum(nil), sum[:]) {
			return false
		}
	}
	return true
}

func (h *HistoryOutput) Complete(body []byte) ([]byte, bool) {
	out := gjson.GetBytes(body, "output")
	if h.Covers(out) {
		return body, true
	}
	if h.invalid || out.Exists() && !out.IsArray() {
		return nil, false
	}
	items := out.Array()
	count := len(items)
	for n := range h.items {
		count = max(count, n+1)
	}
	if count <= len(items) {
		return nil, false
	}
	repaired := make([]json.RawMessage, count)
	for n := range repaired {
		observed, ok := h.items[n]
		if n < len(items) {
			if ok && !observed.matches(items[n]) {
				return nil, false
			}
			repaired[n] = json.RawMessage(items[n].Raw)
		} else {
			if !ok || len(observed.raw) == 0 || !observed.matches(gjson.ParseBytes(observed.raw)) {
				return nil, false
			}
			repaired[n] = observed.raw
		}
	}
	raw, err := json.Marshal(repaired)
	if err != nil {
		return nil, false
	}
	result, err := sjson.SetRawBytes(body, "output", raw)
	return result, err == nil
}
