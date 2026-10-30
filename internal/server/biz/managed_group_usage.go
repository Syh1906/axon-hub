package biz

import (
	"context"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/scopes"
)

// Management uses the signed-in user and explicit project, never a member key.
// Disabled/empty groups can therefore inspect their permitted historical usage.
func (s *APIKeyService) managedUsageGroup(ctx context.Context, groupRef int) (int, *ent.APIKeyGroup, error) {
	if user, ok := contexts.GetUser(ctx); !ok || user == nil {
		return 0, nil, fmt.Errorf("authenticated user is required")
	}
	if err := authz.RequireScope(ctx, scopes.ScopeReadAPIKeys); err != nil {
		return 0, nil, err
	}
	projectID, err := s.groupProject(ctx, false)
	if err != nil {
		return 0, nil, err
	}
	if groupRef < 0 {
		return 0, nil, fmt.Errorf("invalid group")
	}
	if groupRef == 0 {
		return projectID, nil, nil
	}
	group, err := s.entFromContext(ctx).APIKeyGroup.Query().Where(apikeygroup.IDEQ(groupRef), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
	return projectID, group, err
}

func (s *APIKeyService) ManagedGroupUsageMeta(ctx context.Context, system *SystemService, groupRef int) (*SelfUsageMeta, error) {
	_, group, err := s.managedUsageGroup(ctx, groupRef)
	if err != nil {
		return nil, err
	}
	return authz.RunWithSystemBypass(ctx, "managed-group-usage-settings", func(ctx context.Context) (*SelfUsageMeta, error) {
		settings, err := system.GeneralSettings(ctx)
		if err != nil {
			return nil, err
		}
		meta := &SelfUsageMeta{APIKeyType: "group", CurrencyCode: settings.CurrencyCode, Timezone: system.TimeLocation(ctx).String(), MaxRangeDays: selfUsageMaxRangeDays}
		if group != nil {
			meta.GroupName = &group.Name
			meta.APIKeyName = group.Name
		}
		return meta, nil
	})
}

func (s *APIKeyService) managedGroupUsageScope(ctx context.Context, system *SystemService, groupRef int, start, end string) (selfUsageScope, time.Time, error) {
	projectID, group, err := s.managedUsageGroup(ctx, groupRef)
	if err != nil {
		return selfUsageScope{}, time.Time{}, err
	}
	first, last, err := selfUsageDateRange(start, end, system.TimeLocation(ctx))
	if err != nil {
		return selfUsageScope{}, time.Time{}, err
	}
	scope := selfUsageScope{id: fmt.Sprintf("managed-group:%d:%d", projectID, groupRef), projectID: projectID, start: first.UTC(), end: last.AddDate(0, 0, 1).UTC()}
	q := s.entFromContext(ctx).APIKey.Query().Where(apikey.ProjectIDEQ(projectID))
	if group != nil {
		q.Where(apikey.GroupRefEQ(group.ID))
		scope.groupName, scope.apiKeyName = &group.Name, group.Name
	} else {
		q.Where(apikey.GroupRefIsNil())
	}
	scope.keyIDs, err = q.IDs(ctx)
	return scope, first, err
}

func (s *APIKeyService) ManagedGroupUsageStats(ctx context.Context, system *SystemService, groupRef int, start, end string) (*SelfUsageStats, error) {
	scope, first, err := s.managedGroupUsageScope(ctx, system, groupRef, start, end)
	if err != nil {
		return nil, err
	}
	return NewSelfUsageService(s.entFromContext(ctx), system).statsForScope(ctx, scope, first, start, end)
}

func (s *APIKeyService) ManagedGroupUsageRequests(ctx context.Context, system *SystemService, groupRef int, start, end string, model *string, status string, page, pageSize int) (*SelfUsageRequestPage, error) {
	scope, _, err := s.managedGroupUsageScope(ctx, system, groupRef, start, end)
	if err != nil {
		return nil, err
	}
	return NewSelfUsageService(s.entFromContext(ctx), system).requestsForScope(ctx, scope, model, status, page, pageSize)
}
