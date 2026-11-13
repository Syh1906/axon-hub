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
	ConsumptionDimensionModel       ConsumptionDimension = "MODEL"
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
	// 只由 GraphQL 字段选择确定，不改变旧客户端的完整统计契约。
	OmitPerformance bool
	Uncached        bool `json:"-"`
	DailySnapshot   bool `json:"-"`
	GroupRef        *int
	ProjectID       *objects.GUID
	Dimension       ConsumptionDimension
	Metric          ConsumptionMetric
	Period          ConsumptionPeriod
	StartDate       *string
	EndDate         *string
	Search          *string
}

type ConsumptionProject struct {
	ID   objects.GUID
	Name string
}

type ConsumptionMetrics struct {
	PricedCost              float64  `json:"pricedCost"`
	TokenPricedCost         float64  `json:"tokenPricedCost"`
	PricedTokens            int64    `json:"pricedTokens"`
	SuccessCount            int64    `json:"successCount"`
	OutcomeCount            int64    `json:"outcomeCount"`
	FirstTokenSum           float64  `json:"firstTokenSum"`
	FirstTokenCount         int64    `json:"firstTokenCount"`
	SpeedSum                float64  `json:"speedSum"`
	CoefficientSamples      int64    `json:"coefficientSamples"`
	CoefficientEligible     int64    `json:"coefficientEligible"`
	CoefficientTheoretical  int64    `json:"coefficientTheoretical"`
	CoefficientObserved     int64    `json:"coefficientObserved"`
	RequestCount            int      `json:"requestCount"`
	TotalTokens             int64    `json:"totalTokens"`
	TotalCost               float64  `json:"totalCost"`
	UnpricedRequestCount    int      `json:"unpricedRequestCount"`
	PricedRequestCount      int      `json:"pricedRequestCount"`
	TokenPricedRequestCount int      `json:"tokenPricedRequestCount"`
	SpeedSampleCount        int64    `json:"speedSampleCount"`
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
	ChannelRemark   string              `json:"channelRemark"`
	ChannelProvider string              `json:"channelProvider"`
	ChannelTags     []string            `json:"channelTags"`
	DuplicateName   bool                `json:"duplicateName"`
	ID              objects.GUID        `json:"id"`
	Name            string              `json:"name"`
	Metrics         ConsumptionMetrics  `json:"metrics"`
	Models          []*ConsumptionModel `json:"models"`
}

type ConsumptionResult struct {
	Revision           string                          `json:"revision"`
	CacheSamples       []*ConsumptionCacheSample       `json:"cacheSamples"`
	PerformanceSamples []*ConsumptionPerformanceSample `json:"performanceSamples"`
	ResolvedAt         time.Time                       `json:"resolvedAt"`
	Entries            []*ConsumptionEntry             `json:"entries"`
	ModelEntries       []*ConsumptionModel             `json:"modelEntries,omitempty"`
	GeneratedAt        time.Time                       `json:"generatedAt"`
	RangeStart         *time.Time                      `json:"rangeStart"`
	RangeEnd           time.Time                       `json:"rangeEnd"`
	Timezone           string                          `json:"timezone"`
	CurrencyCode       string                          `json:"currencyCode"`
}

// 每页限制日期数，浏览器只合并完整收到的日期，不把部分金额当成全量。
type ConsumptionSlicePage struct {
	Days         []*ConsumptionSliceRevision
	Pending      bool
	ResolvedAt   time.Time
	Slices       []*ConsumptionResult
	Next         *time.Time
	RangeStart   *time.Time
	RangeEnd     time.Time
	Timezone     string
	CurrencyCode string
}

type ConsumptionSliceRevision struct {
	Start    time.Time
	Revision string
}

type ConsumptionCacheSample struct {
	ChannelID   objects.GUID `json:"channelID"`
	Model       string       `json:"model"`
	Samples     int64        `json:"samples"`
	Eligible    int64        `json:"eligible"`
	Theoretical int64        `json:"theoretical"`
	Observed    int64        `json:"observed"`
}

type ConsumptionPerformanceSample struct {
	ChannelID objects.GUID       `json:"channelID"`
	Name      string             `json:"name"`
	Model     string             `json:"model"`
	Metrics   ConsumptionMetrics `json:"metrics"`
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
