package biz

import (
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type codexTierRoute struct {
	capabilities, limits map[string]any
	channel              *Channel
	responses            bool
}

func codexTierID(value any) string {
	if row, ok := value.(map[string]any); ok {
		value = row["id"]
	}
	id, _ := value.(string)
	return id
}

func acceleratedTier(id string) bool {
	mode, _ := objects.NormalizeServiceTier(id)
	return mode == "fast" || mode == "ultrafast"
}

func containsCodexTier(values []any, id string) bool {
	mode, _ := objects.NormalizeServiceTier(id)
	return slices.ContainsFunc(values, func(v any) bool {
		other, _ := objects.NormalizeServiceTier(codexTierID(v))
		return mode == other
	})
}

func (r codexTierRoute) supports(id string) bool {
	if !r.responses {
		return false
	}
	declared := false
	for _, field := range []string{"service_tiers", "additional_speed_tiers"} {
		if limits, exists := r.limits[field].([]any); exists && !containsCodexTier(limits, id) {
			return false
		}
		items, _ := r.capabilities[field].([]any)
		declared = declared || containsCodexTier(items, id)
	}
	requested, _ := objects.NormalizeServiceTier(id)
	settings := r.channel.Settings
	if settings == nil || settings.ResponsesServiceTierOverride == objects.ResponsesServiceTierInherit {
		return declared && overridesPreserveCodexTier(r.channel.GetBodyOverrideOperations(), requested)
	}
	wire, allowed := settings.ResponsesTierForRequest(requested)
	actual, _ := objects.NormalizeServiceTier(wire)
	return allowed && actual == requested
}

// Probe the same sjson path syntax used by outbound overrides. A write to
// :service_tier replaces the field, while service_tier.name replaces its
// string value with an object. Neither is visible to a literal key comparison.
const codexTierProbe = `{"service_tier":"original"}`

func codexTierWriteTarget(path string) (whole, affected bool) {
	if path == "service_tier" {
		return true, true
	}
	if !strings.ContainsAny(path, `.:\*?#|@`) {
		return false, false
	}
	body, err := sjson.Set(codexTierProbe, path, "replacement")
	if err != nil {
		return false, false
	}
	result := gjson.Get(body, "service_tier")
	return result.Str == "replacement", result.Str != "original"
}

// Inherit leaves generic body overrides in effect. A later unconditional set
// can restore the requested tier. Unknown request values cannot attest a tier.
func overridesPreserveCodexTier(ops []objects.OverrideOperation, requested string) bool {
	preserved := true
	for _, op := range ops {
		matches := false
		switch op.Op {
		case objects.OverrideOpSetIfAbsent:
			// The client explicitly sent its tier; the runtime checks the original body.
			if gjson.Get(codexTierProbe, op.Path).Exists() {
				continue
			}
			fallthrough
		case objects.OverrideOpSet:
			whole, affected := codexTierWriteTarget(op.Path)
			if !affected {
				continue
			}
			mode, _ := objects.NormalizeServiceTier(op.Value)
			matches = whole && mode == requested
		case objects.OverrideOpArrayAppend, objects.OverrideOpArrayPrepend, objects.OverrideOpArrayInsert:
			// Inserting at the scalar itself fails at runtime. Inserting below
			// it can create a nested array and replace the selected string.
			whole, affected := codexTierWriteTarget(op.Path)
			if whole || !affected {
				continue
			}
		case objects.OverrideOpDelete:
			body, err := sjson.Delete(codexTierProbe, op.Path)
			if err != nil || gjson.Get(body, "service_tier").Str == "original" {
				continue
			}
		case objects.OverrideOpCopy, objects.OverrideOpRename:
			fromTier := gjson.Get(codexTierProbe, op.From).Str == "original"
			toTier, affected := codexTierWriteTarget(op.To)
			if (fromTier && toTier) || (!affected && !(op.Op == objects.OverrideOpRename && fromTier)) {
				continue
			}
		default:
			continue
		}

		if op.Condition == "" || strings.TrimSpace(op.Condition) == "true" {
			preserved = matches
		} else if strings.Contains(op.Condition, "{{") {
			preserved = preserved && matches
		}
	}
	return preserved
}

// Use the model's labels and ordering. A single accessible route that can
// actually deliver a requested accelerated tier is enough to expose it.
func applyCodexRouteTiers(result, base map[string]any, routes []codexTierRoute) {
	for _, field := range []string{"service_tiers", "additional_speed_tiers"} {
		candidates, exists := base[field].([]any)
		if !exists {
			continue
		}
		common, _ := result[field].([]any)
		values := make([]any, 0, len(candidates))
		for _, value := range candidates {
			id := codexTierID(value)
			if acceleratedTier(id) {
				if slices.ContainsFunc(routes, func(r codexTierRoute) bool { return r.supports(id) }) {
					values = append(values, value)
				}
			} else if slices.ContainsFunc(common, func(v any) bool { return codexTierID(v) == id }) {
				values = append(values, value)
			}
		}
		result[field] = values
	}
	if id, _ := base["default_service_tier"].(string); acceleratedTier(id) {
		tiers, _ := result["service_tiers"].([]any)
		result["default_service_tier"] = nil
		mode, _ := objects.NormalizeServiceTier(id)
		for _, tier := range tiers {
			tierID := codexTierID(tier)
			if other, _ := objects.NormalizeServiceTier(tierID); other == mode {
				result["default_service_tier"] = tierID
				break
			}
		}
	}
}
