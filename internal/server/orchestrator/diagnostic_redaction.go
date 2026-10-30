package orchestrator

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxDiagnosticBodyBytes = 64 * 1024

var diagnosticSecretPattern = regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|password|authorization|cookie|secret)["']?\s*[:=]\s*["']?)[^\s"',}&]+`)

func sanitizeDiagnosticText(value string, secret string) string {
	if secret != "" {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	value = tokenRegex.ReplaceAllString(value, "${1}[REDACTED]")
	return diagnosticSecretPattern.ReplaceAllString(value, "${1}[REDACTED]")
}

func sanitizedDiagnosticBody(body []byte, secret string) (string, bool) {
	var value any
	var redact func(any) any
	redact = func(value any) any {
		switch item := value.(type) {
		case map[string]any:
			for key, child := range item {
				switch strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key)) {
				case "key", "apikey", "authorization", "password", "accesstoken", "refreshtoken", "token", "secret", "cookie", "setcookie":
					item[key] = "[REDACTED]"
				default:
					item[key] = redact(child)
				}
			}
		case []any:
			for i := range item {
				item[i] = redact(item[i])
			}
		case string:
			return sanitizeDiagnosticText(item, secret)
		}
		return value
	}
	text := string(body)
	if json.Unmarshal(body, &value) == nil {
		encoded, _ := json.MarshalIndent(redact(value), "", "  ")
		text = string(encoded)
	} else {
		text = sanitizeDiagnosticText(text, secret)
	}
	truncated := len(text) > maxDiagnosticBodyBytes
	if truncated {
		text = text[:maxDiagnosticBodyBytes]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text, truncated
}
