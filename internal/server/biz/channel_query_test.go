package biz

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"entgo.io/contrib/entgql"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

func TestChannelService_QueryChannels_MultipleModels(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	defer svc.Stop()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	first := createTestChannel(t, client, ctx, "first", []string{"gpt-4"}, nil)
	second := createTestChannel(t, client, ctx, "second", []string{"claude"}, &objects.ChannelSettings{
		ModelMappings: []objects.ModelMapping{{From: "alias", To: "claude"}},
	})
	// 与 Ent 写入的 BLOB 混用文本 JSON，覆盖真实 SQLite 查询的解码路径。
	require.NoError(t, client.Driver().Exec(ctx,
		"UPDATE channels SET supported_models = CAST(supported_models AS TEXT), settings = CAST(settings AS TEXT) WHERE id = ?",
		[]any{second.ID}, nil))
	createTestChannel(t, client, ctx, "unmatched", []string{"other"}, nil)
	for _, routingOrder := range []*ChannelRoutingOrder{
		nil,
		{Field: objects.ChannelRoutingOrderPriority, Direction: entgql.OrderDirectionAsc},
		{Field: objects.ChannelRoutingOrderCostMultiplier, Direction: entgql.OrderDirectionAsc},
	} {
		input := QueryChannelsInput{Models: []string{"gpt-4", "alias", "gpt-4"}, First: lo.ToPtr(1), RoutingOrder: routingOrder}
		page, err := svc.QueryChannels(ctx, input)
		require.NoError(t, err)
		require.Equal(t, 2, page.TotalCount)
		require.Len(t, page.Edges, 1)
		require.True(t, page.PageInfo.HasNextPage)
		ids := []int{page.Edges[0].Node.ID}
		input.After = page.PageInfo.EndCursor
		next, err := svc.QueryChannels(ctx, input)
		require.NoError(t, err)
		require.Len(t, next.Edges, 1)
		ids = append(ids, next.Edges[0].Node.ID)
		require.ElementsMatch(t, []int{first.ID, second.ID}, ids)
		require.False(t, next.PageInfo.HasNextPage)

		input.After, input.First = nil, nil
		input.Model = lo.ToPtr("gpt-4")
		intersection, err := svc.QueryChannels(ctx, input)
		require.NoError(t, err)
		require.Len(t, intersection.Edges, 1)
		require.Equal(t, first.ID, intersection.Edges[0].Node.ID)

		input.Model, input.Models = nil, []string{"missing"}
		empty, err := svc.QueryChannels(ctx, input)
		require.NoError(t, err)
		require.Empty(t, empty.Edges)
		require.Zero(t, empty.TotalCount)
	}
}

