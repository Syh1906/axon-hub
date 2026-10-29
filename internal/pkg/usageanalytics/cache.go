// Cache normalization and scoring are adapted from claude-code-hub (MIT).
// See LICENSE.cch for the original notice and docs/zh/development/consumption-statistics.md.
package usageanalytics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
)

const separator = "\x1f"

type CacheSample struct {
	CompatibilityKey  string
	TheoreticalTokens int64
	TTLBucket         string
}

// CacheSampleFromRequest follows CCH's normalized prefix bytes / 4 estimate.
// It does not inspect or change routing, and never stores prompt content.
func CacheSampleFromRequest(body []byte, format string, projectID, keyID int, model string) *CacheSample {
	var request map[string]any
	if json.Unmarshal(body, &request) != nil {
		return nil
	}
	system, messages, ok := normalizeConversation(request, format)
	if !ok {
		return nil
	}
	fingerprint, size := digest(system), len(system)
	for _, message := range messages {
		if message == "" {
			continue
		}
		fingerprint = digest(fingerprint + message)
		size += len(message)
	}
	ttl := "5m"
	if hasHourCacheControl(request) {
		ttl = "1h"
	}
	scope := digest(fmt.Sprintf("%d|%d|%s|%s", projectID, keyID, format, model))[:16]
	return &CacheSample{CompatibilityKey: scope + ":" + fingerprint, TheoreticalTokens: int64(size / 4), TTLBucket: ttl}
}

// CacheCoefficient uses CCH's integer arithmetic after summing all model/TTL windows.
// Averaging window scores would incorrectly apply the sample thresholds.
func CacheCoefficient(sample, eligible, theoretical, observed int64) int {
	ratio := func(a, b int64) int64 {
		if b <= 0 {
			return 0
		}
		value := new(big.Int).Mul(big.NewInt(a), big.NewInt(10000))
		value.Quo(value, big.NewInt(b))
		if value.Sign() < 0 {
			return 0
		}
		if value.Cmp(big.NewInt(10000)) > 0 {
			return 10000
		}
		return value.Int64()
	}
	factor := int64(1000)
	switch {
	case eligible >= 100:
		factor = 10000
	case eligible >= 30:
		factor = 6000
	case eligible >= 5:
		factor = 3000
	}
	confidence := ratio(eligible, sample) * factor / 10000
	return int(ratio(observed, theoretical) * confidence / 10000)
}

func normalizeConversation(body map[string]any, format string) (string, []string, bool) {
	system := separator
	var messages []string
	switch format {
	case "anthropic/messages":
		items, ok := body["messages"].([]any)
		if !ok {
			return "", nil, false
		}
		switch value := body["system"].(type) {
		case string:
			system += value
		case []any:
			for _, block := range value {
				system += anthropicBlock(block)
			}
		}
		system += normalizedTools(body["tools"], "anthropic")
		for _, item := range items {
			message, ok := item.(map[string]any)
			if !ok {
				continue
			}
			parts := []string{separator, stringValue(message, "role")}
			switch content := message["content"].(type) {
			case string:
				parts = append(parts, separator, "text:", content)
			case []any:
				for _, block := range content {
					parts = append(parts, anthropicBlock(block))
				}
			}
			if len(parts) > 2 {
				messages = append(messages, strings.Join(parts, ""))
			}
		}
	case "openai/chat_completions":
		items, ok := body["messages"].([]any)
		if !ok {
			return "", nil, false
		}
		system += normalizedTools(body["tools"], "openai")
		leadingSystem := true
		for _, item := range items {
			message, ok := item.(map[string]any)
			if !ok {
				continue
			}
			role := stringValue(message, "role")
			if leadingSystem && (role == "system" || role == "developer") {
				system += separator + role + ":" + contentString(message["content"])
				continue
			}
			leadingSystem = false
			normalized := separator + role + separator + "content:" + contentString(message["content"])
			calls, _ := message["tool_calls"].([]any)
			for _, call := range calls {
				value, ok := call.(map[string]any)
				if !ok {
					continue
				}
				fn, _ := value["function"].(map[string]any)
				normalized += separator + "tool_call:" + stringValue(fn, "name") + ":" + stringValue(fn, "arguments")
			}
			if _, exists := message["tool_call_id"]; exists {
				normalized += separator + "tool_result"
			}
			messages = append(messages, normalized)
		}
	case "openai/responses", "openai/responses-ws", "openai/responses_compact":
		system += stringValue(body, "instructions") + normalizedTools(body["tools"], "responses")
		switch input := body["input"].(type) {
		case string:
			messages = append(messages, separator+"user"+separator+"text:"+input)
		case []any:
			for _, item := range input {
				value, ok := item.(map[string]any)
				if !ok {
					continue
				}
				kind := stringValue(value, "type")
				if kind == "" {
					kind = "message"
				}
				var normalized string
				switch kind {
				case "message":
					normalized = stringValue(value, "role") + separator + "content:" + contentString(value["content"])
				case "function_call":
					normalized = kind + separator + stringValue(value, "name") + ":" + stringValue(value, "arguments")
				case "function_call_output":
					normalized = kind + separator + contentString(value["output"])
				case "reasoning":
					content := value["summary"]
					if content == nil {
						content = value["content"]
					}
					normalized = kind + separator + contentString(content)
				default:
					normalized = kind + separator + canonicalJSON(stripVolatile(value))
				}
				messages = append(messages, separator+normalized)
			}
		default:
			return "", nil, false
		}
	case "gemini/contents":
		if nested, ok := body["request"].(map[string]any); ok {
			body = nested
		}
		items, ok := body["contents"].([]any)
		if !ok {
			return "", nil, false
		}
		instruction, _ := body["systemInstruction"].(map[string]any)
		if instruction == nil {
			instruction, _ = body["system_instruction"].(map[string]any)
		}
		parts, _ := instruction["parts"].([]any)
		for _, part := range parts {
			system += geminiPart(part)
		}
		tools, _ := body["tools"].([]any)
		var declarations []any
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if decls, ok := tool["functionDeclarations"].([]any); ok {
				declarations = append(declarations, decls...)
			} else {
				declarations = append(declarations, tool)
			}
		}
		system += normalizedTools(declarations, "gemini")
		for _, item := range items {
			value, ok := item.(map[string]any)
			if !ok {
				continue
			}
			parts, _ := value["parts"].([]any)
			if len(parts) == 0 {
				continue
			}
			normalized := separator + stringValue(value, "role")
			for _, part := range parts {
				normalized += geminiPart(part)
			}
			messages = append(messages, normalized)
		}
	default:
		return "", nil, false
	}
	return system, messages, true
}

