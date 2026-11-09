package biz

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/budgetaccount"
)

// KeyBudgetSummaries reads the same balance, policy and expiry as Budget in
// batches. Detailed rolling-release timestamps remain on the single-owner
// endpoint used by the budget dialog. Authorization precedes internal ledger reads.
func (s *APIKeyService) KeyBudgetSummaries(ctx context.Context, ownerIDs []int, includeGroups bool) ([]*BudgetView, error) {
	projectID, err := s.groupProject(ctx, false)
	if err != nil {
		return nil, err
	}
	if len(ownerIDs) > 100 {
		return nil, fmt.Errorf("at most 100 API keys per budget summary query")
	}
	ids := lo.Uniq(ownerIDs)
	if len(ids) == 0 {
		return []*BudgetView{}, nil
	}
	client := s.entFromContext(ctx)
	if _, transactional := client.Driver().(dialect.Tx); transactional {
		return s.keyBudgetSummariesSnapshot(ctx, projectID, ids, includeGroups)
	}
	options := &stdsql.TxOptions{ReadOnly: true, Isolation: stdsql.LevelRepeatableRead}
	if client.Driver().Dialect() == dialect.SQLite {
		options = &stdsql.TxOptions{}
	}
	tx, err := client.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return s.keyBudgetSummariesSnapshot(ent.NewContext(ctx, tx.Client()), projectID, ids, includeGroups)
}

// Accounts, rolling postings and shared groups must describe one database
// snapshot. A concurrent settlement moves reserved amounts into postings.
func (s *APIKeyService) keyBudgetSummariesSnapshot(ctx context.Context, projectID int, ids []int, includeGroups bool) ([]*BudgetView, error) {
	result := make([]*BudgetView, 0, len(ids))
	visible, err := s.entFromContext(ctx).APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.IDIn(ids...)).
		Select(apikey.FieldID, apikey.FieldGroupRef).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(visible) != len(ids) {
		return nil, &ent.NotFoundError{}
	}
	now := time.Now().UTC()
	views, err := s.budgetSummaryViews(ctx, projectID, budgetaccount.OwnerKindKey, ids, now)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		result = append(result, views[id])
	}
	if includeGroups {
		groups := make([]int, 0, len(visible))
		for _, key := range visible {
			if key.GroupRef != nil {
				groups = append(groups, *key.GroupRef)
			}
		}
		groups = lo.Uniq(groups)
		groupViews, err := s.groupBudgetViews(ctx, projectID, groups, now)
		if err != nil {
			return nil, err
		}
		for _, id := range groups {
			result = append(result, groupViews[id])
		}
	}
	return result, nil
}
