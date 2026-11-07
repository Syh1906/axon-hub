package biz

import (
	"encoding/json"
	"fmt"
)

func snapshotCodexProfile(snapshot *codexSnapshot, modelID string) *codexModelProfile {
	if snapshot == nil || snapshot.Models[modelID] == nil {
		return nil
	}
	return &codexModelProfile{SourceKind: "official_snapshot", SnapshotVersion: snapshot.Version, SourceModelID: modelID, Model: snapshot.Models[modelID]}
}

// Overrides follow JSON Merge Patch: objects merge recursively, arrays replace,
// and null removes a field. Re-parse the full result before exposing it to Codex.
func applyCodexProfileOverrides(model, overrides map[string]any) (map[string]any, error) {
	body, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	mergeCodexProfileObject(result, overrides)
	body, err = json.Marshal(map[string]any{"models": []any{result}})
	if err != nil {
		return nil, err
	}
	parsed, err := parseCodexCatalog(body)
	if err != nil {
		return nil, fmt.Errorf("invalid inherited Codex overrides: %w", err)
	}
	id, _ := model["slug"].(string)
	return parsed[id], nil
}

func mergeCodexProfileObject(target, patch map[string]any) {
	for key, value := range patch {
		if value == nil {
			delete(target, key)
			continue
		}
		if object, ok := value.(map[string]any); ok {
			current, ok := target[key].(map[string]any)
			if !ok {
				current = make(map[string]any)
				target[key] = current
			}
			mergeCodexProfileObject(current, object)
		} else {
			target[key] = value
		}
	}
}
