package biz

import (
	"context"
	"fmt"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
)

// CurrencyDisplayDecimals exposes only the amount display preference to signed-in users.
func (s *SystemService) CurrencyDisplayDecimals(ctx context.Context) (*int, error) {
	if user, ok := contexts.GetUser(ctx); !ok || user == nil {
		return nil, fmt.Errorf("currency display decimals requires an authenticated user")
	}
	return authz.RunWithSystemBypass(ctx, "system-currency-display-decimals", func(ctx context.Context) (*int, error) {
		settings, err := s.GeneralSettings(ctx)
		if err != nil {
			return nil, err
		}
		return settings.CurrencyDisplayDecimals, nil
	})
}
