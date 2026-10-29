package biz

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"entgo.io/contrib/entgql"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestChannelTagPrioritiesSaveAndRestore(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ch := createTestChannel(t, client, ctx, "tagged", []string{"model"}, nil)
	ch = ch.Update().SetTags([]string{"Codex", "Other"}).SetSettings(&objects.ChannelSettings{
		StreamFirstEventTimeoutSeconds: lo.ToPtr(20),
		ModelPriceMultiplier:           lo.ToPtr(decimal.RequireFromString("0.15")),
		TagPriorities:                  []objects.ChannelTagPriority{{Tag: "Other", Priority: 5}},
	}).SaveX(ctx)
	updates := []*ChannelTagPriorityUpdate{{ChannelID: objects.GUID{ID: ch.ID}, Priority: lo.ToPtr(0), OrderingWeight: lo.ToPtr(20)}}
	_, err := svc.UpdateChannelTagPriorities(ctx, "Codex", updates)
	require.NoError(t, err)
	stored := client.Channel.GetX(ctx, ch.ID)
	require.Equal(t, 20, stored.OrderingWeight)
	require.Equal(t, 0, *stored.Settings.PriorityForTags(stored.Tags, []string{"Codex"}))
	require.Equal(t, 5, *stored.Settings.PriorityForTags(stored.Tags, []string{"Other"}))
	require.Equal(t, 20, *stored.Settings.StreamFirstEventTimeoutSeconds)
	require.Equal(t, "0.15", stored.Settings.ModelPriceMultiplier.String())

	// Full settings forms do not erase priorities maintained by the routing editor.
	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{ExtraModelPrefix: "alias"}})
	require.NoError(t, err)
	stored = client.Channel.GetX(ctx, ch.ID)
	require.Equal(t, 0, *stored.Settings.PriorityForTags(stored.Tags, []string{"Codex"}))

	updates[0].Priority, updates[0].OrderingWeight = nil, nil
	_, err = svc.UpdateChannelTagPriorities(ctx, "Codex", updates)
	require.NoError(t, err)
	stored = client.Channel.GetX(ctx, ch.ID)
	require.Nil(t, stored.Settings.PriorityForTags(stored.Tags, []string{"Codex"}))
	require.Equal(t, 5, *stored.Settings.PriorityForTags(stored.Tags, []string{"Other"}))
	require.Equal(t, 20, stored.OrderingWeight, "restoring priority must not reset shared weight")

	untagged := createTestChannel(t, client, ctx, "untagged", []string{"model"}, nil)
	_, err = svc.UpdateChannelTagPriorities(ctx, "Codex", []*ChannelTagPriorityUpdate{
		{ChannelID: objects.GUID{ID: ch.ID}, Priority: lo.ToPtr(10), OrderingWeight: lo.ToPtr(99)},
		{ChannelID: objects.GUID{ID: untagged.ID}, Priority: lo.ToPtr(10)},
	})
	require.ErrorContains(t, err, "does not have tag")
	stored = client.Channel.GetX(ctx, ch.ID)
	require.Nil(t, stored.Settings.PriorityForTags(stored.Tags, []string{"Codex"}))
	require.Equal(t, 20, stored.OrderingWeight, "a later invalid row rolls back the whole save")
}