func TestChannelService_QueryChannels_WithModelFilter(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	// Create test channels with different models
	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "Channel 1", []string{"gpt-4", "gpt-3.5-turbo"}, nil),
		createTestChannel(t, client, ctx, "Channel 2", []string{"gpt-4"}, nil),
		createTestChannel(t, client, ctx, "Channel 3", []string{"claude-3-opus"}, nil),
		createTestChannel(t, client, ctx, "Channel 4", []string{"gpt-4", "claude-3-opus"}, nil),
		createTestChannel(t, client, ctx, "Channel 5", []string{"gpt-3.5-turbo"}, nil),
		createTestChannel(t, client, ctx, "Channel 6", []string{"gpt-4"}, nil),
	}

	tests := []struct {
		name              string
		input             QueryChannelsInput
		expectedIDs       []int
		expectedHasNext   bool
		expectedHasPrev   bool
		expectedEdgeCount int
	}{
		{
			name: "filter by gpt-4 model - first page",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-4"),
				// Model filtering keeps the requested page size
				First: lo.ToPtr(2),
			},
			expectedIDs:       []int{channels[0].ID, channels[1].ID},
			expectedHasNext:   true,
			expectedHasPrev:   false,
			expectedEdgeCount: 2,
		},
		{
			name: "filter by gpt-4 model - all",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-4"),
			},
			expectedIDs:       []int{channels[0].ID, channels[1].ID, channels[3].ID, channels[5].ID},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 4,
		},
		{
			name: "filter by claude-3-opus - exact page",
			input: QueryChannelsInput{
				Model: lo.ToPtr("claude-3-opus"),
				First: lo.ToPtr(2),
			},
			expectedIDs:       []int{channels[2].ID, channels[3].ID},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 2,
		},
		{
			name: "filter by gpt-3.5-turbo",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-3.5-turbo"),
			},
			expectedIDs:       []int{channels[0].ID, channels[4].ID},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 2,
		},
		{
			name: "filter by non-existent model",
			input: QueryChannelsInput{
				Model: lo.ToPtr("non-existent-model"),
			},
			expectedIDs:       []int{},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 0,
		},
		{
			name: "filter by gpt-4 with Last parameter",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-4"),
				Last:  lo.ToPtr(1),
			},
			expectedIDs:       []int{channels[5].ID},
			expectedHasNext:   false,
			expectedHasPrev:   true,
			expectedEdgeCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := svc.QueryChannels(ctx, tt.input)

			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Len(t, conn.Edges, tt.expectedEdgeCount)
			require.Equal(t, tt.expectedHasNext, conn.PageInfo.HasNextPage)
			require.Equal(t, tt.expectedHasPrev, conn.PageInfo.HasPreviousPage)

			// Verify returned channel IDs
			actualIDs := make([]int, len(conn.Edges))
			for i, edge := range conn.Edges {
				actualIDs[i] = edge.Node.ID
			}

			require.ElementsMatch(t, tt.expectedIDs, actualIDs)

			// Verify cursors are set when there are results
			if len(conn.Edges) > 0 {
				require.NotNil(t, conn.PageInfo.StartCursor)
				require.NotNil(t, conn.PageInfo.EndCursor)
			}
		})
	}
}

func TestChannelService_QueryChannels_ModelFilterPagination(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	var expected []int
	for i := 1; i <= 10; i++ {
		models := []string{"claude-3-opus"}
		if i%2 == 0 {
			models = []string{"gpt-4"}
		}
		ch := createTestChannel(t, client, ctx, "FwdChannel"+strconv.Itoa(i), models, nil)
		if i%2 == 0 {
			expected = append(expected, ch.ID)
		}
	}
	var after *ent.Cursor
	var actual []int
	var second *ent.ChannelConnection
	for page := 0; ; page++ {
		result, err := svc.QueryChannels(ctx, QueryChannelsInput{Model: lo.ToPtr("gpt-4"), First: lo.ToPtr(2), After: after})
		require.NoError(t, err)
		require.LessOrEqual(t, len(result.Edges), 2)
		for _, edge := range result.Edges {
			actual = append(actual, edge.Node.ID)
		}
		if page == 1 {
			second = result
		}
		if !result.PageInfo.HasNextPage {
			break
		}
		after = result.PageInfo.EndCursor
	}
	require.Equal(t, expected, actual)
	previous, err := svc.QueryChannels(ctx, QueryChannelsInput{Model: lo.ToPtr("gpt-4"), Last: lo.ToPtr(2), Before: second.PageInfo.StartCursor})
	require.NoError(t, err)
	require.Len(t, previous.Edges, 2)
	require.Equal(t, expected[0], previous.Edges[0].Node.ID)
	require.Equal(t, expected[1], previous.Edges[1].Node.ID)
}

