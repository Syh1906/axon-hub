package biz

import (
	"context"
	"fmt"
	"strings"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
)

func (s *APIKeyService) groupProject(ctx context.Context, write bool) (int, error) {
	projectID, ok := contexts.GetProjectID(ctx)
	if !ok || projectID <= 0 {
		return 0, fmt.Errorf("project context is required")
	}
	if write {
		user, ok := contexts.GetUser(ctx)
		if !ok {
			return 0, fmt.Errorf("authenticated user is required")
		}
		if err := s.requireProjectAdmin(ctx, user.ID, projectID); err != nil {
			return 0, err
		}
	}
	return projectID, nil
}

func (s *APIKeyService) APIKeyGroups(ctx context.Context) ([]*ent.APIKeyGroup, error) {
	projectID, err := s.groupProject(ctx, false)
	if err != nil {
		return nil, err
	}
	return s.entFromContext(ctx).APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(projectID)).Order(ent.Asc(apikeygroup.FieldName)).All(ctx)
}

func (s *APIKeyService) SaveAPIKeyGroup(ctx context.Context, id *int, name string) (*ent.APIKeyGroup, error) {
	projectID, err := s.groupProject(ctx, true)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 128 {
		return nil, fmt.Errorf("group name must contain 1 to 128 characters")
	}
	client := s.entFromContext(ctx)
	if id == nil {
		return client.APIKeyGroup.Create().SetProjectID(projectID).SetName(name).Save(ctx)
	}
	group, err := client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*id), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	return group.Update().SetName(name).Save(ctx)
}

func (s *APIKeyService) DeleteAPIKeyGroup(ctx context.Context, id int) error {
	projectID, err := s.groupProject(ctx, true)
	if err != nil {
		return err
	}
	return s.RunInTransaction(ctx, func(ctx context.Context) error {
		if err := s.lockProjectForAPIKeyName(ctx, projectID); err != nil {
			return err
		}
		client := s.entFromContext(ctx)
		group, err := client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(id), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
		if err != nil {
			return err
		}
		if _, err := client.APIKey.Update().Where(apikey.ProjectIDEQ(projectID), apikey.GroupRefEQ(id)).ClearGroupRef().Save(ctx); err != nil {
			return err
		}
		return client.APIKeyGroup.DeleteOne(group).Exec(ctx)
	})
}

// Assignment grants group usage visibility, so it requires project administration.
func (s *APIKeyService) validateAPIKeyGroup(ctx context.Context, projectID int, id *int) error {
	user, ok := contexts.GetUser(ctx)
	if !ok {
		return fmt.Errorf("authenticated user is required")
	}
	if err := s.requireProjectAdmin(ctx, user.ID, projectID); err != nil {
		return err
	}
	if id == nil {
		return nil
	}
	_, err := s.entFromContext(ctx).APIKeyGroup.Query().Where(apikeygroup.IDEQ(*id), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
	return err
}
