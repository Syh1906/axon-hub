package biz

import (
	"context"
	"fmt"
	"strings"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
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
	var result *ent.APIKeyGroup
	err = s.RunInTransaction(ctx, func(ctx context.Context) error {
		if err := s.lockProjectForAPIKeyName(ctx, projectID); err != nil {
			return err
		}
		client := s.entFromContext(ctx)
		if id == nil {
			var err error
			result, err = client.APIKeyGroup.Create().SetProjectID(projectID).SetName(name).Save(ctx)
			return err
		}
		group, err := client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*id), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
		if err != nil {
			return err
		}
		result, err = group.Update().SetName(name).AddRevision(1).Save(ctx)
		return err
	})
	return result, err
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
		_, err := authz.RunWithSystemBypass(ctx, "api-key-group-budget-lock", func(lockCtx context.Context) (*ent.BudgetAccount, error) {
			return LockBudgetAccount(lockCtx, client, projectID, budgetaccount.OwnerKindGroup, id)
		})
		if err != nil {
			return err
		}
		group, err := client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(id), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
		if err != nil {
			return err
		}
		// Group administration applies to every member, including personal keys
		// hidden from the administrator's ordinary key list.
		memberCtx := authz.WithSystemBypass(ctx, "delete-key-group-membership")
		members, err := client.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.GroupRefEQ(id)).Select(apikey.FieldName).All(memberCtx)
		if err != nil {
			return err
		}
		for _, member := range members {
			if err := s.checkAPIKeyName(ctx, projectID, nil, member.Name, member.ID); err != nil {
				return fmt.Errorf("cannot move group members to ungrouped: %w", err)
			}
		}
		if _, err := client.APIKey.Update().Where(apikey.ProjectIDEQ(projectID), apikey.GroupRefEQ(id)).ClearGroupRef().Save(memberCtx); err != nil {
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
	client := s.entFromContext(ctx)
	query := client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*id), apikeygroup.ProjectIDEQ(projectID))
	// 等待项目锁期间目标组可能已被删除，不能沿用旧事务快照。
	if client.Driver().Dialect() != dialect.SQLite {
		query.Unique(false).Modify(func(q *sql.Selector) { q.ForUpdate() })
	}
	_, err := query.Only(ctx)
	return err
}
