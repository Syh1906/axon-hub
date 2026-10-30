package biz

import (
	"context"
	"fmt"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
)

// DisplayTimezone exposes only the display convention to signed-in users,
// without granting access to editable system settings.
func (s *SystemService) DisplayTimezone(ctx context.Context) (string, error) {
	if user, ok := contexts.GetUser(ctx); !ok || user == nil {
		return "", fmt.Errorf("display timezone requires an authenticated user")
	}
	return authz.RunWithSystemBypass(ctx, "system-display-timezone", func(ctx context.Context) (string, error) {
		return s.TimeLocation(ctx).String(), nil
	})
}
