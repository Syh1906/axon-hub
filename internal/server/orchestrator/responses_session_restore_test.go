package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesSessionRestoresIncrementalHistoryAboveCacheLimit(t *testing.T) {
	ctx := shared.WithSessionScope(shared.WithResponsesAPI(context.Background()), "api-key:1:project:1")
	large, err := json.Marshal(strings.Repeat("x", responsesSessionMaxResponse))
	require.NoError(t, err)
	requests := map[string]string{
		"r1": fmt.Sprintf(`{"service_tier":"priority","input":%s}`, large),
		"r2": `{"previous_response_id":"r1","input":[{"type":"function_call_output","call_id":"c1","output":"one"}]}`,
	}
	responses := map[string]string{
		"r1": `{"id":"r1","status":"completed","output":[{"type":"function_call","call_id":"c1","name":"tool","arguments":"{}"}]}`,
		"r2": `{"id":"r2","status":"completed","output":[{"type":"custom_tool_call","call_id":"c2","name":"tool","input":"two"}]}`,
	}
	var loaded []string
	store := newResponsesSessionStore(func(_ context.Context, id string) ([]byte, []byte, bool, error) {
		loaded = append(loaded, id)
		request, found := requests[id]
		return []byte(request), []byte(responses[id]), found, nil
	})
	body := []byte(`{"model":"current","service_tier":"ultrafast","previous_response_id":"r2","input":[{"type":"custom_tool_call_output","call_id":"c2","output":"done"}]}`)
	prepared, _, _ := store.prepare(ctx, body)
	require.False(t, gjson.GetBytes(prepared, "previous_response_id").Exists())
	require.Equal(t, "ultrafast", gjson.GetBytes(prepared, "service_tier").String())
	require.Equal(t, int64(5), gjson.GetBytes(prepared, "input.#").Int())
	require.Equal(t, "c1", gjson.GetBytes(prepared, "input.1.call_id").String())
	require.Equal(t, "c1", gjson.GetBytes(prepared, "input.2.call_id").String())
	require.Equal(t, "c2", gjson.GetBytes(prepared, "input.3.call_id").String())
	require.Equal(t, "c2", gjson.GetBytes(prepared, "input.4.call_id").String())
	require.Equal(t, []string{"r2", "r1"}, loaded)
	require.Nil(t, store.lookup(ctx, "r2"), "oversized history is usable without being cached")
}

func TestResponsesSessionNeverExpandsPartialOrCyclicHistory(t *testing.T) {
	for _, previous := range []string{"missing", "r2"} {
		t.Run(previous, func(t *testing.T) {
			ctx := shared.WithSessionScope(shared.WithResponsesAPI(context.Background()), "api-key:1")
			store := newResponsesSessionStore(func(_ context.Context, id string) ([]byte, []byte, bool, error) {
				if id != "r2" {
					return nil, nil, false, nil
				}
				return []byte(fmt.Sprintf(`{"previous_response_id":%q,"input":"partial"}`, previous)),
					[]byte(`{"id":"r2","status":"completed","output":[]}`), true, nil
			})
			body := []byte(`{"previous_response_id":"r2","input":"current"}`)
			prepared, _, _ := store.prepare(ctx, body)
			require.JSONEq(t, string(body), string(prepared))
			require.Nil(t, store.lookup(ctx, "r2"))
		})
	}
}

func TestResponsesSessionDoesNotCacheUnexpandedNativeContinuation(t *testing.T) {
	ctx := shared.WithSessionScope(shared.WithResponsesAPI(context.Background()), "api-key:1")
	store := newResponsesSessionStore()
	store.record(ctx, []byte(`{"previous_response_id":"native","input":"delta"}`), []byte(`{"id":"new","status":"completed","output":[]}`))
	require.Nil(t, store.lookup(ctx, "new"))
}

func TestResponsesSessionRestoreFailureKeepsNativeRequestAndCancelStops(t *testing.T) {
	ctx := shared.WithSessionScope(context.Background(), "key:1:project:1")
	cause := errors.New("storage unavailable")
	store := newResponsesSessionStore(func(context.Context, string) ([]byte, []byte, bool, error) { return nil, nil, false, cause })
	body := []byte(`{"previous_response_id":"r1","input":"current"}`)
	restored, _, err := store.prepare(ctx, body)
	require.ErrorIs(t, err, cause)
	require.JSONEq(t, string(body), string(restored))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = store.prepare(canceled, body)
	require.ErrorIs(t, err, context.Canceled)
}

func TestResponsesSessionCachedAncestorAndExpiredLargeOutput(t *testing.T) {
	ctx := shared.WithSessionScope(context.Background(), "key:1:project:1")
	calls := []string{}
	store := newResponsesSessionStore(func(_ context.Context, id string) ([]byte, []byte, bool, error) {
		calls = append(calls, id)
		if id == "r2" {
			return []byte(`{"previous_response_id":"r1","input":"second"}`), []byte(`{"id":"r2","output":[]}`), true, nil
		}
		output, _ := json.Marshal(map[string]any{"id": "r1", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": strings.Repeat("x", responsesSessionMaxResponse)}}})
		return []byte(`{"input":"first"}`), output, true, nil
	})
	store.record(ctx, []byte(`{"input":"first"}`), []byte(`{"id":"r1","output":[]}`))
	body := []byte(`{"previous_response_id":"r2","input":"third"}`)
	prepared, _, err := store.prepare(ctx, body)
	require.NoError(t, err)
	require.Equal(t, []string{"r2"}, calls)
	require.Equal(t, int64(3), gjson.GetBytes(prepared, "input.#").Int())
	for _, record := range store.byResponse {
		record.updatedAt = time.Now().Add(-3 * time.Hour)
	}
	calls = nil
	prepared, _, err = store.prepare(ctx, body)
	require.NoError(t, err)
	require.Equal(t, []string{"r2", "r1"}, calls)
	require.Equal(t, int64(4), gjson.GetBytes(prepared, "input.#").Int())
	require.Nil(t, store.lookup(ctx, "r2"))
}
