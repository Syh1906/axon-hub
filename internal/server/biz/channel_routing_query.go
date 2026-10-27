package biz

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"entgo.io/contrib/entgql"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

type ChannelRoutingOrder struct {
	Field     objects.ChannelRoutingOrderField
	Direction ent.OrderDirection
}

type channelRoutingCursorValue struct {
	Field     objects.ChannelRoutingOrderField `json:"field"`
	Tag       string                           `json:"tag"`
	Direction ent.OrderDirection               `json:"direction"`
	Value     *decimal.Decimal                 `json:"value"`
}

type channelRoutingRow struct {
	channel *ent.Channel
	value   *decimal.Decimal
}

// The two values live in channel JSON settings. Sort the filtered set in Go so
// SQLite, MySQL and PostgreSQL use identical decimal/null ordering, then apply
// value-and-ID cursors. Never sort just the current database page.
func (svc *ChannelService) queryChannelsByRoutingOrder(ctx context.Context, query *ent.ChannelQuery, input QueryChannelsInput) (*ent.ChannelConnection, error) {
	order := input.RoutingOrder
	if !order.Field.IsValid() {
		return nil, fmt.Errorf("invalid channel routing order field")
	}
	if err := order.Direction.Validate(); err != nil {
		return nil, err
	}
	if (input.First != nil && *input.First < 0) || (input.Last != nil && *input.Last < 0) || (input.First != nil && input.Last != nil) {
		return nil, fmt.Errorf("provide one non-negative pagination size")
	}
	tag := lo.FromPtr(input.HasTag)
	channels, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]channelRoutingRow, 0, len(channels))
	for _, ch := range channels {
		if input.Model != nil && *input.Model != "" && !(&Channel{Channel: ch}).IsModelSupported(*input.Model) {
			continue
		}
		var value *decimal.Decimal
		if order.Field == objects.ChannelRoutingOrderPriority {
			selectedTags := ch.Tags
			if tag != "" {
				selectedTags = []string{tag}
			}
			if priority := ch.Settings.PriorityForTags(ch.Tags, selectedTags); priority != nil {
				value = lo.ToPtr(decimal.NewFromInt(int64(*priority)))
			}
		} else {
			value = lo.ToPtr(decimal.NewFromInt(1))
			if ch.Settings != nil && ch.Settings.ModelPriceMultiplier != nil {
				value = ch.Settings.ModelPriceMultiplier
			}
		}
		rows = append(rows, channelRoutingRow{channel: ch, value: value})
	}
	compare := func(a, b channelRoutingRow) int {
		// Inherited priorities have no single number until a model is selected.
		// Keep them after configured values in both directions.
		if a.value == nil && b.value != nil {
			return 1
		}
		if a.value != nil && b.value == nil {
			return -1
		}
		result := 0
		if a.value != nil && b.value != nil {
			result = a.value.Cmp(*b.value)
		}
		if result == 0 {
			result = cmp.Compare(a.channel.ID, b.channel.ID)
		}
		if order.Direction == entgql.OrderDirectionDesc {
			result = -result
		}
		return result
	}
	slices.SortFunc(rows, compare)
	connection := &ent.ChannelConnection{TotalCount: len(rows), Edges: []*ent.ChannelEdge{}}
	start, end := 0, len(rows)
	for _, boundary := range []struct {
		cursor *ent.Cursor
		after  bool
	}{{input.After, true}, {input.Before, false}} {
		if boundary.cursor == nil {
			continue
		}
		encoded, ok := boundary.cursor.Value.(string)
		var value channelRoutingCursorValue
		if !ok || json.Unmarshal([]byte(encoded), &value) != nil || value.Field != order.Field || value.Tag != tag || value.Direction != order.Direction {
			return nil, fmt.Errorf("channel routing cursor does not match the selected order")
		}
		row := channelRoutingRow{channel: &ent.Channel{ID: boundary.cursor.ID}, value: value.Value}
		if boundary.after {
			start, _ = slices.BinarySearchFunc(rows, row, func(a, b channelRoutingRow) int {
				if result := compare(a, b); result != 0 {
					return result
				}
				return -1
			})
		} else {
			end, _ = slices.BinarySearchFunc(rows, row, compare)
		}
	}
	if end < start {
		end = start
	}
	if input.First != nil && end-start > *input.First {
		end = start + *input.First
	}
	if input.Last != nil && end-start > *input.Last {
		start = end - *input.Last
	}
	connection.PageInfo.HasPreviousPage = start > 0
	connection.PageInfo.HasNextPage = end < len(rows)
	for _, row := range rows[start:end] {
		value := channelRoutingCursorValue{Field: order.Field, Tag: tag, Direction: order.Direction, Value: row.value}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		connection.Edges = append(connection.Edges, &ent.ChannelEdge{
			Node: row.channel, Cursor: ent.Cursor{ID: row.channel.ID, Value: string(encoded)},
		})
	}
	if len(connection.Edges) > 0 {
		connection.PageInfo.StartCursor = &connection.Edges[0].Cursor
		connection.PageInfo.EndCursor = &connection.Edges[len(connection.Edges)-1].Cursor
	}
	return connection, nil
}
