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
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("New-Api-User") != "17" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"New-Api-User is required"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":17,"group":"default","quota":5000000,"used_quota":1000000}}`))
		case "/api/log/token":
			require.NotEqual(t, "Bearer fixture-unused", r.Header.Get("Authorization"), "新 Key 关联不应依赖调用记录")
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
		case "/api/token/search":
			require.Equal(t, "Bearer fixture-account", r.Header.Get("Authorization"))
			require.Equal(t, "17", r.Header.Get("New-Api-User"))
			switch r.URL.Query().Get("token") {
			case "fixture-a":
				_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":11,"user_id":17}]}}`))
			case "fixture-b":
				_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":12,"user_id":17}]}}`))
			case "fixture-unused":
				_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":14,"user_id":17}]}}`))
			default:
				_, _ = w.Write([]byte(`{"success":true,"data":{"items":[]}}`))
			}
		case "/api/token/11":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":11,"user_id":17,"group":"current-group"}}`))
		case "/api/token/12":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":12,"user_id":17,"group":"free-group"}}`))
		case "/api/token/14":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":14,"user_id":17,"group":"current-group"}}`))
		case "/api/user/self/groups":
			_, _ = w.Write([]byte(`{"success":true,"data":{"current-group":{"ratio":0.5},"free-group":{"ratio":0}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(httpclient.NewHttpClient(), server.URL+"/v1")
	require.NoError(t, err)
	session := client.Prepare(t.Context(), "fixture-a", objects.UpstreamProviderUnknown, &objects.UpstreamAccountCredentials{AccountToken: "fixture-account"})
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
	require.Equal(t, "10", other.Account.Balance.Amount.String(), "所选账号的余额不依赖模型 Key 归属核验")
	require.Zero(t, other.UpstreamAccountID, "余额查询成功不能冒认 Key 归属")
	newKey := session.Query(t.Context(), "fixture-unused", EmptySnapshot(), true, true, time.Minute)
	require.Equal(t, objects.UpstreamQuerySuccess, newKey.RateState)
	require.Equal(t, "0.5", newKey.Rate.Multiplier.String())
	require.Equal(t, a.Account.Balance.AccountRef, newKey.Account.Balance.AccountRef)
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
				w.WriteHeader(http.StatusServiceUnavailable)
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
			w.WriteHeader(http.StatusServiceUnavailable)
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

func TestNewAPICompleteKeySearchAndIndependentAccountBalance(t *testing.T) {
	for _, tc := range []struct {
		name, search, details, errorCode string
	}{
		{"complete-key", `{"items":[{"id":11,"user_id":17}],"total":1}`, `{"id":11,"user_id":17,"group":"standard"}`, ""},
		{"empty-search", `{"items":[],"total":0}`, "", "KEY_SEARCH_NOT_FOUND"},
		{"ambiguous-search", `{"items":[{"id":11,"user_id":17}],"total":2}`, "", "KEY_SEARCH_AMBIGUOUS"},
		{"wrong-account", `{"items":[{"id":11,"user_id":99}],"total":1}`, "", "ACCOUNT_MISMATCH"},
		{"wrong-details", `{"items":[{"id":11,"user_id":17}],"total":1}`, `{"id":12,"user_id":17}`, "KEY_DETAILS_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				data := ""
				switch r.URL.Path {
				case "/api/status":
					data = `{"quota_per_unit":500000,"quota_display_type":"USD"}`
				case "/api/user/self":
					data = `{"id":17,"quota":5000000,"group":"standard"}`
				case "/api/usage/token/":
					data = `{"object":"token_usage","total_available":500000,"total_granted":500000,"total_used":0}`
				case "/api/token/search":
					// Gateways that search hashed credentials need the full original key.
					if r.URL.Query().Get("token") != "sk-fixture-full" || r.URL.Query().Get("p") != "1" || r.URL.Query().Get("size") != "2" {
						data = `{"items":[],"total":0}`
					} else {
						data = tc.search
					}
				case "/api/token/11":
					data = tc.details
				case "/api/user/self/groups":
					data = `{"standard":{"ratio":0.5}}`
				default:
					t.Errorf("unexpected request path: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"success":true,"data":` + data + `}`))
			}))
			defer server.Close()
			client, err := NewClient(httpclient.NewHttpClient(), server.URL)
			require.NoError(t, err)
			session := client.Prepare(t.Context(), "sk-fixture-full", objects.UpstreamProviderNewAPI, &objects.UpstreamAccountCredentials{AccountToken: "fixture-account", AccountID: 17})
			result := session.Query(t.Context(), "sk-fixture-full", EmptySnapshot(), true, true, time.Minute)
			require.Equal(t, tc.errorCode, result.RateErrorCode)
			require.NotNil(t, result.Account)
			require.Equal(t, "10", result.Account.Balance.Amount.String())
			require.NotNil(t, result.AccountCheckedAt)
			require.Empty(t, result.AccountErrorCode)
			if tc.errorCode != "" {
				require.Equal(t, objects.UpstreamQueryUnmatched, result.RateState)
				require.Nil(t, result.Rate)
				require.Zero(t, result.UpstreamAccountID)
			} else {
				require.Equal(t, "0.5", result.Rate.Multiplier.String())
			}
			accountOnly := session.Query(t.Context(), "sk-fixture-full", EmptySnapshot(), false, false, time.Minute)
			require.Equal(t, "10", accountOnly.Account.Balance.Amount.String(), "余额保存不依赖倍率分支是否到期")
		})
	}
}