func TestChannelService_QueryChannels_WithModelMapping(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	// Create channels with model mappings
	settings := &objects.ChannelSettings{
		ModelMappings: []objects.ModelMapping{
			{From: "gpt-4-latest", To: "gpt-4"},
			{From: "gpt-4-turbo", To: "gpt-4"},
		},
	}

	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "Channel 1", []string{"gpt-4"}, settings),
		createTestChannel(t, client, ctx, "Channel 2", []string{"gpt-3.5-turbo"}, nil),
		createTestChannel(t, client, ctx, "Channel 3", []string{"gpt-4"}, settings),
	}

	tests := []struct {
		name              string
		model             string
		expectedIDs       []int
		expectedEdgeCount int
	}{
		{
			name:              "query by actual model name",
			model:             "gpt-4",
			expectedIDs:       []int{channels[0].ID, channels[2].ID},
			expectedEdgeCount: 2,
		},
		{
			name:              "query by mapped model name",
			model:             "gpt-4-latest",
			expectedIDs:       []int{channels[0].ID, channels[2].ID},
			expectedEdgeCount: 2,
		},
		{
			name:              "query by another mapped model name",
			model:             "gpt-4-turbo",
			expectedIDs:       []int{channels[0].ID, channels[2].ID},
			expectedEdgeCount: 2,
		},
		{
			name:              "query by unmapped model",
			model:             "gpt-3.5-turbo",
			expectedIDs:       []int{channels[1].ID},
			expectedEdgeCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
				Model: &tt.model,
			})

			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Len(t, conn.Edges, tt.expectedEdgeCount)

			// Verify returned channel IDs
			actualIDs := make([]int, len(conn.Edges))
			for i, edge := range conn.Edges {
				actualIDs[i] = edge.Node.ID
			}

			require.ElementsMatch(t, tt.expectedIDs, actualIDs)
		})
	}
}

func TestChannelService_QueryChannels_WithExtraModelPrefix(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	// Create channels with extra model prefix
	settings := &objects.ChannelSettings{
		ExtraModelPrefix: "deepseek",
	}

	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "DeepSeek Channel", []string{"deepseek-chat", "deepseek-reasoner"}, settings),
		createTestChannel(t, client, ctx, "OpenAI Channel", []string{"gpt-4"}, nil),
	}

	tests := []struct {
		name              string
		model             string
		expectedIDs       []int
		expectedEdgeCount int
	}{
		{
			name:              "query by model without prefix",
			model:             "deepseek-chat",
			expectedIDs:       []int{channels[0].ID},
			expectedEdgeCount: 1,
		},
		{
			name:              "query by model with prefix",
			model:             "deepseek/deepseek-chat",
			expectedIDs:       []int{channels[0].ID},
			expectedEdgeCount: 1,
		},
		{
			name:              "query by model with prefix - reasoner",
			model:             "deepseek/deepseek-reasoner",
			expectedIDs:       []int{channels[0].ID},
			expectedEdgeCount: 1,
		},
		{
			name:              "query by unsupported model with prefix",
			model:             "deepseek/gpt-4",
			expectedIDs:       []int{},
			expectedEdgeCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
				Model: &tt.model,
			})

			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Len(t, conn.Edges, tt.expectedEdgeCount)

			// Verify returned channel IDs
			actualIDs := make([]int, len(conn.Edges))
			for i, edge := range conn.Edges {
				actualIDs[i] = edge.Node.ID
			}

			require.ElementsMatch(t, tt.expectedIDs, actualIDs)
		})
	}
}

func TestChannelService_QueryChannels_WithoutModelFilter(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	// Create test channels
	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "Channel 1", []string{"gpt-4"}, nil),
		createTestChannel(t, client, ctx, "Channel 2", []string{"claude-3-opus"}, nil),
		createTestChannel(t, client, ctx, "Channel 3", []string{"gpt-3.5-turbo"}, nil),
	}

	t.Run("query without model filter", func(t *testing.T) {
		conn, err := svc.QueryChannels(ctx, QueryChannelsInput{})

		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Len(t, conn.Edges, 3)

		// Verify all channels are returned
		actualIDs := make([]int, len(conn.Edges))
		for i, edge := range conn.Edges {
			actualIDs[i] = edge.Node.ID
		}

		expectedIDs := []int{channels[0].ID, channels[1].ID, channels[2].ID}
		require.ElementsMatch(t, expectedIDs, actualIDs)
	})

	t.Run("query without model filter with pagination", func(t *testing.T) {
		conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
			First: lo.ToPtr(2),
		})

		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Len(t, conn.Edges, 2)
		require.True(t, conn.PageInfo.HasNextPage)
		require.False(t, conn.PageInfo.HasPreviousPage)
	})
}

