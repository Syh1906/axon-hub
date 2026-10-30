package biz

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/predicate"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/scopes"
)

// Request readers need the reporting timezone, not access to system settings.
func (s *SystemService) RequestLogLocation(ctx context.Context) (*time.Location, error) {
	if err := authz.RequireScope(ctx, scopes.ScopeReadRequests); err != nil {
		return nil, err
	}
	return authz.RunWithSystemBypass(ctx, "request-log-timezone", func(ctx context.Context) (*time.Location, error) {
		return s.TimeLocation(ctx), nil
	})
}

func RequestLogTagPredicate(tags []string) predicate.Request {
	return request.HasChannelWith(func(s *sql.Selector) {
		predicates := make([]*sql.Predicate, 0, len(tags))
		for _, tag := range tags {
			predicates = append(predicates, sqljson.ValueContains(channel.FieldTags, tag))
		}
		s.Where(sql.Or(predicates...))
	})
}

func RequestLogGroupPredicate(refs []int) (predicate.Request, error) {
	groups := make([]predicate.APIKey, 0, len(refs))
	for _, ref := range refs {
		if ref < 0 {
			return nil, fmt.Errorf("invalid API key group reference")
		}
		if ref == 0 {
			groups = append(groups, apikey.GroupRefIsNil())
		} else {
			groups = append(groups, apikey.GroupRefEQ(ref))
		}
	}
	return request.HasAPIKeyWith(apikey.Or(groups...)), nil
}

// Calendar presets share the consumption service's timezone/DST boundaries.
// Custom values are wall-clock times from the date-time picker, not browser UTC.
func RequestLogDatePredicates(period ConsumptionPeriod, start, end *string, now time.Time, location *time.Location) ([]predicate.Request, error) {
	var predicates []predicate.Request
	if period != ConsumptionPeriodCustom {
		from, until, err := consumptionRange(ConsumptionFilter{Period: period}, now, location)
		if err != nil {
			return nil, err
		}
		if from != nil {
			predicates = append(predicates, request.CreatedAtGTE(*from))
		}
		return append(predicates, request.CreatedAtLT(until)), nil
	}
	parse := func(value *string) (*time.Time, error) {
		if value == nil || *value == "" {
			return nil, nil
		}
		const layout = "2006-01-02T15:04:05"
		parsed, err := time.ParseInLocation(layout, *value, location)
		if err != nil || parsed.Format(layout) != *value {
			return nil, fmt.Errorf("invalid request log time %q", *value)
		}
		return &parsed, nil
	}
	from, err := parse(start)
	if err != nil {
		return nil, err
	}
	until, err := parse(end)
	if err != nil {
		return nil, err
	}
	if from != nil && until != nil && until.Before(*from) {
		return nil, fmt.Errorf("request log end must not precede start")
	}
	if from != nil {
		predicates = append(predicates, request.CreatedAtGTE(from.UTC()))
	}
	if until != nil {
		predicates = append(predicates, request.CreatedAtLT(until.Add(time.Second).UTC()))
	}
	return predicates, nil
}
