package biz

import (
	"context"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/pkg/budget"
)

func managementPage(offset, limit int) error {
	if offset < 0 || limit < 1 || limit > 100 {
		return fmt.Errorf("invalid page: limit must be between 1 and 100")
	}
	return nil
}

func (s *APIKeyService) ManagedGroupKeys(ctx context.Context, groupRef, offset, limit int) (*ManagedKeyPage, error) {
	projectID, err := s.groupProject(ctx, false)
	if err != nil {
		return nil, err
	}
	if err := managementPage(offset, limit); err != nil {
		return nil, err
	}
	if groupRef < 0 {
		return nil, fmt.Errorf("invalid group")
	}
	var group *int
	if groupRef > 0 {
		group = &groupRef
	}
	q := s.entFromContext(ctx).APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apiKeyNameGroup(group), apikey.StatusNEQ(apikey.StatusArchived), apikey.TypeNEQ(apikey.TypeNoauth))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := q.Order(ent.Desc(apikey.FieldCreatedAt), ent.Desc(apikey.FieldID)).Offset(offset).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	return &ManagedKeyPage{Items: keys, Total: total}, nil
}

func (s *APIKeyService) ManagedKeyGroups(ctx context.Context, search, status string, offset, limit int, descending bool) (*ManagedKeyGroups, error) {
	projectID, err := s.groupProject(ctx, false)
	if err != nil {
		return nil, err
	}
	if err := managementPage(offset, limit); err != nil {
		return nil, err
	}
	db := s.entFromContext(ctx)
	q := db.APIKeyGroup.Query().Where(apikeygroup.ProjectIDEQ(projectID))
	if search != "" {
		keys, err := db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.NameContainsFold(search), apikey.GroupRefNotNil()).Select(apikey.FieldGroupRef).All(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]int, 0, len(keys))
		for _, key := range keys {
			ids = append(ids, *key.GroupRef)
		}
		q.Where(apikeygroup.Or(apikeygroup.NameContainsFold(search), apikeygroup.NoteContainsFold(search), apikeygroup.IDIn(ids...)))
	}
	switch status {
	case "disabled":
		q.Where(apikeygroup.EnabledEQ(false))
	case "enabled":
		q.Where(apikeygroup.EnabledEQ(true))
	case "all", "expired", "exhausted":
	default:
		return nil, fmt.Errorf("invalid group status")
	}
	if descending {
		q.Order(ent.Desc(apikeygroup.FieldCreatedAt), ent.Desc(apikeygroup.FieldID))
	} else {
		q.Order(ent.Asc(apikeygroup.FieldCreatedAt), ent.Asc(apikeygroup.FieldID))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	derivedFilter := status == "expired" || status == "exhausted" || status == "enabled"
	if !derivedFilter {
		q.Offset(offset).Limit(limit)
	}
	groups, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	var counts []struct {
		GroupRef int `json:"group_ref"`
		Count    int `json:"count"`
	}
	if len(ids) > 0 {
		err = db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.GroupRefIn(ids...), apikey.StatusNEQ(apikey.StatusArchived), apikey.TypeNEQ(apikey.TypeNoauth)).GroupBy(apikey.FieldGroupRef).Aggregate(ent.Count()).Scan(ctx, &counts)
		if err != nil {
			return nil, err
		}
	}
	countByID := map[int]int{}
	for _, row := range counts {
		countByID[row.GroupRef] = row.Count
	}
	ungrouped, err := db.APIKey.Query().Where(apikey.ProjectIDEQ(projectID), apikey.GroupRefIsNil(), apikey.StatusNEQ(apikey.StatusArchived), apikey.TypeNEQ(apikey.TypeNoauth)).Count(ctx)
	if err != nil {
		return nil, err
	}
	views, err := s.groupBudgetViews(ctx, projectID, ids, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	items := []*ManagedKeyGroup{}
	for _, group := range groups {
		view := views[group.ID]
		exhausted := false
		for _, window := range view.Windows {
			limit, _ := budget.ParseAmount(window.Limit)
			available, _ := budget.ParseAmount(window.Available)
			if limit > 0 && available == 0 {
				exhausted = true
			}
		}
		if status == "expired" && !view.Expired {
			continue
		}
		if status == "enabled" && (view.Expired || exhausted) {
			continue
		}
		if status == "exhausted" && !exhausted {
			continue
		}
		items = append(items, &ManagedKeyGroup{ID: group.ID, Name: group.Name, Note: group.Note, Enabled: group.Enabled, Revision: group.Revision, CreatedAt: group.CreatedAt, KeyCount: countByID[group.ID], Budget: view})
	}
	if derivedFilter {
		total = len(items)
		if offset >= total {
			items = []*ManagedKeyGroup{}
		} else {
			end := offset + limit
			if end > total {
				end = total
			}
			items = items[offset:end]
		}
	}
	return &ManagedKeyGroups{Items: items, Total: total, UngroupedCount: ungrouped}, nil
}
