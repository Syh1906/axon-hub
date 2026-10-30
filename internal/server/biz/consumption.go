package biz

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
)

type ConsumptionService struct {
	*AbstractService
	client *ent.Client
	system *SystemService
	cache  xcache.Cache[*ConsumptionResult]
}

func NewConsumptionService(client *ent.Client, system *SystemService, config xcache.Config) *ConsumptionService {
	return &ConsumptionService{AbstractService: &AbstractService{db: client}, client: client, system: system, cache: xcache.NewFromConfig[*ConsumptionResult](config)}
}

func (s *ConsumptionService) Leaderboard(ctx context.Context, input ConsumptionFilter) (*ConsumptionResult, error) {
	if err := requireLeaderboardReader(ctx); err != nil {
		return nil, err
	}
	projectID := 0
	if input.ProjectID != nil {
		if input.ProjectID.Type != ent.TypeProject || input.ProjectID.ID <= 0 {
			return nil, fmt.Errorf("invalid project ID")
		}
		projectID = input.ProjectID.ID
	}
	return s.query(ctx, input, "leaderboard", projectID)
}

func requireLeaderboardReader(ctx context.Context) error {
	user, ok := contexts.GetUser(ctx)
	if !ok || user == nil || !scopes.HasSystemScope(user, scopes.ScopeReadLeaderboard) {
		return fmt.Errorf("permission denied: read_leaderboard is required")
	}
	return nil
}

func (s *ConsumptionService) Projects(ctx context.Context) ([]*ConsumptionProject, error) {
	if err := requireLeaderboardReader(ctx); err != nil {
		return nil, err
	}
	return authz.RunWithSystemBypass(ctx, "consumption-project-options", func(ctx context.Context) ([]*ConsumptionProject, error) {
		projects, err := s.client.Project.Query().Select("id", "name").All(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]*ConsumptionProject, 0, len(projects))
		for _, project := range projects {
			result = append(result, &ConsumptionProject{ID: objects.GUID{Type: ent.TypeProject, ID: project.ID}, Name: project.Name})
		}
		return result, nil
	})
}

// UsageStatistics reports only the current project for authorized request readers.
func (s *ConsumptionService) UsageStatistics(ctx context.Context, input ConsumptionFilter) (*ConsumptionResult, error) {
	user, ok := contexts.GetUser(ctx)
	if !ok || user == nil {
		return nil, fmt.Errorf("usage statistics requires an authenticated user")
	}
	projectID, ok := contexts.GetProjectID(ctx)
	if !ok || projectID <= 0 {
		return nil, fmt.Errorf("project context is required")
	}
	if err := authz.RequireScope(ctx, scopes.ScopeReadRequests); err != nil {
		return nil, err
	}
	if input.Dimension == ConsumptionDimensionChannel || input.Metric != ConsumptionMetricCost {
		return nil, fmt.Errorf("usage statistics supports user, API key and API key group cost summaries")
	}
	if input.ProjectID != nil && (input.ProjectID.Type != ent.TypeProject || input.ProjectID.ID != projectID) {
		return nil, fmt.Errorf("usage statistics project must match the current project")
	}
	return s.query(ctx, input, "usage", projectID)
}