func TestChannelPrioritiesSaveAndRestore(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ch := createTestChannel(t, client, ctx, "global and tags", []string{"model"}, nil)
	ch.Update().SetTags([]string{"Codex", "Shared"}).SetSettings(&objects.ChannelSettings{
		StreamFirstEventTimeoutSeconds: lo.ToPtr(20),
		ModelPriceMultiplier:           lo.ToPtr(decimal.RequireFromString("0.15")),
	}).SaveX(ctx)
	input := ChannelPrioritiesInput{GlobalPriority: lo.ToPtr(10), TagPriorities: []objects.ChannelTagPriority{{Tag: "Codex", Priority: 14}, {Tag: "Shared", Priority: 0}}}
	_, err := svc.UpdateChannelPriorities(ctx, ch.ID, input)
	require.NoError(t, err)
	stored := client.Channel.GetX(ctx, ch.ID)
	require.Equal(t, 10, *stored.Settings.GlobalPriority)
	require.Equal(t, input.TagPriorities, stored.Settings.TagPriorities)
	require.Equal(t, 20, *stored.Settings.StreamFirstEventTimeoutSeconds)
	require.Equal(t, "0.15", stored.Settings.ModelPriceMultiplier.String())

	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Settings: &objects.ChannelSettings{ExtraModelPrefix: "alias"}})
	require.NoError(t, err)
	stored = client.Channel.GetX(ctx, ch.ID)
	require.Equal(t, 10, *stored.Settings.GlobalPriority, "ordinary edits preserve global priority")
	require.Equal(t, input.TagPriorities, stored.Settings.TagPriorities)

	for _, tags := range [][]objects.ChannelTagPriority{
		{{Tag: "Codex", Priority: 5}, {Tag: "Removed", Priority: 6}},
		{{Tag: "Codex", Priority: 5}, {Tag: "Codex", Priority: 6}},
	} {
		_, err = svc.UpdateChannelPriorities(ctx, ch.ID, ChannelPrioritiesInput{GlobalPriority: lo.ToPtr(99), TagPriorities: tags})
		require.Error(t, err)
		stored = client.Channel.GetX(ctx, ch.ID)
		require.Equal(t, 10, *stored.Settings.GlobalPriority, "invalid tag must not partially save global priority")
		require.Equal(t, input.TagPriorities, stored.Settings.TagPriorities)
	}

	_, err = svc.UpdateChannelTagPriorities(ctx, "Shared", []*ChannelTagPriorityUpdate{{ChannelID: objects.GUID{ID: ch.ID}}})
	require.NoError(t, err)
	stored = client.Channel.GetX(ctx, ch.ID)
	require.Equal(t, 10, *stored.Settings.PriorityForTags(stored.Tags, []string{"Shared"}), "batch clearing a tag inherits global")
	require.Equal(t, 14, *stored.Settings.PriorityForTags(stored.Tags, []string{"Codex"}))
	_, err = svc.UpdateChannelPriorities(ctx, ch.ID, ChannelPrioritiesInput{})
	require.NoError(t, err)
	stored = client.Channel.GetX(ctx, ch.ID)
	require.Nil(t, stored.Settings.GlobalPriority)
	require.Empty(t, stored.Settings.TagPriorities)
	require.Nil(t, stored.Settings.PriorityForTags(stored.Tags, []string{"Codex"}))

	untagged := createTestChannel(t, client, ctx, "global only", []string{"model"}, nil)
	_, err = svc.UpdateChannelPriorities(ctx, untagged.ID, ChannelPrioritiesInput{GlobalPriority: lo.ToPtr(0)})
	require.NoError(t, err)
	require.Equal(t, 0, *client.Channel.GetX(ctx, untagged.ID).Settings.PriorityForTags(nil, nil))
}

