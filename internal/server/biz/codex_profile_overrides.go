package biz

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func snapshotCodexProfile(snapshot *codexSnapshot, modelID string) *codexModelProfile {
	if snapshot == nil {
		return nil
	}
	if snapshot.public != nil {
		entry := snapshot.public.models[modelID]
		minimum, _ := entry["minimal_client_version"].(string)
		kind := "official_public"
		if snapshot.public.source() != "official" {
			kind = importedCatalogKind
		}
		if entry != nil && ((minimum == "" && kind == importedCatalogKind) || (validCodexSnapshotVersion(minimum) && compareCodexVersions(minimum, snapshot.clientVersion) <= 0)) {
			return &codexModelProfile{SourceKind: kind, SourceSHA256: snapshot.public.SHA256, SourceModelID: modelID, Model: entry}
		}
	}
	if snapshot.Models[modelID] == nil {
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
	patched := parsed[id]
	if patched == nil {
		return nil, fmt.Errorf("Codex overrides cannot replace the model slug")
	}
	for _, field := range []string{"node_repl_auto_review_required", "node_repl_disabled"} {
		if model[field] == true {
			patched[field] = true
		}
	}
	return patched, nil
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

// Return only declarations explicitly supplied by the outer model form.
// In particular a blank field must not lock the corresponding catalog value.
func codexModelPageFields(profile map[string]any, model *ent.Model) map[string]any {
	fields := make(map[string]any)
	card := model.ModelCard
	if card != nil {
		if len(card.Modalities.Input) > 0 {
			inputs := make([]any, 0, len(card.Modalities.Input))
			for _, input := range card.Modalities.Input {
				switch input {
				case "text", "image", "audio":
					if !slices.Contains(inputs, any(input)) {
						inputs = append(inputs, input)
					}
				}
			}
			fields["input_modalities"] = inputs
		}
		applyCodexCardReasoning(fields, profile, card)
		if card.Limit.Context > 0 {
			fields["max_context_window"] = float64(card.Limit.Context)
		}
	}
	setCodexModelWindows(fields, model.Settings)
	return fields
}

func fixedCodexModelFields(saved, defaults, overrides map[string]any) (map[string]any, map[string]any, error) {
	result := maps.Clone(saved)
	explicit := make(map[string]any)
	if defaults != nil {
		for _, field := range codexCapabilityFields {
			value, exists := saved[field]
			original, hadOriginal := defaults[field]
			if exists != hadOriginal || !reflect.DeepEqual(value, original) {
				explicit[field] = value
			}
		}
	}
	for _, field := range []string{"node_repl_auto_review_required", "node_repl_disabled"} {
		if defaults[field] == true {
			result[field] = true
		}
	}
	if len(overrides) != 0 {
		patched, err := applyCodexProfileOverrides(result, overrides)
		if err != nil {
			return nil, nil, err
		}
		maps.Copy(explicit, overrides)
		result = patched
	}
	return result, explicit, nil
}

func applyCodexCardReasoning(fields, base map[string]any, card *objects.ModelCard) {
	if card.Reasoning.SupportDeclared && !card.Reasoning.Supported {
		fields["supported_reasoning_levels"] = []any{}
		fields["default_reasoning_level"] = nil
		fields["supports_reasoning_effort_updates"] = false
		return
	}
	if len(card.ReasoningEfforts) == 0 {
		return
	}
	levels, _ := base["supported_reasoning_levels"].([]any)
	byEffort := make(map[string]any, len(levels))
	for _, item := range levels {
		level, _ := item.(map[string]any)
		effort, _ := level["effort"].(string)
		byEffort[effort] = item
	}
	choices := make([]any, 0, len(card.ReasoningEfforts))
	for _, effort := range lo.Uniq(card.ReasoningEfforts) {
		if item := byEffort[effort]; item != nil {
			choices = append(choices, item)
			continue
		}
		switch effort {
		case llm.ReasoningEffortNone, llm.ReasoningEffortMinimal, llm.ReasoningEffortLow, llm.ReasoningEffortMedium, llm.ReasoningEffortHigh, llm.ReasoningEffortXHigh, llm.ReasoningEffortMax:
			choices = append(choices, map[string]any{"effort": effort, "description": effort})
		}
	}
	// Ultra is a client mode backed by an actual reasoning effort.
	underlying, _ := base["multi_agent_reasoning_effort"].(string)
	if byEffort["ultra"] != nil && !slices.Contains(card.ReasoningEfforts, "ultra") && slices.Contains(card.ReasoningEfforts, underlying) {
		choices = append(choices, byEffort["ultra"])
	}
	fields["supported_reasoning_levels"] = choices
}

func finalizeCodexModelFields(result, profile, explicit map[string]any, card *objects.ModelCard) map[string]any {
	// Restore only fields explicitly edited in the model definition; keep the
	// key-scoped wire identity and mandatory tool safeguards enforced above.
	for _, field := range codexCapabilityFields {
		if _, edited := explicit[field]; !edited || field == "node_repl_auto_review_required" || field == "node_repl_disabled" {
			continue
		}
		if value, exists := profile[field]; exists {
			result[field] = value
		} else {
			delete(result, field)
		}
	}
	// The separate vision checkbox declares image input only. With no explicit
	// input list it must not force unrelated modalities from the baseline.
	if card != nil && card.Vision && len(card.Modalities.Input) == 0 {
		inputs, _ := result["input_modalities"].([]any)
		if !slices.Contains(inputs, any("image")) {
			result["input_modalities"] = append(slices.Clone(inputs), "image")
		}
	}
	if _, set := explicit["supported_reasoning_levels"]; set {
		result["default_reasoning_level"] = profile["default_reasoning_level"]
	}
	if _, set := explicit["service_tiers"]; set {
		result["default_service_tier"] = profile["default_service_tier"]
	}
	// An explicitly selected default needs its option in the picker. Only
	// restore options present in the effective profile (including card edits).
	if effort, ok := explicit["default_reasoning_level"].(string); ok && effort != "" {
		restoreEffort := func(want string) {
			restoreCodexOption(result, profile, "supported_reasoning_levels", func(option any) bool {
				level, _ := option.(map[string]any)
				return level["effort"] == want
			})
		}
		restoreEffort(effort)
		if effort == "ultra" {
			if underlying, ok := profile["multi_agent_reasoning_effort"].(string); ok {
				restoreEffort(underlying)
			}
		}
	}
	if id, ok := explicit["default_service_tier"].(string); ok && id != "" {
		mode, normalized := objects.NormalizeServiceTier(id)
		matches := func(option any) bool {
			tierID := codexTierID(option)
			other, _ := objects.NormalizeServiceTier(tierID)
			return tierID == id || (normalized && mode == other)
		}
		restoreCodexOption(result, profile, "service_tiers", matches)
	}
	resolveCodexTierDefault(result, profile)
	constrainCodexReasoningProfile(result, profile)
	clearUnavailableCodexDefaults(result)
	if _, forceDefault := explicit["context_window"]; forceDefault {
		if _, forceMax := explicit["max_context_window"]; !forceMax {
			wanted, _ := profile["context_window"].(float64)
			if ceiling, ok := profile["max_context_window"].(float64); ok {
				wanted = min(wanted, ceiling)
			}
			if bound, ok := result["max_context_window"].(float64); ok && bound < wanted {
				result["max_context_window"] = wanted
			}
		}
	}
	return applyCodexContextSettings(result, nil, nil, "")
}

func restoreCodexOption(result, profile map[string]any, field string, matches func(any) bool) {
	available, _ := result[field].([]any)
	if slices.ContainsFunc(available, matches) {
		return
	}
	declared, _ := profile[field].([]any)
	if i := slices.IndexFunc(declared, matches); i >= 0 {
		result[field] = append(slices.Clone(available), declared[i])
	}
}
