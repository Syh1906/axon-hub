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
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"golang.org/x/sync/singleflight"
)

type ConsumptionService struct {
	*AbstractService
	client *ent.Client
	system *SystemService
	cache  xcache.Cache[*ConsumptionResult]
	flight *singleflight.Group
}

func NewConsumptionService(client *ent.Client, system *SystemService, config xcache.Config) *ConsumptionService {
	return &ConsumptionService{AbstractService: &AbstractService{db: client}, client: client, system: system, cache: xcache.NewFromConfig[*ConsumptionResult](config), flight: &singleflight.Group{}}
}

func (s *ConsumptionService) Leaderboard(ctx context.Context, input ConsumptionFilter) (*ConsumptionResult, error) {
	projectID, err := leaderboardProjectID(ctx, input)
	if err != nil {
		return nil, err
	}
	return s.query(ctx, input, "leaderboard", projectID)
}

func leaderboardProjectID(ctx context.Context, input ConsumptionFilter) (int, error) {
	if err := requireLeaderboardReader(ctx); err != nil {
		return 0, err
	}
	projectID := 0
	if input.ProjectID != nil {
		if input.ProjectID.Type != ent.TypeProject || input.ProjectID.ID <= 0 {
			return 0, fmt.Errorf("invalid project ID")
		}
		projectID = input.ProjectID.ID
	}
	return projectID, nil
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
	projectID, err := usageStatisticsProjectID(ctx, input)
	if err != nil {
		return nil, err
	}
	return s.query(ctx, input, "usage", projectID)
}

func usageStatisticsProjectID(ctx context.Context, input ConsumptionFilter) (int, error) {
	user, ok := contexts.GetUser(ctx)
	if !ok || user == nil {
		return 0, fmt.Errorf("usage statistics requires an authenticated user")
	}
	projectID, ok := contexts.GetProjectID(ctx)
	if !ok || projectID <= 0 {
		return 0, fmt.Errorf("project context is required")
	}
	if err := authz.RequireScope(ctx, scopes.ScopeReadRequests); err != nil {
		return 0, err
	}
	if input.Dimension == ConsumptionDimensionChannel || input.Metric != ConsumptionMetricCost {
		return 0, fmt.Errorf("usage statistics supports user, API key, API key group and model cost summaries")
	}
	if input.ProjectID != nil && (input.ProjectID.Type != ent.TypeProject || input.ProjectID.ID != projectID) {
		return 0, fmt.Errorf("usage statistics project must match the current project")
	}
	return projectID, nil
}

func (s *ConsumptionService) query(ctx context.Context, input ConsumptionFilter, view string, projectID int) (*ConsumptionResult, error) {
	if _, err := consumptionEnum(string(input.Dimension), "USER", "API_KEY", "API_KEY_GROUP", "CHANNEL", "MODEL"); err != nil {
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
			latestKey, err := keysQuery.Select(apikey.FieldID, apikey.FieldUpdatedAt).Order(ent.Desc(apikey.FieldUpdatedAt)).First(schematype.SkipSoftDelete(ctx))
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
		key := fmt.Sprintf("consumption:v7:%x:%s", sha256.Sum256(keyInput), keyRevision)
		if result, err := s.cache.Get(ctx, key); err == nil && result != nil {
			return s.withChannelIdentity(ctx, input, result)
		}
		compute := func(ctx context.Context) (*ConsumptionResult, error) {
			if cached, err := s.cache.Get(ctx, key); err == nil && cached != nil {
				return cached, nil
			}
			rows, err := s.queryGroups(ctx, projectID, input, start, end)
			if err != nil {
				return nil, err
			}
			entries := make([]*ConsumptionEntry, 0)
			modelEntries := make([]*ConsumptionModel, 0)
			if input.Dimension == ConsumptionDimensionModel {
				for _, row := range rows {
					modelEntries = append(modelEntries, &ConsumptionModel{Model: row.Model, Metrics: row.metrics()})
				}
				sortConsumptionModels(modelEntries, input)
			} else {
				entries = consumptionEntries(rows, input)
			}
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
					keys, err := s.client.APIKey.Query().Where(apikey.IDIn(ids...)).
						Select(apikey.FieldID, apikey.FieldName, apikey.FieldUserID, apikey.FieldProjectID, apikey.FieldGroupRef).All(schematype.SkipSoftDelete(ctx))
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
			result := &ConsumptionResult{Entries: entries, ModelEntries: modelEntries, GeneratedAt: now, RangeStart: start, RangeEnd: end, Timezone: location.String(), CurrencyCode: settings.CurrencyCode}
			_ = s.cache.Set(ctx, key, result, store.WithExpiration(time.Minute))
			return result, nil
		}
		result, err := s.sharedQuery(ctx, key, compute)
		if err != nil {
			return nil, err
		}
		return s.withChannelIdentity(ctx, input, result)
	})
}

// 调用方取消仅结束自己的等待；共享计算独立限时，供其余页面复用。
func (s *ConsumptionService) sharedQuery(ctx context.Context, key string, compute func(context.Context) (*ConsumptionResult, error)) (*ConsumptionResult, error) {
	result := s.flight.DoChan(key, func() (value any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Error(ctx, "consumption query panicked", log.Any("panic", recovered))
				err = fmt.Errorf("compute consumption: %v", recovered)
			}
		}()
		sharedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return compute(sharedCtx)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-result:
		if value.Err != nil {
			return nil, value.Err
		}
		return value.Val.(*ConsumptionResult), nil
	}
}
