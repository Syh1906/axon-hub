package biz

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/transformer/openai/codex"
)

func TestRequestContinuationKeyUsesExplicitTurnIdentity(t *testing.T) {
	makeHeaders := func(session, thread, turn string) http.Header {
		encoded, err := json.Marshal(codex.TurnMetadata{SessionID: session, ThreadID: thread, TurnID: turn, RequestKind: "turn"})
		require.NoError(t, err)
		headers := http.Header{}
		headers.Set(codex.TurnMetadataHeader, string(encoded))
		return headers
	}
	headers := makeHeaders("session", "thread", "turn")
	key := RequestContinuationKey(headers)
	require.Len(t, key, 64)
	headers.Set(codex.SessionHeaderHyphen, "session")
	headers.Set(codex.ThreadIDHeader, "thread")
	require.Equal(t, key, RequestContinuationKey(headers))
	for _, other := range []http.Header{
		makeHeaders("other-session", "thread", "turn"),
		makeHeaders("session", "other-thread", "turn"),
		makeHeaders("session", "thread", "other-turn"),
		makeHeaders("a:b", "c", "d"), makeHeaders("a", "b:c", "d"),
	} {
		require.NotEqual(t, key, RequestContinuationKey(other))
	}
	require.NotEqual(t, RequestContinuationKey(makeHeaders("a:b", "c", "d")), RequestContinuationKey(makeHeaders("a", "b:c", "d")))
	headers.Set(codex.SessionHeaderHyphen, "conflicting")
	require.Empty(t, RequestContinuationKey(headers))
	for _, raw := range []string{"", "broken", `{}`, `{"session_id":"s","thread_id":"t","turn_id":"u"}`, `{"session_id":"s","thread_id":"t","turn_id":"","request_kind":"turn"}`} {
		headers := http.Header{}
		headers.Set(codex.TurnMetadataHeader, raw)
		require.Empty(t, RequestContinuationKey(headers))
	}
	require.Empty(t, RequestContinuationKey(nil))
}
