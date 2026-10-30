package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/apikeyoperation"
	"github.com/looplj/axonhub/internal/scopes"
)

func managementDigest(value any) string {
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s *APIKeyService) managementProject(ctx context.Context) (int, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeWriteAPIKeys); err != nil {
		return 0, err
	}
	return s.groupProject(ctx, true)
}

// Each row is an independent transaction. A receipt commits with the objects;
// retrying a lost response never creates a second set of credentials.
func (s *APIKeyService) ApplyKeyManagement(ctx context.Context, templates *APIKeyProfileTemplateService, operationID string, rows []*KeyManagementRow) ([]*KeyManagementResult, error) {
	projectID, err := s.managementProject(ctx)
	if err != nil {
		return nil, err
	}
	if operationID == "" || len(operationID) > 64 {
		return nil, fmt.Errorf("invalid operation ID")
	}
	if err := validateManagementRows(rows); err != nil {
		return nil, err
	}
	actor, _ := contexts.GetUser(ctx)
	results := make([]*KeyManagementResult, 0, len(rows))
	for _, row := range rows {
		result := &KeyManagementResult{RowID: row.RowID, KeyIDs: []int{}}
		err := s.RunInTransaction(ctx, func(txCtx context.Context) error {
			db := s.entFromContext(txCtx)
			receiptCtx := authz.WithSystemBypass(txCtx, "key-management-receipt")
			if err := s.lockProjectForAPIKeyName(txCtx, projectID); err != nil {
				return err
			}
			digest := managementDigest(row)
			receipt, err := db.APIKeyOperation.Query().Where(apikeyoperation.ProjectIDEQ(projectID), apikeyoperation.ActorIDEQ(actor.ID), apikeyoperation.OperationIDEQ(operationID), apikeyoperation.RowIDEQ(row.RowID)).Only(receiptCtx)
			if err == nil {
				if receipt.Digest != digest {
					return fmt.Errorf("operation row already committed with different input")
				}
				result.GroupID, result.KeyIDs = receipt.GroupID, receipt.KeyIds
				return nil
			}
			if !ent.IsNotFound(err) {
				return err
			}
			// 名称操作共享项目锁；额外锁定现有密钥行，使版本核对与
			// 未使用项目锁的单条启停操作也具有明确的先后顺序。
			if (row.Action == "updateKey" || row.Action == "archiveKey") && row.TargetID != nil && db.Driver().Dialect() != dialect.SQLite {
				_, err := db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.IDEQ(*row.TargetID)).Unique(false).Modify(func(q *sql.Selector) { q.ForUpdate() }).Only(txCtx)
				if err != nil {
					return err
				}
			}
			if err := s.validateManagementRow(txCtx, projectID, row); err != nil {
				return err
			}
			if err := s.applyManagementRow(txCtx, templates, projectID, operationID, row, result); err != nil {
				return err
			}
			return db.APIKeyOperation.Create().SetProjectID(projectID).SetActorID(actor.ID).SetOperationID(operationID).SetRowID(row.RowID).SetDigest(digest).SetGroupID(result.GroupID).SetKeyIds(result.KeyIDs).Exec(receiptCtx)
		})
		result.Success = err == nil
		if err != nil {
			result.Error = err.Error()
			result.KeyIDs = []int{}
			result.GroupID = 0
		}
		results = append(results, result)
		if err == nil && len(result.KeyIDs) > 0 {
			keys, loadErr := s.entFromContext(ctx).APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.IDIn(result.KeyIDs...)).All(ctx)
			if loadErr == nil {
				for _, key := range keys {
					s.invalidateAPIKeyCaches(ctx, key.Key)
				}
			}
		}
	}
	return results, nil
}

func (s *APIKeyService) KeyManagementResults(ctx context.Context, operationID string) ([]*KeyManagementResult, error) {
	projectID, err := s.managementProject(ctx)
	if err != nil {
		return nil, err
	}
	actor, _ := contexts.GetUser(ctx)
	receipts, err := s.entFromContext(ctx).APIKeyOperation.Query().Where(apikeyoperation.ProjectIDEQ(projectID), apikeyoperation.ActorIDEQ(actor.ID), apikeyoperation.OperationIDEQ(operationID)).All(authz.WithSystemBypass(ctx, "key-management-receipt"))
	if err != nil {
		return nil, err
	}
	results := make([]*KeyManagementResult, 0, len(receipts))
	for _, row := range receipts {
		results = append(results, &KeyManagementResult{RowID: row.RowID, Success: true, GroupID: row.GroupID, KeyIDs: row.KeyIds})
	}
	return results, nil
}

