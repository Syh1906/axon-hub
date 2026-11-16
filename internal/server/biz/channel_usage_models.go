package biz

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

const channelUsageBatchSize = 512

func queryChannelTodayUsageModels(ctx context.Context, client *ent.Client, id int, start, end time.Time) ([]*objects.ChannelTodayModelUsage, error) {
	return readStatisticsSnapshot(ctx, client, false, func(client *ent.Client) ([]*objects.ChannelTodayModelUsage, error) {
		return queryChannelTodayUsageModelsSnapshot(ctx, client, id, start, end)
	})
}

func queryChannelTodayUsageModelsSnapshot(ctx context.Context, client *ent.Client, id int, start, end time.Time) ([]*objects.ChannelTodayModelUsage, error) {
	if err := prepareChannelStandardCosts(ctx, client, []int{id}, start, end); err != nil {
		return nil, err
	}
	rows, err := queryChannelUsageModelsBatch(ctx, client, []int{id}, start, end)
	if err != nil {
		return nil, err
	}
	if rows[id] == nil {
		return []*objects.ChannelTodayModelUsage{}, nil
	}
	return rows[id], nil
}
