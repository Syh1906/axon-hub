package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestFailedTerminalPersistsFailureAfterDone(t *testing.T) {
	ctx, db, _, service, _, _, key := setupBudgetRequest(t)
	for _, status := range []string{"failed", "incomplete"} {
		req := db.Request.Create().SetProjectID(key.ProjectID).SetAPIKeyID(key.ID).SetModelID("gpt-4").SetRequestBody([]byte(`{}`)).SetStatus(request.StatusProcessing).SaveX(ctx)
		state := &PersistenceState{}
		events := &sliceEventStream{events: []*httpclient.StreamEvent{
			{Data: []byte(`{"type":"response.` + status + `","response":{"id":"terminal-test","status":"` + status + `"}}`)},
			&llm.DoneStreamEvent,
		}}
		stream := NewInboundPersistentStream(ctx, events, req, nil, service, &mockInboundTransformer{}, nil, state)
		for stream.Next() {
			stream.Current()
		}
		require.Error(t, stream.Err())
		require.NoError(t, stream.Close())
		require.Equal(t, request.StatusFailed, db.Request.GetX(ctx, req.ID).Status)
	}
}

func TestFailedTerminalCannotBecomeSuccessfulAtDone(t *testing.T) {
	for _, data := range []string{
		`{"type":"response.failed","response":{"status":"failed"}}`,
		`{"type":"response.completed","response":{"status":"failed"}}`,
		`{"type":"response.incomplete","response":{"status":"incomplete"}}`,
		`{"choices":[{"finish_reason":"error"}]}`,
	} {
		event := &httpclient.StreamEvent{Data: []byte(data)}
		state := &PersistenceState{}
		state.observeStreamTerminal(event)
		require.Error(t, state.StreamTerminalError)
		require.False(t, state.StreamCompleted)
		require.True(t, IsTerminalStreamEvent(event), "failure is still a terminal marker for the client writer")
		state.observeStreamTerminal(&llm.DoneStreamEvent)
		require.False(t, state.StreamCompleted)
	}
	require.False(t, isCompletedAggregated(llm.ResponseMeta{Status: "failed", Usage: &llm.Usage{CompletionTokens: 10}}))
	require.True(t, isCompletedAggregated(llm.ResponseMeta{Status: "completed"}))
}
