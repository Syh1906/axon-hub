package upstream

import (
	"context"
	"time"
	_ "time/tzdata"

	"github.com/looplj/axonhub/internal/objects"
)

func (s *Session) sub2APIBalance(ctx context.Context, key string) ([]objects.UpstreamBalance, error) {
	data, err := s.Client.request(ctx, "/v1/usage", key, 0)
	if err != nil {
		return nil, err
	}
	valid, ok := data["isValid"].(bool)
	if !ok {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	if !valid {
		return nil, &QueryError{Code: "UNAUTHORIZED"}
	}
	balance := objects.UpstreamBalance{Currency: "USD", Unit: "CURRENCY", Used: optionalNumber(record(record(data["usage"])["total"])["actual_cost"])}
	switch {
	case text(data["mode"]) == "quota_limited":
		quota := record(data["quota"])
		balance.Kind = objects.UpstreamBalanceKey
		balance.Amount, balance.Limit = optionalNumber(quota["remaining"]), optionalNumber(quota["limit"])
		if used := optionalNumber(quota["used"]); used != nil {
			balance.Used = used
		}
		balance.ExpiresAt = timestamp(data["expires_at"])
	case data["balance"] != nil:
		balance.Kind, balance.Amount = objects.UpstreamBalanceAccount, optionalNumber(data["balance"])
	default:
		balance.Kind, balance.Amount = objects.UpstreamBalanceSubscription, optionalNumber(data["remaining"])
		balance.Unlimited = balance.Amount != nil && balance.Amount.IsNegative()
		if balance.Unlimited {
			balance.Amount = nil
		}
		balance.ExpiresAt = timestamp(record(data["subscription"])["expires_at"])
	}
	if balance.Amount == nil && balance.Used == nil && !balance.Unlimited {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	return []objects.UpstreamBalance{balance}, nil
}

func (s *Session) sub2APIRate(ctx context.Context, key string) (*objects.UpstreamRateSnapshot, error) {
	data, err := s.Client.request(ctx, "/v1/sub2api/billing", key, 0)
	if err != nil {
		return nil, err
	}
	if text(data["object"]) != "sub2api.key_billing" || positiveID(data["schema_version"]) != 1 || text(data["billing_scope"]) != "token" {
		return nil, &QueryError{Code: "UNSUPPORTED_RATE_SCHEMA"}
	}
	multiplier, ok := number(data["effective_rate_multiplier"])
	observedAt := timestamp(data["observed_at"])
	if !ok || multiplier.IsNegative() || observedAt == nil {
		return nil, &QueryError{Code: "INVALID_RESPONSE"}
	}
	now := time.Now().UTC()
	if observedAt.After(now.Add(time.Minute)) || observedAt.Before(now.Add(-30*time.Minute)) {
		return nil, &QueryError{Code: "STALE_RESPONSE"}
	}
	rate := &objects.UpstreamRateSnapshot{Multiplier: multiplier, ObservedAt: *observedAt, SyncedAt: now}
	if data["peak_rate_enabled"] == true {
		location, err := time.LoadLocation(text(data["timezone"]))
		if err != nil {
			return nil, &QueryError{Code: "UNSUPPORTED_RATE_SCHEMA"}
		}
		local := observedAt.In(location)
		for _, field := range []string{"peak_start", "peak_end"} {
			clock, err := time.Parse("15:04", text(data[field]))
			if err != nil {
				return nil, &QueryError{Code: "UNSUPPORTED_RATE_SCHEMA"}
			}
			boundary := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
			if !boundary.After(local) {
				boundary = boundary.AddDate(0, 0, 1)
			}
			if rate.ValidUntil == nil || boundary.Before(*rate.ValidUntil) {
				rate.ValidUntil = &boundary
			}
		}
	}
	return rate, nil
}
