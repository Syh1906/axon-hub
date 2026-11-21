package responses

import (
	"bytes"
	"testing"
)

// Large tool output precedes a real settings change, as in a long Codex turn.
func BenchmarkEffectiveReasoningEffort(b *testing.B) {
	body := []byte(`{"reasoning":{"effort":"high"},"input":[{"type":"function_call_output","output":"`)
	body = append(body, bytes.Repeat([]byte("x"), 4<<20)...)
	body = append(body, `"},{"type":"configuration_update","reasoning":{"effort":"max"}}]}`...)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		if EffectiveReasoningEffort(body) != "max" {
			b.Fatal("last configuration update lost")
		}
	}
}