func (s *ConsumptionService) query(ctx context.Context, input ConsumptionFilter, view string, projectID int) (*ConsumptionResult, error) {
	if _, err := consumptionEnum(string(input.Dimension), "USER", "API_KEY", "API_KEY_GROUP", "CHANNEL"); err != nil {
		return nil, err
	}
	if _, err := consumptionEnum(string(input.Metric), "COST", "CACHE"); err != nil {
		return nil, err
	}
	if input.Search != nil {
		search := strings.TrimSpace(*input.Search)
		input.Search = &search
	}

	// Authorization is checked before the cache and this narrowly scoped read.
	// Usage is project-scoped. The admin leaderboard ignores ambient project context.
	return authz.RunWithSystemBypass(ctx, "consumption-project-query", func(ctx context.Context) (*ConsumptionResult, error) {
		settings, err := s.system.GeneralSettings(ctx)
		if err != nil {
			return nil, err
		}
		location := s.system.TimeLocation(ctx)
		now := time.Now()
		start, end, err := consumptionRange(input, now, location)
		if err != nil {
			return nil, err
		}
		keyRevision := ""
		if input.Dimension == ConsumptionDimensionAPIKey || input.Dimension == ConsumptionDimensionAPIKeyGroup {
			keysQuery := s.client.APIKey.Query()
			groupsQuery := s.client.APIKeyGroup.Query()
			if projectID > 0 {
				keysQuery.Where(apikey.ProjectIDEQ(projectID))
				groupsQuery.Where(apikeygroup.ProjectIDEQ(projectID))
			}
			latestKey, err := keysQuery.Select(apikey.FieldID, apikey.FieldUpdatedAt).Order(ent.Desc(apikey.FieldUpdatedAt)).First(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return nil, err
			}
			latestGroup, err := groupsQuery.Select(apikeygroup.FieldID, apikeygroup.FieldUpdatedAt).Order(ent.Desc(apikeygroup.FieldUpdatedAt)).First(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return nil, err
			}
			if latestKey != nil {
				keyRevision += latestKey.UpdatedAt.Format(time.RFC3339Nano)
			}
			if latestGroup != nil {
				keyRevision += latestGroup.UpdatedAt.Format(time.RFC3339Nano)
			}
		}
		keyInput, err := json.Marshal(struct {
			Project                       int
			View, Day, Timezone, Currency string
			Filter                        ConsumptionFilter
		}{projectID, view, now.In(location).Format("2006-01-02"), location.String(), settings.CurrencyCode, input})
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("consumption:v2:%x:%s", sha256.Sum256(keyInput), keyRevision)
		if result, err := s.cache.Get(ctx, key); err == nil && result != nil {
			return result, nil
		}
		rows, err := s.queryGroups(ctx, projectID, input, start, end)
		if err != nil {
			return nil, err
		}
		entries := consumptionEntries(rows, input)
		if input.Dimension == ConsumptionDimensionAPIKeyGroup && projectID == 0 && len(entries) > 0 {
			ids := make([]int, 0, len(entries))
			for _, entry := range entries {
				ids = append(ids, entry.ID.ID)
			}
			groups, err := s.client.APIKeyGroup.Query().Where(apikeygroup.IDIn(ids...)).
				WithProject(func(q *ent.ProjectQuery) { q.Select(project.FieldName) }).All(ctx)
			if err != nil {
				return nil, err
			}
			labels := make(map[int]string, len(groups))
			for _, group := range groups {
				labels[group.ID] = group.Name + " · " + group.Edges.Project.Name
			}
			for _, entry := range entries {
				entry.Name = labels[entry.ID.ID]
			}
		}
		if input.Dimension == ConsumptionDimensionAPIKey {
			ids := make([]int, 0, len(entries))
			for _, entry := range entries {
				ids = append(ids, entry.ID.ID)
			}
			if len(ids) > 0 {
				keys, err := s.client.APIKey.Query().Where(apikey.IDIn(ids...)).All(ctx)
				if err != nil {
					return nil, err
				}
				labels, err := APIKeyLabels(ctx, s.client, keys)
				if err != nil {
					return nil, err
				}
				for _, entry := range entries {
					entry.Name = labels[entry.ID.ID]
				}
			}
		}
		if input.Dimension == ConsumptionDimensionChannel {
			if err := s.attachCacheCoefficients(ctx, projectID, start, end, entries); err != nil {
				return nil, err
			}
		}
		sortConsumptionEntries(entries, input)
		result := &ConsumptionResult{Entries: entries, GeneratedAt: now, RangeStart: start, RangeEnd: end, Timezone: location.String(), CurrencyCode: settings.CurrencyCode}
		_ = s.cache.Set(ctx, key, result, store.WithExpiration(time.Minute))
		return result, nil
	})
}
