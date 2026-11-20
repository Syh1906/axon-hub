package orchestrator

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/codex"
	"github.com/stretchr/testify/require"
)

func TestCompactionCooldownSingleProbeAndIndependentInputs(t *testing.T) {
	var cooldown compactionCooldown
	now := time.Unix(10, 0)
	key := [32]byte{1}
	start, wait := cooldown.begin(key, 1, true, now)
	require.Nil(t, wait)
	overlap, _ := cooldown.begin(key, 2, true, now)
	start.finish(true, true, now)
	overlap.finish(true, false, now.Add(time.Second))
	_, wait = cooldown.begin(key, 3, true, now.Add(23*time.Second))
	require.Equal(t, 7, wait.seconds)
	require.Equal(t, 1, wait.requestID)
	_, wait = cooldown.begin([32]byte{2}, 4, true, now.Add(23*time.Second))
	require.Nil(t, wait)
	observed, wait := cooldown.begin(key, 5, false, now.Add(25*time.Second))
	require.NotNil(t, wait)
	observed.finish(true, false, now.Add(26*time.Second))
	_, wait = cooldown.begin(key, 6, true, now.Add(27*time.Second))
	require.NotNil(t, wait)
	require.Equal(t, 1, wait.requestID)
	rejected, wait := cooldown.begin(key, 7, true, now.Add(compactionDelay))
	require.Nil(t, wait)
	rejected.finish(false, false, now.Add(compactionDelay))
	var admitted atomic.Int32
	var wg sync.WaitGroup
	var probe *compactionDispatch
	for i := range 16 {
		wg.Go(func() {
			attempt, _ := cooldown.begin(key, 10+i, true, now.Add(compactionDelay))
			if attempt != nil {
				admitted.Add(1)
				probe = attempt
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, admitted.Load())
	probe.finish(true, true, now.Add(45*time.Second))
	_, wait = cooldown.begin(key, 30, true, now.Add(46*time.Second))
	require.Equal(t, 29, wait.seconds)
	require.Equal(t, probe.requestID, wait.requestID)
	probe, wait = cooldown.begin(key, 31, true, now.Add(75*time.Second))
	require.Nil(t, wait)
	probe.finish(true, false, now.Add(76*time.Second))
	_, wait = cooldown.begin(key, 32, true, now.Add(76*time.Second))
	require.Nil(t, wait)
}

func TestCompactionTaskRequiresMatchingFrame(t *testing.T) {
	raw := &httpclient.Request{Headers: http.Header{}}
	set := func(kind, turn string) string {
		b, _ := json.Marshal(codex.TurnMetadata{RequestKind: kind, SessionID: "s", ThreadID: "t", TurnID: turn})
		return string(b)
	}
	raw.Headers.Set(codex.TurnMetadataHeader, set("compaction", "first"))
	_, ok := compactionTask(raw, false)
	require.True(t, ok)
	_, ok = compactionTask(raw, true)
	require.False(t, ok)
	for _, tt := range []struct {
		kind, turn string
		match      bool
	}{{"turn", "first", false}, {"compaction", "second", false}, {"compaction", "first", true}} {
		raw.JSONBody, _ = json.Marshal(map[string]any{"client_metadata": map[string]string{"x-codex-turn-metadata": set(tt.kind, tt.turn)}})
		_, ok = compactionTask(raw, true)
		require.Equal(t, tt.match, ok)
	}
	raw.Headers.Set(codex.SessionHeader, "unrelated-session")
	_, ok = compactionTask(raw, false)
	require.False(t, ok)
}

func TestCompactionCooldownScope(t *testing.T) {
	task := compactionIdentity{Kind: "compaction", Session: "s", Thread: "t", Turn: "op"}
	caller := ent.Request{ProjectID: 1, APIKeyID: 2, UserID: new(3), ModelID: "public"}
	channel := ent.Channel{ID: 7, UpdatedAt: time.Unix(15, 0)}
	wire := httpclient.Request{Method: "POST", URL: "https://supplier.invalid/v1/responses", Body: []byte("one"), Headers: http.Header{"X-Model-Option": {"a"}}}
	key := func() [32]byte {
		k, err := compactionRequestKey(&caller, &channel, task, &wire, false)
		require.NoError(t, err)
		return k
	}
	original := key()
	wire.Headers.Set("X-Trace-Id", "another-http-request")
	require.Equal(t, original, key())
	wire.Headers.Set("Cache-Control", "max-age=0")
	wire.Headers.Set("Accept", "application/json")
	require.Equal(t, original, key())
	caller.APIKeyID++
	require.NotEqual(t, original, key())
	caller.APIKeyID--
	channel.ID++
	require.NotEqual(t, original, key())
	channel.ID--
	task.Turn = "next"
	require.NotEqual(t, original, key())
	task.Turn = "op"
	wire.Body = []byte("two")
	require.NotEqual(t, original, key())
	wire.Body = []byte("one")
	wire.Headers.Set("X-Model-Option", "b")
	require.NotEqual(t, original, key())
	wire.Body = []byte(`{"model":"m","previous_response_id":null}`)
	codexNull, err := compactionRequestKey(&caller, &channel, task, &wire, true)
	require.NoError(t, err)
	genericNull := key()
	wire.Body = []byte(`{"model":"m"}`)
	codexAbsent, err := compactionRequestKey(&caller, &channel, task, &wire, true)
	require.NoError(t, err)
	require.Equal(t, codexAbsent, codexNull)
	require.NotEqual(t, genericNull, key())
}

func TestCompactionObserveCountsOverlappingExpiredProbes(t *testing.T) {
	var cooldown compactionCooldown
	key := [32]byte{1}
	now := time.Unix(10, 0)
	first, _ := cooldown.begin(key, 1, false, now)
	first.finish(true, true, now)
	probe, wait := cooldown.begin(key, 2, false, now.Add(compactionDelay))
	require.Nil(t, wait)
	other, wait := cooldown.begin(key, 3, false, now.Add(compactionDelay))
	require.NotNil(t, other)
	require.NotNil(t, wait)
	other.finish(true, false, now.Add(compactionDelay))
	probe.finish(true, false, now.Add(compactionDelay))
	_, wait = cooldown.begin(key, 4, false, now.Add(compactionDelay))
	require.Nil(t, wait)
}
