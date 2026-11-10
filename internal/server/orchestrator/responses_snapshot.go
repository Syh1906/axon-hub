package orchestrator

import (
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/llm/transformer/shared"
)

// Only recovery expands old turns. Healthy native WS requests keep their delta.
func expandResponsesSnapshot(body []byte, snapshot *shared.ResponsesSnapshot) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if snapshot.Size()+len(payload["input"]) > 128<<20 {
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
