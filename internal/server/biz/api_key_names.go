package biz

import (
	"context"
	"fmt"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/predicate"
	"github.com/looplj/axonhub/internal/pkg/xerrors"
)

// An absent group is its own name scope, not a wildcard across all groups.
func apiKeyNameGroup(groupID *int) predicate.APIKey {
	if groupID == nil {
		return apikey.GroupRefIsNil()
	}
	return apikey.GroupRefEQ(*groupID)
}

// Call after an authorized write (or project-admin membership check), inside
// the transaction holding lockProjectForAPIKeyName. Hidden personal keys also
// reserve names; return only a conflict, never another owner's credential.
func (s *APIKeyService) checkAPIKeyName(ctx context.Context, projectID int, groupID *int, name string, exceptID int) error {
	checkCtx := authz.WithSystemBypass(ctx, "api-key-name-scope")
	client := s.entFromContext(ctx)
	query := client.APIKey.Query().Where(
		apikey.ProjectIDEQ(projectID), apiKeyNameGroup(groupID), apikey.NameEQ(name), apikey.IDNEQ(exceptID),
	).Select(apikey.FieldID).Limit(1)
	// MySQL 的普通读取可能仍停留在获取项目锁前的事务快照。
	// 写入时使用当前读，让最终查重包含其他事务刚提交的改名或移组。
	if client.Driver().Dialect() != dialect.SQLite {
		query.Unique(false).Modify(func(q *sql.Selector) { q.ForUpdate() })
	}
	conflicts, err := query.All(checkCtx)
	if err != nil {
		return fmt.Errorf("failed to check API key name in target group: %w", err)
	}
	if len(conflicts) > 0 {
		return xerrors.DuplicateNameError("API Key in target group", name)
	}
	return nil
}

// 在项目锁之后读取并锁定密钥；保持项目、密钥、预算的统一锁顺序。
func (s *APIKeyService) currentAPIKey(ctx context.Context, id int) (*ent.APIKey, error) {
	client := s.entFromContext(ctx)
	query := client.APIKey.Query().Where(apikey.IDEQ(id))
	if client.Driver().Dialect() != dialect.SQLite {
		query.Unique(false).Modify(func(q *sql.Selector) { q.ForUpdate() })
	}
	return query.Only(ctx)
}
