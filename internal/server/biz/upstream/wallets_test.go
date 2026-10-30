package upstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/stretchr/testify/require"
)

type walletTransport func(*http.Request) (*http.Response, error)

func (f walletTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOfficialWalletQuery(t *testing.T) {
	for _, tc := range []struct {
		url, path, body, currency, amount string
		provider                          objects.UpstreamProvider
		count                             int
	}{
		{"https://api.deepseek.com/anthropic", "/user/balance", `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"0"},{"currency":"USD","total_balance":"1.2345"}]}`, "CNY", "0", objects.UpstreamProviderDeepSeek, 2},
		{"https://api.moonshot.ai/v1", "/v1/users/me/balance", `{"code":0,"status":true,"data":{"available_balance":5.25,"cash_balance":-1,"voucher_balance":5.25}}`, "USD", "5.25", objects.UpstreamProviderMoonshot, 1},
		{"https://api.moonshot.cn/anthropic", "/v1/users/me/balance", `{"code":0,"status":true,"data":{"available_balance":0}}`, "CNY", "0", objects.UpstreamProviderMoonshot, 1},
	} {
		t.Run(tc.url, func(t *testing.T) {
			calls := 0
			native := &http.Client{Transport: walletTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, tc.path, r.URL.Path)
				require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
				recorder := httptest.NewRecorder()
				recorder.Header().Set("Content-Type", "application/json")
				recorder.WriteString(tc.body)
				return recorder.Result(), nil
			})}
			client, err := NewClient(httpclient.NewHttpClientWithClient(native), tc.url)
			require.NoError(t, err)
			session := client.Prepare(t.Context(), "fixture-key", objects.UpstreamProviderUnknown, nil)
			require.Equal(t, tc.provider, session.Provider)
			result := session.Query(t.Context(), "fixture-key", EmptySnapshot(), true, true, time.Minute)
			require.Equal(t, 1, calls)
			require.Equal(t, objects.UpstreamQuerySuccess, result.BalanceState)
			require.Equal(t, objects.UpstreamQueryUnsupported, result.RateState)
			require.Len(t, result.Balances, tc.count)
			require.Equal(t, tc.currency, result.Balances[0].Currency)
			require.Equal(t, tc.amount, result.Balances[0].Amount.String())
		})
	}
}

func TestWalletInvalidResponses(t *testing.T) {
	for _, body := range []string{`{}`, `{"is_available":true,"balance_infos":[]}`, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"bad"}]}`} {
		var data map[string]any
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		require.NoError(t, decoder.Decode(&data))
		_, err := parseWallet(objects.UpstreamProviderDeepSeek, "api.deepseek.com", data)
		require.Error(t, err)
	}
}

func TestBillingDetectionAndPeriod(t *testing.T) {
	for _, tc := range []struct {
		name, subscription, amount string
		usage                      bool
		from, to                   string
	}{
		{"direct", `{"balance":12.5}`, "12.5", false, "", ""},
		{"known-period", `{"hard_limit_usd":100,"start_date":"2026-10-01","end_date":"2026-11-01"}`, "75", true, "2026-10-01", "2026-11-01"},
		{"midnight-timestamps", `{"hard_limit_usd":100,"start_date":1790812800,"end_date":1793491200}`, "75", true, "2026-10-01", "2026-11-01"},
		{"non-midnight-period", `{"hard_limit_usd":100,"start_date":"2026-10-01T12:00:00Z","end_date":"2026-11-01T12:00:00Z"}`, "", true, "2026-10-01", "2026-11-01"},
		{"offset-period", `{"hard_limit_usd":100,"start_date":"2026-10-01T00:00:00+08:00","end_date":"2026-11-01T00:00:00+08:00"}`, "", true, "2026-09-30", "2026-10-31"},
		{"same-day-period", `{"hard_limit_usd":100,"start_date":"2026-10-01T01:00:00Z","end_date":"2026-10-01T23:00:00Z"}`, "", true, "2026-10-01", "2026-10-01"},
		{"unknown-period", `{"hard_limit_usd":100}`, "", true, "", ""},
		{"unlimited", `{"hard_limit_usd":999999999}`, "", true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usageCalls := 0
			var queriedFrom, queriedTo string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/gateway/v1/dashboard/billing/subscription":
					w.Write([]byte(tc.subscription))
				case "/gateway/v1/dashboard/billing/usage":
					usageCalls++
					queriedFrom, queriedTo = r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date")
					w.Write([]byte(`{"total_usage":2500}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewClient(httpclient.NewHttpClient(), server.URL+"/gateway/v1")
			require.NoError(t, err)
			session := client.Prepare(t.Context(), "fixture", objects.UpstreamProviderUnknown, nil)
			require.Equal(t, objects.UpstreamProviderBilling, session.Provider)
			result := session.Query(t.Context(), "fixture", EmptySnapshot(), true, true, time.Minute)
			require.Equal(t, objects.UpstreamQuerySuccess, result.BalanceState)
			require.Len(t, result.Balances, 1)
			b := result.Balances[0]
			require.Equal(t, objects.UpstreamBalanceQuota, b.Kind)
			if tc.amount == "" {
				require.Nil(t, b.Amount)
			} else {
				require.Equal(t, tc.amount, b.Amount.String())
			}
			if tc.usage {
				require.Equal(t, 1, usageCalls)
				if tc.from != "" {
					require.Equal(t, tc.from, queriedFrom)
					require.Equal(t, tc.to, queriedTo)
				}
				require.Equal(t, "25", b.Used.String())
				require.Equal(t, queriedFrom+"T00:00:00Z", b.UsageStart.Format(time.RFC3339))
				require.Equal(t, queriedTo+"T00:00:00Z", b.UsageEnd.Format(time.RFC3339))
				if tc.name != "unlimited" {
					require.Equal(t, "100", b.Limit.String())
				}
				if tc.amount == "" {
					require.Nil(t, b.ExpiresAt)
				}
			} else {
				require.Zero(t, usageCalls)
			}
			require.Equal(t, tc.name == "unlimited", b.Unlimited)
		})
	}
}
