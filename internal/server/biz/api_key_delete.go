package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
)

// DeletedAPIKeyPrefix identifies non-secret deletion records retained by backups.
const DeletedAPIKeyPrefix = "deleted:"

// apiKeyCacheIdentity keeps deletion markers addressable by the original cache
// key. Incremental refresh can therefore evict stale credentials even if a
// cross-instance invalidation notification was missed. No credential is retained.
func apiKeyCacheIdentity(key *ent.APIKey) string {
	if key.DeletedAt != 0 && strings.HasPrefix(key.Key, DeletedAPIKeyPrefix) {
		_, identity, ok := strings.Cut(strings.TrimPrefix(key.Key, DeletedAPIKeyPrefix), ":")
		if ok {
			return identity
		}
	}
	return buildAPIKeyCacheKey(key.Key)
}

// DeleteAPIKeys irreversibly removes credentials, retaining only their identity
// for historical requests, group reports and in-flight budget settlement.
// The complete selection commits together; authorization or storage failures
// leave every selected key unchanged.
func (s *APIKeyService) DeleteAPIKeys(ctx context.Context, ids []int) error {
	projectID, err := s.managementProject(ctx)
	if err != nil {
		return err
	}
	ids = lo.Uniq(ids)
	if len(ids) == 0 {
		return fmt.Errorf("select at least one API key")
	}
	slices.Sort(ids)
	var originals []string
	err = s.RunInTransaction(ctx, func(ctx context.Context) error {
		if err := s.lockProjectForAPIKeyName(ctx, projectID); err != nil {
			return err
		}
		db := s.entFromContext(ctx)
		// Keep the same project -> key-budget lock order as membership edits.
		// Admission also takes the key-budget lock before checking the live key.
		for _, id := range ids {
			_, err := authz.RunWithSystemBypass(ctx, "delete-api-key-budget-lock", func(lockCtx context.Context) (*ent.BudgetAccount, error) {
				return LockBudgetAccount(lockCtx, db, projectID, budgetaccount.OwnerKindKey, id)
			})
			if err != nil {
				return err
			}
		}
		query := db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.IDIn(ids...)).Order(ent.Asc(apikey.FieldID))
		if db.Driver().Dialect() != dialect.SQLite {
			query.Unique(false).Modify(func(q *sql.Selector) { q.ForUpdate() })
		}
		keys, err := query.All(ctx)
		if err != nil {
			return err
		}
		if len(keys) != len(ids) {
			return fmt.Errorf("selected API keys no longer exist or are not accessible; reload before deleting")
		}
		actor, _ := contexts.GetUser(ctx)
		for _, key := range keys {
			if key.Type == apikey.TypeNoauth {
				return fmt.Errorf("system API keys cannot be deleted")
			}
			if key.Type == apikey.TypePersonal && key.UserID != actor.ID {
				return fmt.Errorf("personal API keys can only be deleted by their creator")
			}
		}
		for _, key := range keys {
			marker := fmt.Sprintf("%s%d:%s", DeletedAPIKeyPrefix, key.ID, buildAPIKeyCacheKey(key.Key))
			// Retain historical identity/configuration, but destroy the secret.
			// Deleted profiles cannot receive later template publications.
			count, err := db.APIKey.Update().Where(apikey.IDEQ(key.ID), apikey.ProjectIDEQ(projectID), apikey.DeletedAtEQ(0)).
				SetKey(marker).SetStatus(apikey.StatusDisabled).SetDeletedAt(int(time.Now().Unix())).
				SetScopes([]string{}).SetAllowedIps([]string{}).Save(ctx)
			if err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("API key changed during deletion; reload before deleting")
			}
			originals = append(originals, key.Key)
		}
		runAfterCommit(ctx, func(committedCtx context.Context) {
			for _, key := range originals {
				s.APIKeyCache.Invalidate(buildAPIKeyCacheKey(key))
			}
			s.invalidateAPIKeyCaches(committedCtx, originals...)
		})
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
