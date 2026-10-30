package biz

import (
	"context"
	"fmt"
	"strings"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/apikeyprofiletemplate"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

func validateManagementRows(rows []*KeyManagementRow) error {
	if len(rows) == 0 || len(rows) > 100 {
		return fmt.Errorf("submit between 1 and 100 rows")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row == nil || row.RowID == "" || len(row.RowID) > 64 || seen[row.RowID] {
			return fmt.Errorf("row IDs must be distinct and contain 1 to 64 characters")
		}
		seen[row.RowID] = true
	}
	return nil
}

func (s *APIKeyService) validateManagementRow(ctx context.Context, projectID int, row *KeyManagementRow) error {
	db := s.entFromContext(ctx)
	if row == nil {
		return fmt.Errorf("missing row")
	}
	if row.Name != nil && (strings.TrimSpace(*row.Name) == "" || len([]rune(*row.Name)) > 128) {
		return fmt.Errorf("name must contain 1 to 128 characters")
	}
	if row.Note != nil && len([]rune(*row.Note)) > 1024 {
		return fmt.Errorf("note exceeds 1024 characters")
	}
	if row.GroupRef != nil && *row.GroupRef < 0 {
		return fmt.Errorf("invalid group")
	}
	switch row.Action {
	case "createGroup":
		if row.Name == nil {
			return fmt.Errorf("group name required")
		}
		exists, err := db.APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(projectID), apikeygroup.NameEQ(strings.TrimSpace(*row.Name))).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("group name already exists")
		}
	case "createKey":
		if row.GroupRef == nil {
			return fmt.Errorf("select a group or explicitly choose ungrouped")
		}
		if len(row.Keys) == 0 {
			return fmt.Errorf("at least one key required")
		}
	case "updateGroup", "deleteGroup", "updateKey", "archiveKey":
		if row.TargetID == nil || *row.TargetID <= 0 {
			return fmt.Errorf("target required")
		}
		if row.Action == "updateGroup" || row.Action == "deleteGroup" {
			group, err := db.APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(projectID), apikeygroup.IDEQ(*row.TargetID)).Only(ctx)
			if err != nil {
				return err
			}
			if row.Revision == nil || *row.Revision != group.Revision {
				return fmt.Errorf("group changed; reload before saving")
			}
			if row.Action == "updateGroup" && row.Name != nil {
				exists, err := db.APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(projectID), apikeygroup.IDNEQ(group.ID), apikeygroup.NameEQ(strings.TrimSpace(*row.Name))).Exist(ctx)
				if err != nil {
					return err
				}
				if exists {
					return fmt.Errorf("group name already exists")
				}
			}
			if row.Action == "deleteGroup" {
				members, err := db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.GroupRefEQ(group.ID)).Select(apikey.FieldName).All(authz.WithSystemBypass(ctx, "preview-key-group-removal"))
				if err != nil {
					return err
				}
				for _, member := range members {
					if err := s.checkAPIKeyName(ctx, projectID, nil, member.Name, member.ID); err != nil {
						return fmt.Errorf("cannot move group members to ungrouped: %w", err)
					}
				}
			}
		} else {
			key, err := db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.IDEQ(*row.TargetID)).Only(ctx)
			if err != nil {
				return err
			}
			if row.UpdatedAt == nil || !row.UpdatedAt.Equal(key.UpdatedAt) {
				return fmt.Errorf("key changed; reload before saving")
			}
			if key.Type == apikey.TypeNoauth {
				return fmt.Errorf("system key cannot be managed here")
			}
			actor, _ := contexts.GetUser(ctx)
			if key.Type == apikey.TypePersonal && key.UserID != actor.ID {
				return fmt.Errorf("personal API key can only be modified by its creator")
			}
			name, group := key.Name, key.GroupRef
			if row.Name != nil {
				name = strings.TrimSpace(*row.Name)
			}
			if row.GroupRef != nil {
				group = row.GroupRef
				if *group == 0 {
					group = nil
				}
			}
			if err := s.checkAPIKeyName(ctx, projectID, group, name, key.ID); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported management action")
	}
	if row.GroupRef != nil && *row.GroupRef > 0 {
		if err := s.validateAPIKeyGroup(ctx, projectID, row.GroupRef); err != nil {
			return err
		}
	}
	if row.SetAllowedIPs {
		if err := validateAllowedIPs(row.AllowedIPs); err != nil {
			return err
		}
	}
	if row.TemplateID != nil {
		if _, err := db.APIKeyProfileTemplate.Query().Where(apikeyprofiletemplate.ProjectIDEQ(projectID), apikeyprofiletemplate.IDEQ(*row.TemplateID)).Only(ctx); err != nil {
			return err
		}
	}
	if err := s.validateBudgetPatch(ctx, row.Budget); err != nil {
		return err
	}
	if row.Budget != nil && (row.Action == "updateGroup" || row.Action == "updateKey") {
		kind := "key"
		if row.Action == "updateGroup" {
			kind = "group"
		}
		view, err := s.Budget(ctx, kind, *row.TargetID)
		if err != nil {
			return err
		}
		if row.Budget.Revision == nil || *row.Budget.Revision != view.Revision {
			return fmt.Errorf("budget changed; reload before saving")
		}
	}
	if len(row.Keys) > 100 {
		return fmt.Errorf("at most 100 initial keys per row")
	}
	names := map[string]bool{}
	for _, key := range row.Keys {
		if key == nil {
			return fmt.Errorf("missing key")
		}
		name := strings.TrimSpace(key.Name)
		if name == "" || len([]rune(name)) > 128 {
			return fmt.Errorf("key name must contain 1 to 128 characters")
		}
		if names[name] {
			return fmt.Errorf("duplicate key name in group: %s", name)
		}
		names[name] = true
		if key.Type != "" && key.Type != "personal" && key.Type != "user" && key.Type != "service_account" {
			return fmt.Errorf("invalid key type")
		}
		if err := validateAllowedIPs(key.AllowedIPs); err != nil {
			return err
		}
		if key.Type == "service_account" && len(key.Scopes) == 0 {
			return fmt.Errorf("service account scopes must be explicitly selected")
		}
		if err := s.validateBudgetPatch(ctx, key.Budget); err != nil {
			return err
		}
		if key.TemplateID != nil {
			if _, err := db.APIKeyProfileTemplate.Query().Where(apikeyprofiletemplate.ProjectIDEQ(projectID), apikeyprofiletemplate.IDEQ(*key.TemplateID)).Only(ctx); err != nil {
				return err
			}
		}
		if row.Action == "createKey" {
			group := row.GroupRef
			if group != nil && *group == 0 {
				group = nil
			}
			if err := s.checkAPIKeyName(ctx, projectID, group, name, 0); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *APIKeyService) validateBudgetPatch(ctx context.Context, patch *KeyBudgetPatch) error {
	if patch == nil {
		return nil
	}
	zone := s.SystemService.TimeLocation(ctx).String()
	if patch.Timezone != nil {
		zone = *patch.Timezone
	}
	rules := []budget.Rule{}
	for _, input := range patch.Rules {
		if input == nil {
			return fmt.Errorf("missing budget rule")
		}
		amount, err := budget.ParseAmount(input.Limit)
		if err != nil {
			return err
		}
		rules = append(rules, budget.Rule{Period: input.Period, Mode: input.Mode, Limit: amount, Minute: input.Minute, Timezone: zone})
	}
	return (budget.Policy{Timezone: zone, Rules: rules}).Validate()
}

func (s *APIKeyService) PreviewKeyManagement(ctx context.Context, rows []*KeyManagementRow) ([]*KeyManagementResult, error) {
	projectID, err := s.managementProject(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateManagementRows(rows); err != nil {
		return nil, err
	}
	results := make([]*KeyManagementResult, 0, len(rows))
	names := map[string]bool{}
	for _, row := range rows {
		if row == nil {
			return nil, fmt.Errorf("missing row")
		}
		result := &KeyManagementResult{RowID: row.RowID, KeyIDs: []int{}}
		err := s.validateManagementRow(ctx, projectID, row)
		if err == nil {
			entries := []string{}
			if row.Action == "createGroup" && row.Name != nil {
				entries = append(entries, "group:"+strings.TrimSpace(*row.Name))
			}
			if row.Action == "createKey" {
				for _, key := range row.Keys {
					entries = append(entries, fmt.Sprintf("key:%d:%s", *row.GroupRef, strings.TrimSpace(key.Name)))
				}
			}
			if row.Action == "updateGroup" && row.Name != nil {
				entries = append(entries, "group:"+strings.TrimSpace(*row.Name))
			}
			if row.Action == "updateKey" {
				key, loadErr := s.entFromContext(ctx).APIKey.Get(ctx, *row.TargetID)
				if loadErr != nil {
					return nil, loadErr
				}
				name, groupID := key.Name, 0
				if key.GroupRef != nil {
					groupID = *key.GroupRef
				}
				if row.Name != nil {
					name = strings.TrimSpace(*row.Name)
				}
				if row.GroupRef != nil {
					groupID = *row.GroupRef
				}
				entries = append(entries, fmt.Sprintf("key:%d:%s", groupID, name))
			}
			if row.Action == "deleteGroup" {
				members, loadErr := s.entFromContext(ctx).APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.GroupRefEQ(*row.TargetID)).Select(apikey.FieldName).All(authz.WithSystemBypass(ctx, "preview-key-group-removal"))
				if loadErr != nil {
					return nil, loadErr
				}
				for _, key := range members {
					entries = append(entries, "key:0:"+key.Name)
				}
			}
			for _, entry := range entries {
				if names[entry] {
					err = fmt.Errorf("duplicate name in submitted scope: %s", entry)
					break
				}
			}
			if err == nil {
				for _, entry := range entries {
					names[entry] = true
				}
			}
		}
		result.Success = err == nil
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results, nil
}

// Read fresh membership and group state so a cached credential cannot bypass a
// move or group suspension. This check never changes individual key status.
func (s *APIKeyService) CheckAPIKeyGroupEnabled(ctx context.Context, keyID, projectID int) error {
	_, err := authz.RunWithSystemBypass(ctx, "authenticate-key-group", func(ctx context.Context) (bool, error) {
		db := s.entFromContext(ctx)
		key, err := db.APIKey.Query().Where(apikey.IDEQ(keyID), apikey.ProjectIDEQ(projectID)).Select(apikey.FieldGroupRef).Only(ctx)
		if err != nil {
			return false, err
		}
		if key.GroupRef == nil {
			return true, nil
		}
		group, err := db.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*key.GroupRef), apikeygroup.ProjectIDEQ(projectID)).Only(ctx)
		if err != nil {
			return false, err
		}
		if !group.Enabled {
			return false, fmt.Errorf("API key group disabled: %w", ErrInvalidAPIKey)
		}
		return true, nil
	})
	return err
}
