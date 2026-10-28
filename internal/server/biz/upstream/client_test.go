package upstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestNewAPIAccountAssociationAndCurrentRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`))
		case "/api/user/self":
			if r.Header.Get("Authorization") != "Bearer fixture-account" {
				w.WriteHeader(401)
				return
			}
			if r.Header.Get("New-Api-User") != "17" {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"message":"New-Api-User is required"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":17,"group":"default","quota":5000000,"used_quota":1000000}}`))
		case "/api/log/token":
			switch r.Header.Get("Authorization") {
			case "Bearer fixture-a":
				_, _ = w.Write([]byte(`{"success":true,"data":[{"token_id":11,"user_id":17,"group":"old-group"}]}`))
			case "Bearer fixture-b":
				_, _ = w.Write([]byte(`{"success":true,"data":[{"token_id":12,"user_id":17}]}`))
			case "Bearer fixture-other":
				_, _ = w.Write([]byte(`{"success":true,"data":[{"token_id":13,"user_id":99}]}`))
			default:
				_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
			}
		case "/api/usage/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"object":"token_usage","total_available":1000000,"total_granted":1500000,"total_used":500000,"unlimited_quota":false}}`))
		case "/api/token/11":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":11,"user_id":17,"group":"current-group"}}`))
		case "/api/token/12":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":12,"user_id":17,"group":"free-group"}}`))
		case "/api/user/self/groups":
			_, _ = w.Write([]byte(`{"success":true,"data":{"current-group":{"ratio":0.5},"free-group":{"ratio":0}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(httpclient.NewHttpClient(), server.URL+"/v1")
	require.NoError(t, err)
	session := client.Prepare(t.Context(), "fixture-a", objects.UpstreamProviderUnknown, &objects.ChannelUpstreamCredentials{AccountToken: "fixture-account"})
	require.Equal(t, objects.UpstreamProviderNewAPI, session.Provider)
	require.NotNil(t, session.Account)
	require.Equal(t, 17, session.Account.ID)
	a := session.Query(t.Context(), "fixture-a", EmptySnapshot(), true, true, 10*time.Minute)
	b := session.Query(t.Context(), "fixture-b", EmptySnapshot(), true, true, 10*time.Minute)
	require.Equal(t, "2", a.Balances[0].Amount.String())
	require.Equal(t, "USD", a.Balances[0].Currency)
	require.Equal(t, "0.5", a.Rate.Multiplier.String())
	require.Equal(t, "current-group", a.Rate.Group, "the current token group must replace the historical log group")
	require.Equal(t, "0", b.Rate.Multiplier.String(), "zero is a valid rate")
	require.Equal(t, a.Account.Balance.AccountRef, b.Account.Balance.AccountRef)
	require.Equal(t, "10", a.Account.Balance.Amount.String())
	other := session.Query(t.Context(), "fixture-other", EmptySnapshot(), true, true, time.Minute)
	require.Equal(t, objects.UpstreamQueryUnmatched, other.RateState)
	require.Nil(t, other.Account)
	newKey := session.Query(t.Context(), "fixture-unused", EmptySnapshot(), true, true, time.Minute)
	require.Equal(t, objects.UpstreamQueryUnmatched, newKey.RateState)
	require.Nil(t, newKey.Rate)
	encoded, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "fixture-account")
	require.NotContains(t, string(encoded), "fixture-a")
}

func TestSub2APIIndependentResultsAndUnsupportedBilling(t *testing.T) {
	balanceFailure, oldVersion := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/usage":
			if balanceFailure {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write([]byte(`{"isValid":true,"mode":"quota_limited","quota":{"remaining":0,"limit":20,"used":20}}`))
		case "/v1/sub2api/billing":
			if oldVersion {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "sub2api.key_billing", "schema_version": 1, "billing_scope": "token", "effective_rate_multiplier": 0.8, "observed_at": time.Now().UTC().Format(time.RFC3339), "peak_rate_enabled": true, "timezone": "Asia/Shanghai", "peak_start": "10:00", "peak_end": "23:00"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(httpclient.NewHttpClient(), server.URL)
	require.NoError(t, err)
	session := client.Prepare(t.Context(), "fixture-key", objects.UpstreamProviderUnknown, nil)
	require.Equal(t, objects.UpstreamProviderSub2API, session.Provider)
	first := session.Query(t.Context(), "fixture-key", EmptySnapshot(), true, true, time.Minute)
	require.Equal(t, "0", first.Balances[0].Amount.String())
	require.Equal(t, "0.8", first.Rate.Multiplier.String())
	require.NotNil(t, first.Rate.ValidUntil)
	require.True(t, first.Rate.ValidUntil.After(first.Rate.ObservedAt))
	balanceFailure = true
	second := session.Query(t.Context(), "fixture-key", first, true, true, time.Minute)
	require.Equal(t, objects.UpstreamQueryError, second.BalanceState)
	require.Equal(t, objects.UpstreamQuerySuccess, second.RateState)
	require.Equal(t, first.BalanceUpdatedAt, second.BalanceUpdatedAt)
	oldVersion = true
	third := session.Query(t.Context(), "fixture-key", second, true, true, time.Minute)
	require.Equal(t, objects.UpstreamQueryUnsupported, third.RateState)
	require.Nil(t, third.Rate)
	require.False(t, Due(third.RateState, nil, false, time.Now()))
	require.True(t, Due(third.RateState, nil, true, time.Now()))
}

func TestDetectionDoesNotTreatTemporaryFailureAsUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			w.WriteHeader(503)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(httpclient.NewHttpClient(), server.URL)
	require.NoError(t, err)
	session := client.Prepare(t.Context(), "fixture", objects.UpstreamProviderUnknown, nil)
	result := session.Query(t.Context(), "fixture", EmptySnapshot(), true, true, time.Minute)
	require.Equal(t, objects.UpstreamQueryError, result.RateState)
	require.Equal(t, objects.UpstreamQueryError, result.BalanceState)
	require.True(t, Due(result.RateState, result.NextRateCheckAt, false, time.Now().Add(2*time.Minute)))
}
