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
	facts, err := readChannelUsageFacts(ctx, client, []int{id}, start, end)
	if err != nil {
		return nil, err
	}
	return renderChannelUsage([]int{id}, start.Location().String(), facts)[0].Models, nil
}