func (s *APIKeyService) applyManagementRow(ctx context.Context, templates *APIKeyProfileTemplateService, projectID int, operationID string, row *KeyManagementRow, result *KeyManagementResult) error {
	db := s.entFromContext(ctx)
	var normalizedName *string
	if row.Name != nil {
		name := strings.TrimSpace(*row.Name)
		normalizedName = &name
	}
	actor, _ := contexts.GetUser(ctx)
	operation := managementDigest([]any{projectID, actor.ID, operationID, row.RowID})
	switch row.Action {
	case "createGroup":
		group, err := s.SaveAPIKeyGroup(ctx, nil, *row.Name)
		if err != nil {
			return err
		}
		group, err = group.Update().SetNillableNote(row.Note).SetNillableEnabled(row.Enabled).Save(ctx)
		if err != nil {
			return err
		}
		result.GroupID = group.ID
		if err := s.applyBudgetPatch(ctx, "group", group.ID, row.Budget, operation); err != nil {
			return err
		}
		for i, input := range row.Keys {
			key, err := s.createManagedKey(ctx, templates, projectID, &group.ID, input, managementDigest([]any{operation, i}))
			if err != nil {
				return err
			}
			result.KeyIDs = append(result.KeyIDs, key.ID)
		}
	case "createKey":
		groupID := row.GroupRef
		if groupID != nil && *groupID == 0 {
			groupID = nil
		}
		for i, input := range row.Keys {
			key, err := s.createManagedKey(ctx, templates, projectID, groupID, input, managementDigest([]any{operation, i}))
			if err != nil {
				return err
			}
			result.KeyIDs = append(result.KeyIDs, key.ID)
		}
		if groupID != nil {
			result.GroupID = *groupID
		}
	case "updateGroup":
		group, err := db.APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(projectID), apikeygroup.IDEQ(*row.TargetID)).Only(ctx)
		if err != nil {
			return err
		}
		if row.Revision == nil || group.Revision != *row.Revision {
			return fmt.Errorf("group changed; reload before saving")
		}
		_, err = group.Update().SetNillableName(normalizedName).SetNillableNote(row.Note).SetNillableEnabled(row.Enabled).AddRevision(1).Save(ctx)
		if err != nil {
			return err
		}
		result.GroupID = group.ID
		return s.applyBudgetPatch(ctx, "group", group.ID, row.Budget, operation)
	case "deleteGroup":
		return s.DeleteAPIKeyGroup(ctx, *row.TargetID)
	case "updateKey", "archiveKey":
		key, err := db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.IDEQ(*row.TargetID)).Only(ctx)
		if err != nil {
			return err
		}
		input := ent.UpdateAPIKeyInput{Name: normalizedName}
		if row.GroupRef != nil {
			if *row.GroupRef == 0 {
				input.ClearGroupRef = true
			} else {
				input.GroupRef = row.GroupRef
			}
		}
		if row.SetAllowedIPs {
			input.ClearAllowedIps = true
			input.AllowedIps = row.AllowedIPs
		}
		key, err = s.UpdateAPIKey(ctx, key.ID, input)
		if err != nil {
			return err
		}
		if row.Enabled != nil {
			status := apikey.StatusDisabled
			if *row.Enabled {
				status = apikey.StatusEnabled
			}
			if _, err = s.UpdateAPIKeyStatus(ctx, key.ID, status); err != nil {
				return err
			}
		}
		if row.Action == "archiveKey" {
			if _, err = s.UpdateAPIKeyStatus(ctx, key.ID, apikey.StatusArchived); err != nil {
				return err
			}
		}
		if row.TemplateID != nil {
			if _, err = s.loadManagementTemplate(ctx, templates, *row.TemplateID, key.ID); err != nil {
				return err
			}
		}
		if err = s.applyBudgetPatch(ctx, "key", key.ID, row.Budget, operation); err != nil {
			return err
		}
		result.KeyIDs = []int{key.ID}
	default:
		return fmt.Errorf("unsupported management action")
	}
	return nil
}

func (s *APIKeyService) createManagedKey(ctx context.Context, templates *APIKeyProfileTemplateService, projectID int, groupID *int, input *ManagedKeyInput, operationID string) (*ent.APIKey, error) {
	typ := input.Type
	if typ == "" {
		typ = apikey.TypePersonal
	}
	key, err := s.CreateAPIKey(ctx, ent.CreateAPIKeyInput{Name: strings.TrimSpace(input.Name), Type: &typ, ProjectID: projectID, GroupRef: groupID, Scopes: input.Scopes, AllowedIps: input.AllowedIPs})
	if err != nil {
		return nil, err
	}
	if input.TemplateID != nil {
		key, err = s.loadManagementTemplate(ctx, templates, *input.TemplateID, key.ID)
		if err != nil {
			return nil, err
		}
	}
	if err := s.applyBudgetPatch(ctx, "key", key.ID, input.Budget, operationID); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *APIKeyService) loadManagementTemplate(ctx context.Context, templates *APIKeyProfileTemplateService, templateID, keyID int) (*ent.APIKey, error) {
	key, err := templates.LoadTemplate(ctx, templateID, keyID)
	if err != nil {
		return nil, err
	}
	key.Profiles.ActiveProfile = key.Profiles.Profiles[len(key.Profiles.Profiles)-1].Name
	return s.UpdateAPIKeyProfiles(ctx, key.ID, *key.Profiles)
}

func (s *APIKeyService) applyBudgetPatch(ctx context.Context, kind string, ownerID int, patch *KeyBudgetPatch, operationID string) error {
	if patch == nil {
		return nil
	}
	view, err := s.Budget(ctx, kind, ownerID)
	if err != nil {
		return err
	}
	if patch.Revision != nil && view.Revision != *patch.Revision {
		return fmt.Errorf("budget changed; reload before saving")
	}
	input := BudgetSettingsInput{Timezone: view.Timezone, ExpiresAt: view.ExpiresAt, Revision: view.Revision, OperationID: operationID}
	if patch.Timezone != nil {
		input.Timezone = *patch.Timezone
	}
	if patch.SetExpiry {
		input.ExpiresAt = patch.ExpiresAt
	}
	for _, window := range view.Windows {
		rule := &BudgetRuleInput{Period: window.Period, Mode: window.Mode, Limit: window.Limit, Minute: window.Minute}
		if window.PendingRule != nil {
			copy := *window.PendingRule
			rule = &copy
		}
		for _, change := range patch.Rules {
			if change.Period == rule.Period {
				copy := *change
				rule = &copy
			}
		}
		input.Rules = append(input.Rules, rule)
	}
	_, err = s.SaveBudget(ctx, kind, ownerID, input)
	return err
}