func TestChannelService_QueryChannels_PrimaryAPIFormatFilter(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	imageSettings := &objects.ChannelSettings{PrimaryAPIFormat: objects.PrimaryAPIFormatImageGeneration}
	chatSettings := &objects.ChannelSettings{PrimaryAPIFormat: "openai/chat_completions"}

	image := createTestChannel(t, client, ctx, "Image Channel", []string{"gpt-image-2"}, imageSettings)
	chat := createTestChannel(t, client, ctx, "Chat Channel", []string{"gpt-4"}, chatSettings)
	unmarked := createTestChannel(t, client, ctx, "Unmarked Channel", []string{"gpt-4"}, nil)

	t.Run("keep only image-generation channels", func(t *testing.T) {
		conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
			PrimaryAPIFormat: lo.ToPtr(objects.PrimaryAPIFormatImageGeneration),
		})
		require.NoError(t, err)
		require.Len(t, conn.Edges, 1)
		require.Equal(t, image.ID, conn.Edges[0].Node.ID)
	})

	t.Run("drop image-generation channels from a vendor tab", func(t *testing.T) {
		conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
			ExcludePrimaryAPIFormat: lo.ToPtr(objects.PrimaryAPIFormatImageGeneration),
		})
		require.NoError(t, err)

		actualIDs := make([]int, len(conn.Edges))
		for i, edge := range conn.Edges {
			actualIDs[i] = edge.Node.ID
		}
		require.ElementsMatch(t, []int{chat.ID, unmarked.ID}, actualIDs)
	})
}

func TestChannelService_QueryChannels_OrderingWeightZeroCursor(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	// 3 x 10, 5 x 5, 17 x 0: with a page size of 20 the first page ends on weight 0,
	// whose cursor loses its value through msgpack omitempty.
	weights := []int{10, 10, 10, 5, 5, 5, 5, 5}
	for len(weights) < 25 {
		weights = append(weights, 0)
	}

	for i, weight := range weights {
		ch := createTestChannel(t, client, ctx, "Weighted "+strconv.Itoa(i), []string{"gpt-4"}, nil)
		_, err := client.Channel.UpdateOneID(ch.ID).SetOrderingWeight(weight).Save(ctx)
		require.NoError(t, err)
	}

	orderBy := &ent.ChannelOrder{Direction: entgql.OrderDirectionDesc, Field: ent.ChannelOrderFieldOrderingWeight}

	page1, err := svc.QueryChannels(ctx, QueryChannelsInput{First: lo.ToPtr(20), OrderBy: orderBy})
	require.NoError(t, err)
	require.Len(t, page1.Edges, 20)
	require.True(t, page1.PageInfo.HasNextPage)
	require.Equal(t, 0, page1.Edges[19].Node.OrderingWeight)

	after := roundTripCursor(t, *page1.PageInfo.EndCursor)
	require.Nil(t, after.Value, "msgpack omitempty drops the zero order value")

	page2, err := svc.QueryChannels(ctx, QueryChannelsInput{First: lo.ToPtr(20), After: after, OrderBy: orderBy})
	require.NoError(t, err)
	require.Len(t, page2.Edges, 5)
	require.False(t, page2.PageInfo.HasNextPage)

	seen := make(map[int]bool, 25)
	for _, edge := range page1.Edges {
		seen[edge.Node.ID] = true
	}

	for _, edge := range page2.Edges {
		require.Equal(t, 0, edge.Node.OrderingWeight, "page 2 must not contain weights above the end of page 1")
		require.False(t, seen[edge.Node.ID], "page 2 must not repeat page 1")
		seen[edge.Node.ID] = true
	}

	require.Len(t, seen, 25)
}

