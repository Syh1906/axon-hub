package upstream

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/shopspring/decimal"
)

// QueryVersion invalidates only old unsupported detections when new sources ship.
const QueryVersion = 1

func WalletProvider(host string) objects.UpstreamProvider {
	switch strings.ToLower(host) {
	case "api.deepseek.com":
		return objects.UpstreamProviderDeepSeek
	case "api.moonshot.cn", "api.moonshot.ai":
		return objects.UpstreamProviderMoonshot
	default:
		return objects.UpstreamProviderUnknown
	}
}

func (s *Session) walletBalance(ctx context.Context, key string) ([]objects.UpstreamBalance, error) {
	path := "/user/balance"
	if s.Provider == objects.UpstreamProviderMoonshot {
		path = "/v1/users/me/balance"
	}
	data, err := s.Client.request(ctx, path, key, 0)
	if err != nil {
		return nil, err
	}
	return parseWallet(s.Provider, s.Client.base.Hostname(), data)
}

func parseWallet(provider objects.UpstreamProvider, host string, data map[string]any) ([]objects.UpstreamBalance, error) {
	balances := []objects.UpstreamBalance{}
	if provider == objects.UpstreamProviderDeepSeek {
		if _, ok := data["is_available"].(bool); !ok {
			return nil, &QueryError{Code: "INVALID_RESPONSE"}
		}
		entries, _ := data["balance_infos"].([]any)
		seen := map[string]bool{}
		for _, entry := range entries {
			info := record(entry)
			amount := optionalNumber(info["total_balance"])
			currency := text(info["currency"])
			if amount == nil || (currency != "CNY" && currency != "USD") || seen[currency] {
				return nil, &QueryError{Code: "INVALID_RESPONSE"}
			}
			seen[currency] = true
			balances = append(balances, objects.UpstreamBalance{Kind: objects.UpstreamBalanceAccount, Amount: amount, Currency: currency, Unit: "CURRENCY"})
		}
	} else {
		code, ok := number(data["code"])
		if data["status"] != true || !ok || !code.IsZero() {
			return nil, &QueryError{Code: "UPSTREAM_REJECTED"}
		}
		amount := optionalNumber(record(data["data"])["available_balance"])
		if amount != nil {
			currency := "CNY"
			if strings.EqualFold(host, "api.moonshot.ai") {
				currency = "USD"
			}
			balances = append(balances, objects.UpstreamBalance{Kind: objects.UpstreamBalanceAccount, Amount: amount, Currency: currency, Unit: "CURRENCY"})
		}
	}
	if len(balances) == 0 {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	return balances, nil
}

func billingRecord(data map[string]any) map[string]any {
	if nested := record(data["data"]); nested != nil {
		return nested
	}
	return data
}

func isBilling(data map[string]any) bool {
	data = billingRecord(data)
	return optionalNumber(data["balance"]) != nil || optionalNumber(data["hard_limit_usd"]) != nil
}

func (s *Session) billingBalance(ctx context.Context, key string) ([]objects.UpstreamBalance, error) {
	data, err := s.Client.request(ctx, "/v1/dashboard/billing/subscription", key, 0)
	if err != nil {
		return nil, err
	}
	data = billingRecord(data)
	if !isBilling(data) {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	// A compatible billing endpoint does not establish the wallet's owner or scope.
	balance := objects.UpstreamBalance{Kind: objects.UpstreamBalanceQuota, Currency: "USD", Unit: "CURRENCY", Amount: optionalNumber(data["balance"])}
	if balance.Amount != nil {
		return []objects.UpstreamBalance{balance}, nil
	}
	balance.Limit = optionalNumber(data["hard_limit_usd"])
	if balance.Limit != nil && balance.Limit.GreaterThanOrEqual(decimal.NewFromInt(1_000_000)) {
		balance.Unlimited, balance.Limit = true, nil
	}
	start, end := billingDate(data["start_date"]), billingDate(data["end_date"])
	periodKnown := start != nil && end != nil && end.After(*start)
	now := time.Now().UTC()
	from, to := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC), now
	if periodKnown {
		from, to = *start, *end
	}
	// Billing usage accepts dates only. Record the actual UTC query boundaries,
	// and calculate a remainder only if no precision was lost from the limit period.
	from, to = from.UTC().Truncate(24*time.Hour), to.UTC().Truncate(24*time.Hour)
	periodMatches := periodKnown && start.Equal(from) && end.Equal(to)
	balance.UsageStart, balance.UsageEnd = &from, &to
	usage, err := s.Client.requestWithQuery(ctx, "/v1/dashboard/billing/usage", key, 0, url.Values{"start_date": {from.Format("2006-01-02")}, "end_date": {to.Format("2006-01-02")}})
	if err != nil {
		return nil, err
	}
	usage = billingRecord(usage)
	if cents := optionalNumber(usage["total_usage"]); cents != nil {
		used := cents.Div(decimal.NewFromInt(100))
		balance.Used = &used
	} else {
		balance.Used = optionalNumber(usage["used_usd"])
	}
	if balance.Used == nil {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	// Never subtract usage from a limit with an unknown or different period.
	if periodMatches && balance.Limit != nil {
		remaining := decimal.Max(decimal.Zero, balance.Limit.Sub(*balance.Used))
		balance.Amount, balance.ExpiresAt = &remaining, end
	}
	return []objects.UpstreamBalance{balance}, nil
}

func billingDate(value any) *time.Time {
	if parsed, err := time.Parse("2006-01-02", text(value)); err == nil {
		return &parsed
	}
	return timestamp(value)
}
