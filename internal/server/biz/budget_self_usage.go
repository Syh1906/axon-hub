package biz

import (
	"context"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
)

func (s *SelfUsageService) Budgets(ctx context.Context) ([]*BudgetView, error) {
	key, err := selfUsageKey(ctx)
	if err != nil {
		return nil, err
	}
	defaultTimezone := s.system.TimeLocation(ctx).String()
	return authz.RunWithSystemBypass(ctx, "self-usage-budgets", func(ctx context.Context) ([]*BudgetView, error) {
		current, err := s.client.APIKey.Query().Where(apikey.IDEQ(key.ID), apikey.ProjectIDEQ(key.ProjectID), apikey.StatusEQ(apikey.StatusEnabled)).Only(ctx)
		if err != nil {
			return nil, err
		}
		service := NewBudgetService(s.client)
		own, err := service.View(ctx, current.ProjectID, "key", current.ID, defaultTimezone)
		if err != nil {
			return nil, err
		}
		result := []*BudgetView{own}
		if current.GroupRef != nil {
			group, err := s.client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*current.GroupRef), apikeygroup.ProjectIDEQ(current.ProjectID)).Only(ctx)
			if err != nil {
				return nil, err
			}
			shared, err := service.View(ctx, current.ProjectID, "group", group.ID, defaultTimezone)
			if err != nil {
				return nil, err
			}
			result = append(result, shared)
		}
		return result, nil
	})
}