func TestChannelPriorityIntegerBounds(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ch := createTestChannel(t, client, ctx, "priority bounds", []string{"model"}, nil)
	ch.Update().SetTags([]string{"Codex"}).SaveX(ctx)
	for _, value := range []int{-2147483648, 2147483647} {
		_, err := svc.UpdateChannelPriorities(ctx, ch.ID, ChannelPrioritiesInput{
			GlobalPriority: &value, TagPriorities: []objects.ChannelTagPriority{{Tag: "Codex", Priority: value}},
		})
		require.NoError(t, err)
		stored := client.Channel.GetX(ctx, ch.ID)
		require.Equal(t, value, *stored.Settings.GlobalPriority)
		require.Equal(t, value, stored.Settings.TagPriorities[0].Priority)
	}
	if strconv.IntSize < 64 {
		return // A 32-bit int cannot represent the overflow inputs below.
	}
	for _, raw := range []int64{-2147483649, 2147483648} {
		value := int(raw)
		for _, input := range []ChannelPrioritiesInput{
			{GlobalPriority: &value},
			{GlobalPriority: lo.ToPtr(10), TagPriorities: []objects.ChannelTagPriority{{Tag: "Codex", Priority: value}}},
		} {
			_, err := svc.UpdateChannelPriorities(ctx, ch.ID, input)
			require.ErrorContains(t, err, "between -2147483648 and 2147483647")
		}
		_, err := svc.UpdateChannelTagPriorities(ctx, "Codex", []*ChannelTagPriorityUpdate{
			{ChannelID: objects.GUID{ID: ch.ID}, Priority: &value},
		})
		require.ErrorContains(t, err, "between -2147483648 and 2147483647")
		stored := client.Channel.GetX(ctx, ch.ID)
		require.Equal(t, 2147483647, *stored.Settings.GlobalPriority, "invalid inputs leave all persisted values unchanged")
		require.Equal(t, 2147483647, stored.Settings.TagPriorities[0].Priority)
	}
}

func TestChannelRoutingOrderPagination(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ids := make([]int, 0, 5)
	for i, priority := range []*int{lo.ToPtr(20), lo.ToPtr(0), lo.ToPtr(14), lo.ToPtr(14), nil} {
		ch := createTestChannel(t, client, ctx, string(rune('a'+i)), []string{"model"}, nil)
		settings := &objects.ChannelSettings{ModelPriceMultiplier: lo.ToPtr(decimal.NewFromInt(int64(5 - i)))}
		if priority != nil {
			settings.TagPriorities = []objects.ChannelTagPriority{{Tag: "Codex", Priority: *priority}}
		}
		ch.Update().SetTags([]string{"Codex"}).SetSettings(settings).SaveX(ctx)
		ids = append(ids, ch.ID)
	}
	for _, tc := range []struct {
		name      string
		field     objects.ChannelRoutingOrderField
		direction ent.OrderDirection
		expected  []int
	}{
		{"priority ascending", objects.ChannelRoutingOrderPriority, entgql.OrderDirectionAsc, []int{ids[1], ids[2], ids[3], ids[0], ids[4]}},
		{"priority descending", objects.ChannelRoutingOrderPriority, entgql.OrderDirectionDesc, []int{ids[0], ids[3], ids[2], ids[1], ids[4]}},
		{"multiplier ascending", objects.ChannelRoutingOrderCostMultiplier, entgql.OrderDirectionAsc, []int{ids[4], ids[3], ids[2], ids[1], ids[0]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := QueryChannelsInput{HasTag: lo.ToPtr("Codex"), First: lo.ToPtr(2), RoutingOrder: &ChannelRoutingOrder{Field: tc.field, Direction: tc.direction}}
			var actual []int
			var pages []*ent.ChannelConnection
			for {
				page, err := svc.QueryChannels(ctx, input)
				require.NoError(t, err)
				require.Equal(t, 5, page.TotalCount)
				for _, edge := range page.Edges {
					actual = append(actual, edge.Node.ID)
				}
				pages = append(pages, page)
				if !page.PageInfo.HasNextPage {
					break
				}
				// Real GraphQL cursor serialization catches zero-value loss.
				var encoded bytes.Buffer
				page.PageInfo.EndCursor.MarshalGQL(&encoded)
				var raw string
				require.NoError(t, json.Unmarshal(encoded.Bytes(), &raw))
				cursor := &ent.Cursor{}
				require.NoError(t, cursor.UnmarshalGQL(raw))
				input.After = cursor
				require.LessOrEqual(t, len(actual), 5)
			}
			require.Equal(t, tc.expected, actual)
			input.After, input.First = nil, nil
			input.Before, input.Last = pages[2].PageInfo.StartCursor, lo.ToPtr(2)
			previous, err := svc.QueryChannels(ctx, input)
			require.NoError(t, err)
			require.Equal(t, tc.expected[2:4], lo.Map(previous.Edges, func(edge *ent.ChannelEdge, _ int) int { return edge.Node.ID }))
		})
	}
	// No tag filter uses each channel's own tags, matching the list display.
	pageWithoutTag, err := svc.QueryChannels(ctx, QueryChannelsInput{First: lo.ToPtr(10), RoutingOrder: &ChannelRoutingOrder{Field: objects.ChannelRoutingOrderPriority, Direction: entgql.OrderDirectionAsc}})
	require.NoError(t, err)
	require.Equal(t, []int{ids[1], ids[2], ids[3], ids[0], ids[4]}, lo.Map(pageWithoutTag.Edges, func(edge *ent.ChannelEdge, _ int) int { return edge.Node.ID }))
	// Global values drive unfiltered sorting, while an explicit tag still overrides them.
	_, err = svc.UpdateChannelPriorities(ctx, ids[0], ChannelPrioritiesInput{GlobalPriority: lo.ToPtr(-1), TagPriorities: []objects.ChannelTagPriority{{Tag: "Codex", Priority: 20}}})
	require.NoError(t, err)
	_, err = svc.UpdateChannelPriorities(ctx, ids[4], ChannelPrioritiesInput{GlobalPriority: lo.ToPtr(12)})
	require.NoError(t, err)
	for _, tc := range []struct {
		tag      *string
		expected []int
	}{
		{nil, []int{ids[0], ids[1], ids[4], ids[2], ids[3]}},
		{lo.ToPtr("Codex"), []int{ids[1], ids[4], ids[2], ids[3], ids[0]}},
	} {
		page, err := svc.QueryChannels(ctx, QueryChannelsInput{HasTag: tc.tag, First: lo.ToPtr(10), RoutingOrder: &ChannelRoutingOrder{Field: objects.ChannelRoutingOrderPriority, Direction: entgql.OrderDirectionAsc}})
		require.NoError(t, err)
		require.Equal(t, tc.expected, lo.Map(page.Edges, func(edge *ent.ChannelEdge, _ int) int { return edge.Node.ID }))
	}
	// Filters are applied before sorting/pagination, not to the current page.
	page, err := svc.QueryChannels(ctx, QueryChannelsInput{HasTag: lo.ToPtr("Other"), First: lo.ToPtr(2), RoutingOrder: &ChannelRoutingOrder{Field: objects.ChannelRoutingOrderPriority, Direction: entgql.OrderDirectionAsc}})
	require.NoError(t, err)
	require.Zero(t, page.TotalCount)
}

