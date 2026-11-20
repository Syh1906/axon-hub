package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/transformer/shared"
)

// Verify the final provider input, after prompt, plugin and channel changes,
// before sharing an immutable prefix. Only new items need independent storage.
func retainedResponsesSuffix(input gjson.Result, snapshot *shared.ResponsesSnapshot) ([]json.RawMessage, bool) {
	if !input.IsArray() {
		return nil, false
	}
	prefix := snapshot.Items()
	var suffix []json.RawMessage
	index := 0
	matched := true
	input.ForEach(func(_, item gjson.Result) bool {
		if index < len(prefix) {
			matched = bytes.Equal(prefix[index], []byte(item.Raw))
		} else {
			suffix = append(suffix, json.RawMessage(item.Raw))
		}
		index++
		return matched
	})
	return suffix, matched && index >= len(prefix)
}

func responsesReplayFits(historyBytes, inputBytes int) bool {
	return inputBytes <= (128<<20)-historyBytes
}

// Only recovery expands old turns. Healthy native WS requests keep their delta.
func expandResponsesSnapshot(body []byte, snapshot *shared.ResponsesSnapshot) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !responsesReplayFits(snapshot.Size(), len(payload["input"])) {
		return nil, shared.ResponsesRecoveryError()
	}
	items := snapshot.Items()
	items = append(items, responseSessionInputItems(payload["input"])...)
	input, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("encode complete Responses input: %w", err)
	}
	payload["input"] = input
	delete(payload, "previous_response_id")
	return json.Marshal(payload)
}