// Mirrors the reported symptom: the "all" tab pages correctly because its first
// page ends on a non-zero weight, while a vendor tab with 25 channels and a
// 20-row page ends on weight 0 and used to leak higher weights onto page 2.
func TestChannelService_QueryChannels_VendorTabZeroCursor(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	setWeight := func(ch *ent.Channel, weight int) {
		_, err := client.Channel.UpdateOneID(ch.ID).SetOrderingWeight(weight).Save(ctx)
		require.NoError(t, err)
	}

	// 20 non-OpenAI channels, all weighted, so the "all" tab's first page never ends on 0.
	for i := 1; i <= 20; i++ {
		ch, err := client.Channel.Create().
			SetType(channel.TypeDeepseek).
			SetName("DeepSeek " + strconv.Itoa(i)).
			SetBaseURL("https://api.deepseek.com/v1").
			SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
			SetSupportedModels([]string{"deepseek-chat"}).
			SetDefaultTestModel("deepseek-chat").
			SetStatus(channel.StatusEnabled).
			Save(ctx)
		require.NoError(t, err)
		setWeight(ch, i)
	}

	// 25 OpenAI channels with weights 10 / 5 / 0 interleaved by creation order,
	// so weighted rows also sit on low ids.
	for i := 0; i < 25; i++ {
		ch := createTestChannel(t, client, ctx, "OpenAI "+strconv.Itoa(i), []string{"gpt-4"}, nil)
		switch i % 5 {
		case 0:
			setWeight(ch, 10)
		case 1:
			setWeight(ch, 5)
		}
	}

	orderBy := &ent.ChannelOrder{Direction: entgql.OrderDirectionDesc, Field: ent.ChannelOrderFieldOrderingWeight}

	// Walk every page through GraphQL-shaped cursors and require the concatenation
	// to be one non-increasing weight sequence that covers each row exactly once.
	assertPagesConsistent := func(t *testing.T, label string, base QueryChannelsInput, total int) {
		t.Helper()

		seen := make(map[int]bool, total)
		previousWeight := int(^uint(0) >> 1)

		var after *entgql.Cursor[int]

		for page := 1; ; page++ {
			input := base
			input.First = lo.ToPtr(20)
			input.OrderBy = orderBy
			input.After = after

			conn, err := svc.QueryChannels(ctx, input)
			require.NoError(t, err)
			require.Equal(t, total, conn.TotalCount)
			require.NotEmpty(t, conn.Edges, "%s: page %d is empty", label, page)

			for _, edge := range conn.Edges {
				require.LessOrEqual(t, edge.Node.OrderingWeight, previousWeight,
					"%s: page %d has weight %d after weight %d", label, page, edge.Node.OrderingWeight, previousWeight)
				require.False(t, seen[edge.Node.ID], "%s: page %d repeats channel %d", label, page, edge.Node.ID)
				seen[edge.Node.ID] = true
				previousWeight = edge.Node.OrderingWeight
			}

			if !conn.PageInfo.HasNextPage {
				break
			}

			after = roundTripCursor(t, *conn.PageInfo.EndCursor)
		}

		require.Len(t, seen, total, "%s: pages must cover every row exactly once", label)
	}

	t.Run("all tab", func(t *testing.T) {
		assertPagesConsistent(t, "all", QueryChannelsInput{}, 45)
	})

	t.Run("openai vendor tab", func(t *testing.T) {
		assertPagesConsistent(t, "openai", QueryChannelsInput{
			Where: &ent.ChannelWhereInput{
				TypeIn: []channel.Type{channel.TypeOpenai, channel.TypeOpenaiResponses},
			},
			ExcludePrimaryAPIFormat: lo.ToPtr(objects.PrimaryAPIFormatImageGeneration),
		}, 25)
	})
}

// roundTripCursor serializes a cursor the way GraphQL transports it, so the
// msgpack omitempty behaviour on zero order values is exercised.
func roundTripCursor(t *testing.T, cursor entgql.Cursor[int]) *entgql.Cursor[int] {
	t.Helper()

	var buf bytes.Buffer
	cursor.MarshalGQL(&buf)

	var encoded string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &encoded))

	var decoded entgql.Cursor[int]
	require.NoError(t, decoded.UnmarshalGQL(encoded))

	return &decoded
}

// Helper function to create test channel.
func createTestChannel(
	t *testing.T,
	client *ent.Client,
	ctx context.Context,
	name string,
	models []string,
	settings *objects.ChannelSettings,
) *ent.Channel {
	t.Helper()

	builder := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName(name).
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels(models).
		SetDefaultTestModel(models[0]).
		SetStatus(channel.StatusEnabled)

	if settings != nil {
		builder.SetSettings(settings)
	}

	ch, err := builder.Save(ctx)
	require.NoError(t, err)

	return ch
}