func TestChannelCostMultiplierQuickEditPreservesPrices(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := channelAutoPriceTestContext(client)
	ch := createAutoPriceTestChannel(t, ctx, client, "quick multiplier", []string{"priced"})
	var base objects.ModelPrice
	require.NoError(t, json.Unmarshal([]byte(`{"items":[{"itemCode":"prompt_tokens","pricing":{"mode":"usage_per_unit","usagePerUnit":"2"}}]}`), &base))
	initial, err := svc.SaveChannelModelPrices(ctx, ch.ID, []SaveChannelModelPriceInput{{ModelID: "priced", Price: base}}, nil)
	require.NoError(t, err)
	updated, err := svc.UpdateChannelCostMultiplier(ctx, ch.ID, decimal.RequireFromString("0.15"))
	require.NoError(t, err)
	require.Len(t, updated, 1)
	require.Equal(t, initial[0].Price.Items, updated[0].Price.Items)
	require.NotEqual(t, initial[0].ReferenceID, updated[0].ReferenceID)
	require.Equal(t, "0.15", updated[0].Price.EffectiveMultiplier().String())
	require.Equal(t, 2, countChannelModelPriceVersions(t, ctx, client, initial[0].ID))
	_, err = svc.UpdateChannelCostMultiplier(ctx, ch.ID, decimal.NewFromInt(-1))
	require.Error(t, err)
	require.Equal(t, "0.15", client.Channel.GetX(ctx, ch.ID).Settings.ModelPriceMultiplier.String())
}
