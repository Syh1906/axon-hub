package shared

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesSnapshotPreservesCompleteHistory(t *testing.T) {
	// Given a root, a complete tool call, and its matching output on the next turn.
	root := NewResponsesSnapshot(nil, []json.RawMessage{json.RawMessage(`{"role":"user","content":"root"}`)}, []json.RawMessage{json.RawMessage(`{"type":"function_call","call_id":"call1","arguments":"{}"}`)})
	next := NewResponsesSnapshot(root, []json.RawMessage{json.RawMessage(`{"type":"function_call_output","call_id":"call1","output":"result"}`)}, nil)
	// Then recovery yields all items exactly once and keeps the root immutable.
	require.Len(t, root.Items(), 2)
	require.Len(t, next.Items(), 3)
	require.Equal(t, root.Items()[0], next.Items()[0])
	require.Contains(t, string(next.Items()[2]), "result")
	require.Greater(t, next.Size(), root.Size())
}

func TestResponsesSnapshotEmptyTurnsConsumeBudget(t *testing.T) {
	// Empty turns must not grow an unbounded chain outside the byte budget.
	budget := NewResponsesBudget(1024)
	lane := NewResponsesLane("lane", budget)
	for i := range 100 {
		parent := lane.Record().Snapshot
		lane.Remember(ResponsesRecord{ID: fmt.Sprint(i), Snapshot: NewResponsesSnapshot(parent, nil, nil)})
		if lane.Record().Snapshot == nil {
			require.Less(t, i, 100)
			require.Equal(t, fmt.Sprint(i), lane.Record().ID)
			require.True(t, budget.Replace(0, 1024), "overflow releases the old chain")
			budget.Replace(1024, 0)
			lane.Close()
			return
		}
	}
	t.Fatal("empty history escaped the connection budget")
}

func TestResponsesSnapshotLaneReplacementAndCloseReleaseBudget(t *testing.T) {
	root := NewResponsesSnapshot(nil, []json.RawMessage{json.RawMessage(`"root"`)}, nil)
	next := NewResponsesSnapshot(root, []json.RawMessage{json.RawMessage(`"next"`)}, nil)
	budget := NewResponsesBudget(next.Size())
	lane := NewResponsesLane("lane", budget)
	lane.Remember(ResponsesRecord{ID: "root", Snapshot: root})
	lane.Remember(ResponsesRecord{ID: "next", Snapshot: next})
	require.Len(t, lane.Record().Snapshot.Items(), 2)
	require.False(t, budget.Replace(0, 1))
	lane.Close()
	lane.Close()
	require.True(t, budget.Replace(0, next.Size()))
}

// The benchmark includes the lane's budget accounting, not just node allocation.
// Keeping the same new item must have flat cost as the retained history grows.
func BenchmarkResponsesSnapshotAppend(b *testing.B) {
	for _, turns := range []int{1, 256, 4096} {
		b.Run(fmt.Sprint(turns), func(b *testing.B) {
			item := []json.RawMessage{json.RawMessage(`{"role":"user","content":"fixed incremental input"}`)}
			var root *ResponsesSnapshot
			for range turns {
				root = NewResponsesSnapshot(root, item, nil)
			}
			lane := NewResponsesLane("benchmark", NewResponsesBudget(64<<20))
			defer lane.Close()
			lane.Remember(ResponsesRecord{ID: "root", Snapshot: root})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				lane.Remember(ResponsesRecord{ID: "next", Snapshot: NewResponsesSnapshot(root, item, nil)})
			}
		})
	}
}
