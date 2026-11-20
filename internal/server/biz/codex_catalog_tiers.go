package biz

import (
	"slices"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type codexTierRoute struct {
	capabilities, limits map[string]any
	channel              *Channel
	responses            bool
	models               *codexTierModels
}

type codexTierModels struct {
	RequestModel, Model string
}

// Evaluate scalar expressions involving only literals and the two known model
// names. A request-dependent or more complex template stays unknown. Do not
// execute general user templates while listing models.
func (m *codexTierModels) staticValue(raw string) (string, bool) {
	if !strings.Contains(raw, "{{") || !strings.Contains(raw, "}}") {
		return raw, true
	}
	t, err := template.New("tier").Parse(raw)
	if err != nil || len(t.Templates()) != 1 {
		return "", false
	}
	for _, node := range t.Tree.Root.Nodes {
		if node.Type() == parse.NodeText {
			continue
		}
		action, ok := node.(*parse.ActionNode)
		if !ok || len(action.Pipe.Decl) != 0 {
			return "", false
		}
		for _, cmd := range action.Pipe.Cmds {
			for _, arg := range cmd.Args {
				switch v := arg.(type) {
				case *parse.IdentifierNode:
					switch v.Ident {
					case "eq", "ne", "and", "or", "not":
					default:
						return "", false
					}
				case *parse.FieldNode:
					if len(v.Ident) != 1 || (v.Ident[0] != "RequestModel" && v.Ident[0] != "Model") {
						return "", false
					}
				case *parse.BoolNode, *parse.StringNode, *parse.NumberNode:
				default:
					return "", false
				}
			}
		}
	}
	var rendered strings.Builder
	if err := t.Execute(&rendered, m); err != nil {
		return "", false
	}
	return rendered.String(), true
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
		return declared && overridesPreserveCodexTier(r.channel.GetBodyOverrideOperations(), requested, r.models)
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
func overridesPreserveCodexTier(ops []objects.OverrideOperation, requested string, models *codexTierModels) bool {
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
			value, known := op.Value, true
			if models != nil {
				value, known = models.staticValue(value)
			}
			mode, _ := objects.NormalizeServiceTier(value)
			matches = whole && (!known || mode == requested)
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
			if models != nil && !fromTier {
				// A missing source skips the operation. A present source can
				// restore the requested string only when writing the whole field.
				matches = preserved || toTier
			}
		default:
			continue
		}

		condition := op.Condition
		if models != nil && condition != "" {
			value, known := models.staticValue(condition)
			if !known {
				preserved = preserved || matches
				continue
			}
			if strings.TrimSpace(value) != "true" {
				continue
			}
		}
		if models != nil || condition == "" || strings.TrimSpace(condition) == "true" {
			preserved = matches
		} else if strings.Contains(condition, "{{") {
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
}

func resolveCodexTierDefault(result, base map[string]any) {
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
