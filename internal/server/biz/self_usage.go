package biz

import (
	"context"
	"fmt"
	"sort"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/apikeygroup"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/usagelog"
)

const selfUsageMaxRangeDays = 90

type SelfUsageService struct {
	client *ent.Client
	system *SystemService
}

func NewSelfUsageService(client *ent.Client, system *SystemService) *SelfUsageService {
	return &SelfUsageService{client: client, system: system}
}

type SelfUsageMeta struct {
	APIKeyName   string
	APIKeyType   string
	GroupName    *string
	CurrencyCode string
	Timezone     string
	MaxRangeDays int
}

type SelfUsageSummary struct {
	SuccessRequests  int
	FailedRequests   int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
	TotalTokens      int
	Cost             *float64
	UsageRecords     int
	UnpricedRecords  int
}

type SelfUsageModelStat struct {
	Model string
	Usage SelfUsageSummary
}

type SelfUsageDailyStat struct {
	Date  string
	Usage SelfUsageSummary
}

type SelfUsageStats struct {
	ScopeID    string
	APIKeyName string
	GroupName  *string
	Start      string
	End        string
	Totals     SelfUsageSummary
	Models     []SelfUsageModelStat
	Daily      []SelfUsageDailyStat
}

type SelfUsageRequest struct {
	APIKeyName          string
	ID                  int
	CreatedAt           time.Time
	Model               string
	Status              string
	RequestStatus       string
	Stream              bool
	LatencyMs           *int
	FirstTokenLatencyMs *int
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheWriteTokens    int
	ReasoningTokens     int
	TotalTokens         int
	Cost                *float64
	CostItems           []SelfUsageCostItem
	CostMultiplier      *float64
	CostMultiplierMixed bool
	UsageRecords        int
	UnpricedRecords     int
}

type SelfUsageRequestPage struct {
	ScopeID    string
	APIKeyName string
	GroupName  *string
	Items      []SelfUsageRequest
	Page       int
	PageSize   int
	Total      int
}

// The request predicates are applied to every request query and again to the
// request side of every usage join. Usage-log key/project fields are not used
// as a substitute: the request is the authoritative owner and time anchor.
type selfUsageScope struct {
	id         string
	apiKeyName string
	groupName  *string
	projectID  int
	keyIDs     []int
	start, end time.Time
}

func selfUsageKey(ctx context.Context) (*ent.APIKey, error) {
	key, ok := contexts.GetAPIKey(ctx)
	if !ok || key == nil || (key.Type != "user" && key.Type != "personal") {
		return nil, fmt.Errorf("self usage requires an authenticated user or personal API key")
	}
	return key, nil
}

func (s *SelfUsageService) Meta(ctx context.Context) (*SelfUsageMeta, error) {
	key, err := selfUsageKey(ctx)
	if err != nil {
		return nil, err
	}
	// Settings are read in their own narrow bypass, with only public display
	// fields copied to the return value.
	return authz.RunWithSystemBypass(ctx, "self-usage-settings", func(ctx context.Context) (*SelfUsageMeta, error) {
		settings, err := s.system.GeneralSettings(ctx)
		if err != nil {
			return nil, err
		}
		var groupName *string
		current, err := s.client.APIKey.Get(ctx, key.ID)
		if err != nil {
			return nil, err
		}
		if current.GroupRef != nil {
			group, err := s.client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*current.GroupRef), apikeygroup.ProjectIDEQ(key.ProjectID)).Only(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return nil, err
			}
			if group != nil {
				groupName = &group.Name
			}
		}
		labels, err := APIKeyLabels(ctx, s.client, []*ent.APIKey{current})
		if err != nil {
			return nil, err
		}
		return &SelfUsageMeta{GroupName: groupName,
			APIKeyName: labels[current.ID], APIKeyType: string(key.Type),
			CurrencyCode: settings.CurrencyCode, Timezone: s.system.TimeLocation(ctx).String(),
			MaxRangeDays: selfUsageMaxRangeDays,
		}, nil
	})
}

