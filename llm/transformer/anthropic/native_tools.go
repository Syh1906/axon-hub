package anthropic

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer"
)

const nativeToolsKey = "anthropic_native_tools"

type nativeTool struct {
	Index int  `json:"index"`
	Tool  Tool `json:"tool"`
}

func (t *Tool) UnmarshalJSON(data []byte) error {
	type toolAlias Tool
	var decoded toolAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*t = Tool(decoded)
	if _, represented := convertToolToLLM(*t); !represented {
		t.Raw = slices.Clone(data)
	}
	return nil
}

func (t Tool) MarshalJSON() ([]byte, error) {
	if len(t.Raw) == 0 {
		type toolAlias Tool
		return json.Marshal(toolAlias(t))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(t.Raw, &fields); err != nil {
		return nil, err
	}
	// Cache optimization still owns cache_control; all other unmodeled fields
	// remain native rather than being approximated as a function tool.
	if t.CacheControl == nil {
		delete(fields, "cache_control")
	} else {
		fields["cache_control"], _ = json.Marshal(t.CacheControl)
	}
	return json.Marshal(fields)
}

func restoreNativeTools(unified *llm.Request, req *MessageRequest) error {
	data := nativeJSON(unified.TransformerMetadata, nativeToolsKey)
	if len(data) == 0 {
		return nil
	}
	var tools []nativeTool
	if err := json.Unmarshal(data, &tools); err != nil {
		return fmt.Errorf("%w: invalid native Anthropic tools: %w", transformer.ErrInvalidRequest, err)
	}
	for _, tool := range tools {
		index := min(max(tool.Index, 0), len(req.Tools))
		req.Tools = slices.Insert(req.Tools, index, tool.Tool)
	}
	req.ToolChoice = applyParallelToolCalls(req.ToolChoice, unified.ParallelToolCalls, len(req.Tools) > 0)
	return nil
}
