package biz

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/llm/transformer/openai/codex"
)

// RequestContinuationKey identifies only an explicitly supplied client turn.
// It is captured before outbound adapters can fabricate compatibility metadata.
// Keep the same parser for incoming requests and historical header backfills.
func RequestContinuationKey(headers http.Header) string {
	var metadata codex.TurnMetadata
	if err := json.Unmarshal([]byte(headers.Get(codex.TurnMetadataHeader)), &metadata); err != nil {
		return ""
	}
	if metadata.RequestKind != "turn" {
		return ""
	}
	sessionID := codex.GetSessionIDFromHeaders(headers)
	threadID := strings.TrimSpace(headers.Get(codex.ThreadIDHeader))
	if threadID == "" {
		threadID = strings.TrimSpace(metadata.ThreadID)
	}
	turnID := strings.TrimSpace(metadata.TurnID)
	if sessionID == "" || threadID == "" || turnID == "" {
		return ""
	}
	// Conflicting explicit identities are not evidence of a continuation.
	if metadata.SessionID != "" && strings.TrimSpace(metadata.SessionID) != sessionID ||
		metadata.ThreadID != "" && strings.TrimSpace(metadata.ThreadID) != threadID {
		return ""
	}
	encoded, _ := json.Marshal([]string{"codex-turn-v1", sessionID, threadID, turnID})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
