package upstream

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

func EmptySnapshot() *objects.UpstreamKeySnapshot {
	return &objects.UpstreamKeySnapshot{Provider: objects.UpstreamProviderUnknown, BalanceState: objects.UpstreamQueryNever, RateState: objects.UpstreamQueryNever, Balances: []objects.UpstreamBalance{}}
}

func Due(state objects.UpstreamQueryState, next *time.Time, force bool, now time.Time) bool {
	if force {
		return true
	}
	if state == objects.UpstreamQueryUnsupported || state == objects.UpstreamQueryAuthRequired {
		return false
	}
	return next == nil || !now.Before(*next)
}

func (s *Session) Query(ctx context.Context, key string, previous *objects.UpstreamKeySnapshot, balanceDue, rateDue bool, interval time.Duration) *objects.UpstreamKeySnapshot {
	result := *previous
	result.Provider = s.Provider
	now, next := time.Now().UTC(), time.Now().UTC().Add(interval)
	if balanceDue {
		var balances []objects.UpstreamBalance
		var err error
		switch s.Provider {
		case objects.UpstreamProviderNewAPI:
			balances, err = s.newAPIBalance(ctx, key)
		case objects.UpstreamProviderSub2API:
			balances, err = s.sub2APIBalance(ctx, key)
		default:
			code := s.AccountErrorCode
			if code == "" {
				code = "UNSUPPORTED"
			}
			err = &QueryError{Code: code}
		}
		result.BalanceCheckedAt, result.NextBalanceCheckAt = &now, &next
		if err != nil {
			result.BalanceState, result.BalanceErrorCode = ErrorState(err), ErrorCode(err)
		} else {
			result.Balances, result.BalanceState, result.BalanceErrorCode, result.BalanceUpdatedAt = balances, objects.UpstreamQuerySuccess, "", &now
		}
	}
	if rateDue {
		var rate *objects.UpstreamRateSnapshot
		var err error
		switch s.Provider {
		case objects.UpstreamProviderNewAPI:
			rate, result.UpstreamTokenID, result.UpstreamAccountID, err = s.newAPIRate(ctx, key, previous)
		case objects.UpstreamProviderSub2API:
			rate, err = s.sub2APIRate(ctx, key)
		default:
			code := s.AccountErrorCode
			if code == "" {
				code = "UNSUPPORTED"
			}
			err = &QueryError{Code: code}
		}
		result.RateCheckedAt, result.NextRateCheckAt = &now, &next
		if err != nil {
			result.RateState, result.RateErrorCode = ErrorState(err), ErrorCode(err)
			if result.RateState != objects.UpstreamQueryError {
				result.Rate = nil
			}
		} else {
			result.Rate, result.RateState, result.RateErrorCode = rate, objects.UpstreamQuerySuccess, ""
			if rate.ValidUntil != nil && rate.ValidUntil.Before(next) {
				result.NextRateCheckAt = rate.ValidUntil
			}
		}
		if s.Account != nil && result.UpstreamAccountID == s.Account.ID {
			result.Account = &objects.UpstreamAccountSnapshot{AccountID: s.Account.ID, Balance: s.Account.Balance, UpdatedAt: s.Account.SyncedAt}
		}
	}
	return &result
}