func (s *SelfUsageService) rangeScope(ctx context.Context, start, end string, group ...bool) (selfUsageScope, time.Time, error) {
	key, err := selfUsageKey(ctx)
	if err != nil {
		return selfUsageScope{}, time.Time{}, err
	}
	loc, err := authz.RunWithSystemBypass(ctx, "self-usage-settings", func(ctx context.Context) (*time.Location, error) {
		return s.system.TimeLocation(ctx), nil
	})
	if err != nil {
		return selfUsageScope{}, time.Time{}, fmt.Errorf("read self usage timezone: %w", err)
	}
	first, err := time.ParseInLocation("2006-01-02", start, loc)
	if err != nil || first.Format("2006-01-02") != start {
		return selfUsageScope{}, time.Time{}, fmt.Errorf("invalid_range: start must be YYYY-MM-DD")
	}
	last, err := time.ParseInLocation("2006-01-02", end, loc)
	if err != nil || last.Format("2006-01-02") != end {
		return selfUsageScope{}, time.Time{}, fmt.Errorf("invalid_range: end must be YYYY-MM-DD")
	}
	if last.Before(first) || last.Sub(first).Hours()/24 > selfUsageMaxRangeDays+2 {
		return selfUsageScope{}, time.Time{}, fmt.Errorf("invalid_range: range must contain 1 to %d days", selfUsageMaxRangeDays)
	}
	// Count calendar dates rather than 24-hour periods: a DST transition can
	// make a local day 23 or 25 hours long.
	days := 0
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		days++
		if days > selfUsageMaxRangeDays {
			return selfUsageScope{}, time.Time{}, fmt.Errorf("invalid_range: range must contain 1 to %d days", selfUsageMaxRangeDays)
		}
	}
	scope, err := authz.RunWithSystemBypass(ctx, "self-usage-group-scope", func(ctx context.Context) (selfUsageScope, error) {
		result := selfUsageScope{id: fmt.Sprintf("key:%d", key.ID), keyIDs: []int{key.ID}, projectID: key.ProjectID, start: first.UTC(), end: last.AddDate(0, 0, 1).UTC()}
		current, err := s.client.APIKey.Get(ctx, key.ID)
		if err != nil {
			return result, err
		}
		if current.ProjectID != key.ProjectID || current.Status != apikey.StatusEnabled {
			return result, fmt.Errorf("self usage is unavailable")
		}
		if current.GroupRef != nil {
			row, err := s.client.APIKeyGroup.Query().Where(apikeygroup.IDEQ(*current.GroupRef), apikeygroup.ProjectIDEQ(key.ProjectID)).Only(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return result, err
			}
			if row != nil {
				result.groupName = &row.Name
			}
		}
		if result.groupName != nil {
			result.apiKeyName = current.Name + " · " + *result.groupName
		} else {
			labels, err := APIKeyLabels(ctx, s.client, []*ent.APIKey{current})
			if err != nil {
				return result, err
			}
			result.apiKeyName = labels[current.ID]
		}
		if len(group) > 0 && group[0] {
			if result.groupName == nil {
				return result, fmt.Errorf("group usage is unavailable")
			}
			result.id = fmt.Sprintf("group:%d", *current.GroupRef)
			result.keyIDs, err = s.client.APIKey.Query().Where(apikey.ProjectIDEQ(key.ProjectID), apikey.GroupRefEQ(*current.GroupRef)).IDs(ctx)
		}
		return result, err
	})
	if err != nil {
		return selfUsageScope{}, time.Time{}, err
	}
	return scope, first, nil
}

func (scope selfUsageScope) requestQuery(client *ent.Client) *ent.RequestQuery {
	return client.Request.Query().Where(
		request.APIKeyIDIn(scope.keyIDs...), request.ProjectIDEQ(scope.projectID),
		request.SourceEQ(request.SourceAPI), request.CreatedAtGTE(scope.start), request.CreatedAtLT(scope.end),
	)
}

func (scope selfUsageScope) join(s *sql.Selector) *sql.SelectTable {
	r := sql.Table(request.Table)
	s.Join(r).On(s.C(usagelog.FieldRequestID), r.C(request.FieldID))
	s.Where(sql.And(
		sql.InInts(r.C(request.FieldAPIKeyID), scope.keyIDs...),
		sql.EQ(r.C(request.FieldProjectID), scope.projectID),
		sql.EQ(r.C(request.FieldSource), request.SourceAPI),
		sql.GTE(r.C(request.FieldCreatedAt), scope.start),
		sql.LT(r.C(request.FieldCreatedAt), scope.end),
	))
	return r
}

