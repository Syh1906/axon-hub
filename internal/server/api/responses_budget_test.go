package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestResponsesMergedWarmupSharesBodyBudget(t *testing.T) {
	const m = httpclient.LargeBodyThreshold
	budget := httpclient.NewBodyBudget(2 * m)
	s := &responsesWebSocketSession{budget: shared.NewResponsesBudget(64 << 20)}
	defer s.releaseWarmup()
	raw := httptest.NewRequest(http.MethodGet, "/responses", nil)
	input := strings.Repeat("x", 700<<10)
	frame := func(envelope string) responsesFrame {
		f, err := parseResponsesFrame([]byte(envelope))
		require.Nil(t, err)
		var admissionErr error
		f.lease, admissionErr = budget.Admit(t.Context(), int64(len(envelope)))
		require.NoError(t, admissionErr)
		return f
	}
	w := frame(`{"type":"response.create","model":"m","generate":false,"input":"` + input + `"}`)
	_, warm, failure := s.prepareFrame(raw, w)
	w.lease.Release()
	require.Nil(t, failure)
	turn := `{"type":"response.create","previous_response_id":"` + warm.ID + `","input":"` + input + `"}`
	busy, err := budget.Admit(t.Context(), 2*m)
	require.NoError(t, err)
	defer busy.Release()
	f := frame(turn)
	_, _, failure = s.prepareFrame(raw, f)
	f.lease.Release()
	require.NotNil(t, failure)
	require.Equal(t, http.StatusServiceUnavailable, failure.StatusCode)
	require.Equal(t, warm.ID, s.warmupID)
	busy.Release()
	f = frame(turn)
	defer f.lease.Release()
	request, _, failure := s.prepareFrame(raw, f)
	require.Nil(t, failure)
	require.Greater(t, len(request.Body), m)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = budget.Admit(canceled, m)
	require.ErrorIs(t, err, httpclient.ErrBodyBudget)
	s.releaseConsumedWarmup()
	f.lease.Release()
	available, err := budget.Admit(t.Context(), 2*m)
	require.NoError(t, err)
	available.Release()
}

func TestResponsesAdapterBudgetsQueue128MiB(t *testing.T) {
	// Given
	d := newResponsesWebSocketDispatcher(context.Background(), func() {}, nil, nil, nil, 0, nil, nil)
	// When
	accepted := d.reserve(128 << 20)
	// Then
	require.True(t, accepted)
	require.False(t, d.reserve(1))
	d.queueBudget.Replace(128<<20, 0)
	<-d.pending
	require.True(t, d.reserve(128<<20))
	require.Equal(t, 128<<20, responsesWebSocketMaxMessageSize)
}

func BenchmarkResponsesQueueRouting(b *testing.B) {
	frame := []byte(`{"type":"response.create","stream_id":"lane","input":"` + strings.Repeat("x", 2<<20) + `"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(frame)))
	for b.Loop() {
		f, err := parseResponsesFrame(frame)
		if err != nil || f.streamID != "lane" {
			b.Fatal("invalid routing")
		}
	}
}

func TestResponsesAdapterBudgetsWarmupSharesConnection64MiB(t *testing.T) {
	// Given
	budget := shared.NewResponsesBudget(64 << 20)
	require.True(t, budget.Replace(0, (64<<20)-64))
	session := &responsesWebSocketSession{budget: budget}
	frame := []byte(`{"type":"response.create","generate":false,"model":"gpt-5","input":"this warmup exceeds the remaining retained byte budget of the connection"}`)
	// When
	parsed, parseErr := parseResponsesFrame(frame)
	require.Nil(t, parseErr)
	_, _, err := session.prepareFrame(httptest.NewRequest(http.MethodGet, "/responses", nil), parsed)
	// Then
	require.NotNil(t, err)
	require.Empty(t, session.warmupID)
}

func TestResponsesAdapterBudgetsTranscriptOverflowDropsEligibility(t *testing.T) {
	// Given
	budget := shared.NewResponsesBudget(64 << 20)
	lane := shared.NewResponsesLane("lane", budget)
	budget.Replace(0, (64<<20)-2)
	// When
	lane.Remember(shared.ResponsesRecord{ID: "successful_id", Snapshot: shared.NewResponsesSnapshot(nil, []json.RawMessage{json.RawMessage(`{"canary":true}`)}, nil)})
	// Then
	require.Equal(t, "successful_id", lane.Record().ID)
	require.Nil(t, lane.Record().Snapshot)
	lane.Close()
}
