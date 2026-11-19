package contexts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
)

func TestResponsesEventClocksDistinguishStructureAndDeliveredDelta(t *testing.T) {
	_, clock := WithRequestClock(t.Context(), time.Now())
	a := clock.AddAttempt(1, 2, nil)
	for _, event := range []*httpclient.StreamEvent{
		{Data: []byte(`{"type":"response.created"}`)},
		{Type: "response.output_text.delta", Data: []byte(`{"delta":""}`)},
		{Type: "untrusted-custom-type", Data: []byte(`{}`)},
	} {
		a.ObserveResponsesEvent(event)
		clock.WroteResponsesEvent(event)
	}
	s := clock.Snapshot()
	require.Equal(t, "response.created", s.Attempts[0].FirstEvent.Type)
	require.Nil(t, s.Attempts[0].FirstDelta)
	require.Nil(t, s.FirstWriteDelta)
	event := &httpclient.StreamEvent{Data: []byte(`{"type":"response.function_call_arguments.delta","delta":"fragment"}`)}
	a.ObserveResponsesEvent(event)
	require.Nil(t, clock.Snapshot().FirstWriteDelta)
	clock.WroteResponsesEvent(event)
	later := &httpclient.StreamEvent{Type: "response.output_text.delta", Data: []byte(`{"delta":"later"}`)}
	a.ObserveResponsesEvent(later)
	clock.WroteResponsesEvent(later)
	s = clock.Snapshot()
	require.Equal(t, "response.function_call_arguments.delta", s.Attempts[0].FirstDelta.Type)
	require.Equal(t, s.Attempts[0].FirstDelta.Type, s.FirstWriteDelta.Type)
	require.False(t, s.FirstWriteDelta.At.Before(s.Attempts[0].FirstDelta.At))
	require.Equal(t, len(event.Data), s.FirstWriteDelta.Bytes)
}