func selfUsageBucket(d string, created string) string {
	switch d {
	case dialect.SQLite:
		// Ent stores SQLite timestamps with a Go timezone suffix. The first
		// 19 characters are the UTC wall clock components (as in dashboard).
		return fmt.Sprintf("CAST(CAST(strftime('%%s', substr(%s, 1, 19)) AS INTEGER) / 900 AS INTEGER)", created)
	case dialect.Postgres:
		// Ent maps field.Time to PostgreSQL timestamptz; EPOCH uses its UTC
		// instant rather than session-local rendering (unlike timestamp without time zone).
		return fmt.Sprintf("FLOOR(EXTRACT(EPOCH FROM %s) / 900)", created)
	case dialect.MySQL:
		// The driver writes UTC wall-clock fields and range predicates compare those
		// same fields; TIMESTAMPDIFF avoids UNIX_TIMESTAMP's session-zone conversion.
		return fmt.Sprintf("TIMESTAMPDIFF(SECOND, '1970-01-01 00:00:00', %s) DIV 900", created)
	default:
		return fmt.Sprintf("FLOOR(EXTRACT(EPOCH FROM %s) / 900)", created)
	}
}

type selfUsageCounts struct {
	Model   string `json:"model"`
	Bucket  int64  `json:"bucket"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
}

type selfUsageAmounts struct {
	Model      string   `json:"model"`
	Bucket     int64    `json:"bucket"`
	RequestID  int      `json:"request_id"`
	Input      int      `json:"input_tokens"`
	Output     int      `json:"output_tokens"`
	CacheRead  int      `json:"cache_read_tokens"`
	CacheWrite int      `json:"cache_write_tokens"`
	Reasoning  int      `json:"reasoning_tokens"`
	Total      int      `json:"total_tokens"`
	Cost       *float64 `json:"cost"`
	Records    int      `json:"usage_records"`
	Unpriced   int      `json:"unpriced_records"`
}

func selfUsageAmountColumns(s *sql.Selector) []string {
	sum := func(field, alias string) string {
		return sql.As(fmt.Sprintf("COALESCE(SUM(%s), 0)", s.C(field)), alias)
	}
	return []string{
		sum(usagelog.FieldPromptTokens, "input_tokens"), sum(usagelog.FieldCompletionTokens, "output_tokens"),
		sum(usagelog.FieldPromptCachedTokens, "cache_read_tokens"), sum(usagelog.FieldPromptWriteCachedTokens, "cache_write_tokens"),
		sum(usagelog.FieldCompletionReasoningTokens, "reasoning_tokens"), sum(usagelog.FieldTotalTokens, "total_tokens"),
		sql.As(fmt.Sprintf("SUM(%s)", s.C(usagelog.FieldTotalCost)), "cost"),
		sql.As(sql.Count(s.C(usagelog.FieldID)), "usage_records"),
		sql.As(fmt.Sprintf("SUM(CASE WHEN %s IS NULL THEN 1 ELSE 0 END)", s.C(usagelog.FieldTotalCost)), "unpriced_records"),
	}
}

func (summary *SelfUsageSummary) addCount(row selfUsageCounts) {
	summary.SuccessRequests += row.Success
	summary.FailedRequests += row.Failed
}

func (summary *SelfUsageSummary) addAmount(row selfUsageAmounts) {
	summary.InputTokens += row.Input
	summary.OutputTokens += row.Output
	summary.CacheReadTokens += row.CacheRead
	summary.CacheWriteTokens += row.CacheWrite
	summary.ReasoningTokens += row.Reasoning
	summary.TotalTokens += row.Total
	summary.UsageRecords += row.Records
	summary.UnpricedRecords += row.Unpriced
	if row.Cost != nil {
		if summary.Cost == nil {
			summary.Cost = new(float64)
		}
		*summary.Cost += *row.Cost
	}
}

func (summary *SelfUsageSummary) finishCost() {
	if summary.UsageRecords == 0 {
		zero := 0.0
		summary.Cost = &zero
	}
}

func (s *SelfUsageService) Stats(ctx context.Context, start, end string, group ...bool) (*SelfUsageStats, error) {
	scope, first, err := s.rangeScope(ctx, start, end, group...)
	if err != nil {
		return nil, err
	}
	return authz.RunWithSystemBypass(ctx, "self-usage", func(ctx context.Context) (*SelfUsageStats, error) {
		var counts []selfUsageCounts
		err := scope.requestQuery(s.client).Modify(func(q *sql.Selector) {
			bucket := selfUsageBucket(q.Dialect(), q.C(request.FieldCreatedAt))
			status := q.C(request.FieldStatus)
			q.Select(sql.As(q.C(request.FieldModelID), "model"), sql.As(bucket, "bucket"),
				sql.As(fmt.Sprintf("SUM(CASE WHEN %s = 'completed' THEN 1 ELSE 0 END)", status), "success"),
				sql.As(fmt.Sprintf("SUM(CASE WHEN %s IN ('failed', 'canceled') THEN 1 ELSE 0 END)", status), "failed"),
			).GroupBy(q.C(request.FieldModelID), bucket)
		}).Scan(ctx, &counts)
		if err != nil {
			return nil, fmt.Errorf("query self usage request counts: %w", err)
		}

		var amounts []selfUsageAmounts
		err = s.client.UsageLog.Query().Modify(func(q *sql.Selector) {
			r := scope.join(q)
			bucket := selfUsageBucket(q.Dialect(), r.C(request.FieldCreatedAt))
			columns := []string{sql.As(r.C(request.FieldModelID), "model"), sql.As(bucket, "bucket")}
			q.Select(append(columns, selfUsageAmountColumns(q)...)...).GroupBy(r.C(request.FieldModelID), bucket)
		}).Scan(ctx, &amounts)
		if err != nil {
			return nil, fmt.Errorf("query self usage amounts: %w", err)
		}

		loc := first.Location()
		result := &SelfUsageStats{ScopeID: scope.id, APIKeyName: scope.apiKeyName, GroupName: scope.groupName, Start: start, End: end, Models: []SelfUsageModelStat{}, Daily: []SelfUsageDailyStat{}}
		byModel := make(map[string]*SelfUsageSummary)
		byDate := make(map[string]*SelfUsageSummary)
		for d := first; d.Before(scope.end.In(loc)); d = d.AddDate(0, 0, 1) {
			date := d.Format("2006-01-02")
			byDate[date] = &SelfUsageSummary{}
			result.Daily = append(result.Daily, SelfUsageDailyStat{Date: date})
		}
		modelSummary := func(model string) *SelfUsageSummary {
			if byModel[model] == nil {
				byModel[model] = &SelfUsageSummary{}
			}
			return byModel[model]
		}
		for _, row := range counts {
			date := time.Unix(row.Bucket*900, 0).In(loc).Format("2006-01-02")
			result.Totals.addCount(row)
			modelSummary(row.Model).addCount(row)
			if daily := byDate[date]; daily != nil {
				daily.addCount(row)
			}
		}
		for _, row := range amounts {
			date := time.Unix(row.Bucket*900, 0).In(loc).Format("2006-01-02")
			result.Totals.addAmount(row)
			modelSummary(row.Model).addAmount(row)
			if daily := byDate[date]; daily != nil {
				daily.addAmount(row)
			}
		}
		result.Totals.finishCost()
		for model, usage := range byModel {
			usage.finishCost()
			result.Models = append(result.Models, SelfUsageModelStat{Model: model, Usage: *usage})
		}
		sort.Slice(result.Models, func(i, j int) bool {
			a, b := result.Models[i].Usage, result.Models[j].Usage
			if a.Cost == nil || b.Cost == nil {
				if a.Cost != nil || b.Cost != nil {
					return a.Cost != nil
				}
			} else if *a.Cost != *b.Cost {
				return *a.Cost > *b.Cost
			}
			if a.SuccessRequests+a.FailedRequests != b.SuccessRequests+b.FailedRequests {
				return a.SuccessRequests+a.FailedRequests > b.SuccessRequests+b.FailedRequests
			}
			return result.Models[i].Model < result.Models[j].Model
		})
		for i := range result.Daily {
			usage := byDate[result.Daily[i].Date]
			usage.finishCost()
			result.Daily[i].Usage = *usage
		}
		return result, nil
	})
}

func (s *SelfUsageService) Requests(ctx context.Context, start, end string, model *string, status string, page, pageSize int, group ...bool) (*SelfUsageRequestPage, error) {
	scope, _, err := s.rangeScope(ctx, start, end, group...)
	if err != nil {
		return nil, err
	}
	if page < 1 || pageSize < 1 || pageSize > 100 || page > int(^uint(0)>>1)/pageSize {
		return nil, fmt.Errorf("invalid_page: page must be positive and pageSize between 1 and 100")
	}
	if status != "" && status != "SUCCESS" && status != "FAILED" && status != "PROCESSING" {
		return nil, fmt.Errorf("invalid_page: unknown status")
	}
	return authz.RunWithSystemBypass(ctx, "self-usage", func(ctx context.Context) (*SelfUsageRequestPage, error) {
		query := func() *ent.RequestQuery {
			q := scope.requestQuery(s.client)
			if model != nil {
				q.Where(request.ModelIDEQ(*model))
			}
			switch status {
			case "SUCCESS":
				q.Where(request.StatusEQ(request.StatusCompleted))
			case "FAILED":
				q.Where(request.StatusIn(request.StatusFailed, request.StatusCanceled))
			case "PROCESSING":
				q.Where(request.StatusIn(request.StatusPending, request.StatusProcessing))
			}
			return q
		}
		total, err := query().Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("count self usage requests: %w", err)
		}
		requests, err := query().Select(
			request.FieldID, request.FieldAPIKeyID, request.FieldCreatedAt, request.FieldModelID, request.FieldStatus,
			request.FieldStream, request.FieldMetricsLatencyMs, request.FieldMetricsFirstTokenLatencyMs,
		).Order(request.ByCreatedAt(sql.OrderDesc()), request.ByID(sql.OrderDesc())).
			Limit(pageSize).Offset((page - 1) * pageSize).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("list self usage requests: %w", err)
		}
		result := &SelfUsageRequestPage{ScopeID: scope.id, APIKeyName: scope.apiKeyName, GroupName: scope.groupName, Items: make([]SelfUsageRequest, 0, len(requests)), Page: page, PageSize: pageSize, Total: total}
		if len(requests) == 0 {
			return result, nil
		}
		ids := make([]int, 0, len(requests))
		for _, req := range requests {
			ids = append(ids, req.ID)
		}
		costs, err := s.loadRequestCosts(ctx, scope, ids)
		if err != nil {
			return nil, err
		}
		var amounts []selfUsageAmounts
		err = s.client.UsageLog.Query().Where(usagelog.RequestIDIn(ids...)).Modify(func(q *sql.Selector) {
			scope.join(q)
			q.Select(append([]string{sql.As(q.C(usagelog.FieldRequestID), "request_id")}, selfUsageAmountColumns(q)...)...).
				GroupBy(q.C(usagelog.FieldRequestID))
		}).Scan(ctx, &amounts)
		if err != nil {
			return nil, fmt.Errorf("query self usage page amounts: %w", err)
		}
		byID := make(map[int]SelfUsageSummary, len(amounts))
		for _, row := range amounts {
			usage := SelfUsageSummary{}
			usage.addAmount(row)
			usage.finishCost()
			byID[row.RequestID] = usage
		}
		keyIDs := make([]int, 0, len(requests))
		for _, req := range requests {
			keyIDs = append(keyIDs, req.APIKeyID)
		}
		keys, err := s.client.APIKey.Query().Where(apikey.IDIn(keyIDs...), apikey.ProjectIDEQ(scope.projectID)).All(ctx)
		if err != nil {
			return nil, err
		}
		labels, err := APIKeyLabels(ctx, s.client, keys)
		if err != nil {
			return nil, err
		}
		for _, req := range requests {
			usage, ok := byID[req.ID]
			if !ok {
				usage.finishCost()
			}
			row := SelfUsageRequest{APIKeyName: labels[req.APIKeyID],
				ID: req.ID, CreatedAt: req.CreatedAt, Model: req.ModelID, Stream: req.Stream,
				InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
				CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
				ReasoningTokens: usage.ReasoningTokens, TotalTokens: usage.TotalTokens,
				Cost: usage.Cost, UsageRecords: usage.UsageRecords, UnpricedRecords: usage.UnpricedRecords,
			}
			row.RequestStatus = string(req.Status)
			row.CostItems = costs[req.ID].items
			row.CostMultiplier = costs[req.ID].multiplier
			row.CostMultiplierMixed = costs[req.ID].multiplierMixed
			switch req.Status {
			case request.StatusCompleted:
				row.Status = "SUCCESS"
			case request.StatusFailed, request.StatusCanceled:
				row.Status = "FAILED"
			default:
				row.Status = "PROCESSING"
			}
			if req.MetricsLatencyMs != nil {
				value := int(*req.MetricsLatencyMs)
				row.LatencyMs = &value
			}
			if req.MetricsFirstTokenLatencyMs != nil {
				value := int(*req.MetricsFirstTokenLatencyMs)
				row.FirstTokenLatencyMs = &value
			}
			result.Items = append(result.Items, row)
		}
		return result, nil
	})
}