func anthropicBlock(raw any) string {
	block, ok := raw.(map[string]any)
	if !ok {
		if value, ok := raw.(string); ok {
			return separator + "text:" + value
		}
		return ""
	}
	kind := stringValue(block, "type")
	switch kind {
	case "text", "thinking":
		return separator + kind + ":" + stringValue(block, kind)
	case "redacted_thinking":
		return separator + kind + ":" + stringValue(block, "data")
	case "tool_use":
		return separator + kind + ":" + stringValue(block, "name") + ":" + canonicalJSON(block["input"])
	case "tool_result":
		return separator + kind + ":" + contentString(block["content"])
	case "image", "document":
		source, _ := block["source"].(map[string]any)
		if source == nil {
			return separator + kind + ":"
		}
		media := firstString(source, "media_type", "mediaType")
		data := stringValue(source, "data")
		if data != "" {
			data = digest(data)
		} else {
			data = stringValue(source, "url")
		}
		return separator + kind + ":" + media + ":" + data
	default:
		if kind == "" {
			return ""
		}
		return separator + kind + ":" + canonicalJSON(stripVolatile(block))
	}
}

func geminiPart(raw any) string {
	part, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if text, ok := part["text"].(string); ok {
		return separator + "text:" + text
	}
	if call, ok := part["functionCall"].(map[string]any); ok {
		return separator + "tool_use:" + stringValue(call, "name") + ":" + canonicalJSON(call["args"])
	}
	if result, ok := part["functionResponse"].(map[string]any); ok {
		return separator + "tool_result:" + stringValue(result, "name") + ":" + canonicalJSON(result["response"])
	}
	inline, _ := part["inlineData"].(map[string]any)
	if inline == nil {
		inline, _ = part["inline_data"].(map[string]any)
	}
	if inline != nil {
		data := stringValue(inline, "data")
		if data != "" {
			data = digest(data)
		}
		return separator + "image:" + firstString(inline, "mimeType", "mime_type") + ":" + data
	}
	file, _ := part["fileData"].(map[string]any)
	if file == nil {
		file, _ = part["file_data"].(map[string]any)
	}
	if file != nil {
		return separator + "file:" + firstString(file, "mimeType", "mime_type") + ":" + firstString(file, "fileUri", "file_uri")
	}
	return separator + "part:" + canonicalJSON(stripVolatile(part))
}

func normalizedTools(raw any, format string) string {
	tools, _ := raw.([]any)
	type spec struct{ name, description, parameters string }
	var specs []spec
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if format == "openai" {
			if fn, ok := tool["function"].(map[string]any); ok {
				tool = fn
			}
		}
		name := stringValue(tool, "name")
		if name == "" {
			continue
		}
		parametersKey := "parameters"
		if format == "anthropic" {
			parametersKey = "input_schema"
		}
		parameters := ""
		if value, ok := tool[parametersKey].(map[string]any); ok {
			parameters = canonicalJSON(value)
		}
		specs = append(specs, spec{name, stringValue(tool, "description"), parameters})
	}
	slices.SortStableFunc(specs, func(a, b spec) int { return strings.Compare(a.name, b.name) })
	var result strings.Builder
	for _, spec := range specs {
		result.WriteString(separator + spec.name + ":" + spec.description + ":" + spec.parameters)
	}
	return result.String()
}

func canonicalJSON(value any) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value) // Values come exclusively from decoded JSON.
	return strings.TrimSuffix(strings.NewReplacer("\\u2028", "\u2028", "\\u2029", "\u2029").Replace(out.String()), "\n")
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
func stringValue(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}
func firstString(value map[string]any, a, b string) string {
	if text := stringValue(value, a); text != "" {
		return text
	}
	return stringValue(value, b)
}
func contentString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return canonicalJSON(value)
}
func stripVolatile(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, child := range value {
		if !slices.Contains([]string{"id", "call_id", "tool_use_id", "tool_call_id", "cache_control"}, key) {
			result[key] = child
		}
	}
	return result
}

func hasHourCacheControl(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		if control, ok := value["cache_control"].(map[string]any); ok && stringValue(control, "ttl") == "1h" {
			return true
		}
		for _, child := range value {
			if hasHourCacheControl(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasHourCacheControl(child) {
				return true
			}
		}
	}
	return false
}
