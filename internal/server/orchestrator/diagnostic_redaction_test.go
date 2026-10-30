package orchestrator

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDiagnosticRedactionAndBoundedBody(t *testing.T) {
	text, truncated := sanitizedDiagnosticBody([]byte(`{"error":{"code":"insufficient_quota","message":"rejected fixture-secret"},"headers":{"Authorization":"Bearer hidden","api_key":"other-secret"}}`), "fixture-secret")
	require.False(t, truncated)
	require.Contains(t, text, "insufficient_quota")
	for _, value := range []string{"fixture-secret", "hidden", "other-secret"} {
		require.NotContains(t, text, value)
	}
	text, truncated = sanitizedDiagnosticBody([]byte(strings.Repeat("错", 30000)), "")
	require.True(t, truncated)
	require.LessOrEqual(t, len(text), maxDiagnosticBodyBytes)
	require.True(t, utf8.ValidString(text))
	require.NotContains(t, sanitizeDiagnosticText("api_key=fixture-value authorization=fixture-auth", ""), "fixture-")
}
