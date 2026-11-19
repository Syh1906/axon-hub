package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
)

func TestWebSocketEvidenceScopeExpiryAndLateResult(t *testing.T) {
	s := new(ChannelService)
	k := WebSocketScope{9, "config", "model", "endpoint", "account"}
	at := time.Now()
	observe := func(start time.Time, reason string) {
		s.RecordWebSocketObservation(k, k.Model, "https://example.invalid/v1/responses", "••••1234", "traffic", start, httpclient.WebSocketObservation{Reason: reason, Handshake: 80 * time.Millisecond})
	}
	observe(at, "verified")
	for _, other := range []WebSocketScope{{9, "changed", "model", "endpoint", "account"}, {9, "config", "other", "endpoint", "account"}, {9, "config", "model", "other", "account"}, {9, "config", "model", "endpoint", "other"}} {
		reason, _ := s.WebSocketDecision(other)
		require.Equal(t, "websocket_unverified", reason)
	}
	observe(at.Add(-time.Second), "no_available_account")
	observe(at.Add(time.Second), "accepted")
	reason, timeout := s.WebSocketDecision(k)
	require.Empty(t, reason)
	require.Greater(t, timeout, time.Duration(0))
	observe(at.Add(2*time.Second), "no_available_account")
	reason, _ = s.WebSocketDecision(k)
	require.NotEmpty(t, reason)
	expired := s.wsEvidence.entries[k]
	past := at.Add(-time.Second)
	expired.ExpiresAt = &past
	s.wsEvidence.entries[k] = expired
	reason, _ = s.WebSocketDecision(k)
	require.Equal(t, "websocket_unverified", reason)
}

func TestWebSocketTrafficDiscoveryExpiryAndCancellation(t *testing.T) {
	s := new(ChannelService)
	key := WebSocketScope{ChannelID: 1, Model: "model"}
	s.RecordWebSocketObservation(key, key.Model, "", "", "probe", time.Now(), httpclient.WebSocketObservation{Reason: "connection_failed"})
	reason, _ := s.WebSocketTrafficDecision(t.Context(), key)
	require.Equal(t, "websocket_temporarily_unavailable", reason)
	old := s.wsEvidence.entries[key]
	past := time.Now().Add(-time.Minute)
	old.ExpiresAt = &past
	s.wsEvidence.entries[key] = old
	ctx, cancel := context.WithCancel(t.Context())
	reason, budget := s.WebSocketTrafficDecision(ctx, key)
	require.Empty(t, reason)
	require.Equal(t, 3*time.Second, budget)
	reason, _ = s.WebSocketTrafficDecision(t.Context(), key)
	require.Equal(t, "websocket_discovery_in_progress", reason)
	cancel()
	require.Eventually(t, func() bool {
		reason, _ := s.WebSocketTrafficDecision(t.Context(), key)
		return reason == ""
	}, time.Second, time.Millisecond)
}

func TestWebSocketProbeSerializesFinalDestination(t *testing.T) {
	s := new(ChannelService)
	release, err := s.AcquireWebSocketProbe(t.Context(), "https://fixture.test/first")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.AcquireWebSocketProbe(ctx, "wss://fixture.test/second")
	require.ErrorIs(t, err, context.Canceled)
	other, err := s.AcquireWebSocketProbe(t.Context(), "https://other.test/first")
	require.NoError(t, err)
	other()
	release()
	require.Empty(t, s.wsEvidence.gates)
}

func TestWebSocketEvidenceBounded(t *testing.T) {
	s := new(ChannelService)
	for i := range 1030 {
		s.RecordWebSocketObservation(WebSocketScope{ChannelID: i}, "", "", "", "probe", time.Now(), httpclient.WebSocketObservation{Reason: "verified"})
	}
	require.Len(t, s.wsEvidence.entries, 1024)
}
