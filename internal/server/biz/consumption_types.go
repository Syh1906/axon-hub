package biz

import (
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

type ConsumptionDimension string

const (
	ConsumptionDimensionAPIKeyGroup ConsumptionDimension = "API_KEY_GROUP"
	ConsumptionDimensionUser        ConsumptionDimension = "USER"
	ConsumptionDimensionAPIKey      ConsumptionDimension = "API_KEY"
	ConsumptionDimensionChannel     ConsumptionDimension = "CHANNEL"
)

type ConsumptionMetric string

const (
	ConsumptionMetricCost  ConsumptionMetric = "COST"
	ConsumptionMetricCache ConsumptionMetric = "CACHE"
)

type ConsumptionPeriod string

const (
	ConsumptionPeriodToday     ConsumptionPeriod = "TODAY"
	ConsumptionPeriodYesterday ConsumptionPeriod = "YESTERDAY"
	ConsumptionPeriodWeek      ConsumptionPeriod = "WEEK"
	ConsumptionPeriodMonth     ConsumptionPeriod = "MONTH"
	ConsumptionPeriodAll       ConsumptionPeriod = "ALL"
	ConsumptionPeriodCustom    ConsumptionPeriod = "CUSTOM"
)

type ConsumptionFilter struct {
	GroupRef  *int
	ProjectID *objects.GUID
	Dimension ConsumptionDimension
	Metric    ConsumptionMetric
	Period    ConsumptionPeriod
	StartDate *string
	EndDate   *string
	Search    *string
}

type ConsumptionProject struct {
	ID   objects.GUID
	Name string
}

type ConsumptionMetrics struct {
	RequestCount            int      `json:"requestCount"`
	TotalTokens             int64    `json:"totalTokens"`
	TotalCost               float64  `json:"totalCost"`
	CacheRequests           int      `json:"cacheRequests"`
	CacheReadTokens         int64    `json:"cacheReadTokens"`
	CacheInputTokens        int64    `json:"cacheInputTokens"`
	CacheCreationCost       float64  `json:"cacheCreationCost"`
	CacheHitRate            *float64 `json:"cacheHitRate"`
	SuccessRate             *float64 `json:"successRate"`
	AvgFirstTokenMs         *float64 `json:"avgFirstTokenMs"`
	AvgTokensPerSecond      *float64 `json:"avgTokensPerSecond"`
	AvgCostPerRequest       *float64 `json:"avgCostPerRequest"`
	AvgCostPerMillionTokens *float64 `json:"avgCostPerMillionTokens"`
	CacheCoefficient        *int     `json:"cacheCoefficient"`
}

type ConsumptionModel struct {
	Model   string             `json:"model"`
	Metrics ConsumptionMetrics `json:"metrics"`
}

type ConsumptionEntry struct {
	ID      objects.GUID        `json:"id"`
	Name    string              `json:"name"`
	Metrics ConsumptionMetrics  `json:"metrics"`
	Models  []*ConsumptionModel `json:"models"`
}

type ConsumptionResult struct {
	Entries      []*ConsumptionEntry `json:"entries"`
	GeneratedAt  time.Time           `json:"generatedAt"`
	RangeStart   *time.Time          `json:"rangeStart"`
	RangeEnd     time.Time           `json:"rangeEnd"`
	Timezone     string              `json:"timezone"`
	CurrencyCode string              `json:"currencyCode"`
}

// consumptionRange uses calendar days in the configured system timezone.
// The exclusive upper bound includes the entire selected final day, including DST.
func consumptionRange(input ConsumptionFilter, now time.Time, location *time.Location) (*time.Time, time.Time, error) {
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	end := start.AddDate(0, 0, 1)
	switch input.Period {
	case ConsumptionPeriodToday:
	case ConsumptionPeriodYesterday:
		end, start = start, start.AddDate(0, 0, -1)
	case ConsumptionPeriodWeek:
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		end = start.AddDate(0, 0, 7)
	case ConsumptionPeriodMonth:
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
		end = start.AddDate(0, 1, 0)
	case ConsumptionPeriodAll:
		return nil, now.UTC(), nil
	case ConsumptionPeriodCustom:
		if input.StartDate == nil || input.EndDate == nil {
			return nil, time.Time{}, fmt.Errorf("custom period requires startDate and endDate")
		}
		var err error
		start, err = time.ParseInLocation("2006-01-02", *input.StartDate, location)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("invalid startDate: %w", err)
		}
		end, err = time.ParseInLocation("2006-01-02", *input.EndDate, location)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("invalid endDate: %w", err)
		}
		if end.Before(start) {
			return nil, time.Time{}, fmt.Errorf("endDate must not precede startDate")
		}
		end = end.AddDate(0, 0, 1)
	default:
		return nil, time.Time{}, fmt.Errorf("invalid consumption period %q", input.Period)
	}
	// Ent persists UTC timestamps. SQLite compares their textual representation,
	// so keep calendar arithmetic local and normalize the query boundaries.
	start = start.UTC()
	return &start, end.UTC(), nil
}
