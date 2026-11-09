package biz

import (
	"context"
	"strings"

	"entgo.io/contrib/entgql"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
)

// QueryChannelsInput represents the input for querying channels with additional filters.
type QueryChannelsInput struct {
	After                   *entgql.Cursor[int]
	First                   *int
	Before                  *entgql.Cursor[int]
	Last                    *int
	OrderBy                 *ent.ChannelOrder
	RoutingOrder            *ChannelRoutingOrder
	Where                   *ent.ChannelWhereInput
	HasTag                  *string
	Model                   *string
	PrimaryAPIFormat        *string
	ExcludePrimaryAPIFormat *string
	HealthGateAbnormal      *bool
}

// QueryChannels queries channels with the specified input parameters, including model filtering.
func (svc *ChannelService) QueryChannels(ctx context.Context, input QueryChannelsInput) (*ent.ChannelConnection, error) {
	// Build the base query
	var (
		query = svc.entFromContext(ctx).Channel.Query()
		err   error
	)

	// Apply standard filters
	if input.Where != nil {
		query, err = input.Where.Filter(query)
		if err != nil {
			return nil, err
		}
	}

	if input.HasTag != nil && *input.HasTag != "" {
		query = query.Where(func(s *sql.Selector) {
			s.Where(sqljson.ValueContains("tags", *input.HasTag))
		})
	}

	if format := strings.TrimSpace(lo.FromPtr(input.PrimaryAPIFormat)); format != "" {
		query = query.Where(func(s *sql.Selector) {
			s.Where(sqljson.ValueEQ(channel.FieldSettings, format, sqljson.Path("primaryApiFormat")))
		})
	}

	if format := strings.TrimSpace(lo.FromPtr(input.ExcludePrimaryAPIFormat)); format != "" {
		query = query.Where(func(s *sql.Selector) {
			// Missing JSON keys make ValueEQ/ValueNEQ UNKNOWN; keep those rows.
			s.Where(sql.Or(
				sql.Not(sqljson.HasKey(channel.FieldSettings, sqljson.Path("primaryApiFormat"))),
				sqljson.ValueNEQ(channel.FieldSettings, format, sqljson.Path("primaryApiFormat")),
			))
		})
	}

	if input.RoutingOrder != nil {
		return svc.queryChannelsByRoutingOrder(ctx, query, input)
	}

	if input.Model != nil && *input.Model != "" {
		// Model aliases depend on settings. Inspect only these small fields,
		// then let Ent paginate and load details for the matching page.
		var candidates []*ent.Channel
		if err := query.Clone().Select(channel.FieldID, channel.FieldSupportedModels, channel.FieldSettings).Scan(ctx, &candidates); err != nil {
			return nil, err
		}
		ids := make([]int, 0, len(candidates))
		for _, candidate := range candidates {
			if (&Channel{Channel: candidate}).IsModelSupported(*input.Model) {
				ids = append(ids, candidate.ID)
			}
		}
		query = query.Where(channel.IDIn(ids...))
	}

	// The default (ID) order field has no GraphQL name and keeps a nil cursor value.
	if order := input.OrderBy; order != nil && order.Field != nil && order.Field.String() != "" {
		RestoreZeroCursorValue(input.After, order.Field.Value)
		RestoreZeroCursorValue(input.Before, order.Field.Value)
	}
	return query.WithProviderQuotaStatus().Paginate(ctx, input.After, input.First, input.Before, input.Last,
		ent.WithChannelOrder(input.OrderBy),
	)
}
