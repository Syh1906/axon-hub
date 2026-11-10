package biz

import (
	"context"
	"sort"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

// 渠道元信息独立于统计缓存，改标签不触发历史用量重新聚合。
// 一次轻量投影确定全站重名，搜索、项目过滤和排序不改变辨认方式。
func (s *ConsumptionService) withChannelIdentity(ctx context.Context, input ConsumptionFilter, result *ConsumptionResult) (*ConsumptionResult, error) {
	if input.Dimension != ConsumptionDimensionChannel || len(result.Entries) == 0 {
		return result, nil
	}
	channels, err := s.client.Channel.Query().Select(channel.FieldID, channel.FieldName, channel.FieldType, channel.FieldTags, channel.FieldRemark).All(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(channels))
	frequencies := make(map[string]map[string]int)
	for _, ch := range channels {
		counts[ch.Name]++
	}
	for _, ch := range channels {
		if counts[ch.Name] < 2 {
			continue
		}
		if frequencies[ch.Name] == nil {
			frequencies[ch.Name] = make(map[string]int)
		}
		seen := make(map[string]bool, len(ch.Tags))
		for _, tag := range ch.Tags {
			if !seen[tag] {
				frequencies[ch.Name][tag]++
				seen[tag] = true
			}
		}
	}
	// 只复制结果外壳和实体；缓存中的指标和模型明细保持只读共享。
	copyResult := *result
	copyResult.Entries = make([]*ConsumptionEntry, len(result.Entries))
	byID := make(map[int]*ConsumptionEntry, len(result.Entries))
	for i, entry := range result.Entries {
		copyEntry := *entry
		copyEntry.ChannelTags = []string{}
		copyResult.Entries[i] = &copyEntry
		byID[entry.ID.ID] = &copyEntry
	}
	for _, ch := range channels {
		entry := byID[ch.ID]
		if entry == nil {
			continue
		}
		entry.Name = ch.Name
		if ch.Remark != nil {
			entry.ChannelRemark = *ch.Remark
		}
		entry.ChannelProvider = objects.ChannelProvider(string(ch.Type))
		entry.DuplicateName = counts[ch.Name] > 1
		entry.ChannelTags = append([]string{}, ch.Tags...)
		if entry.DuplicateName {
			sort.SliceStable(entry.ChannelTags, func(i, j int) bool {
				return frequencies[ch.Name][entry.ChannelTags[i]] < frequencies[ch.Name][entry.ChannelTags[j]]
			})
		}
	}
	return &copyResult, nil
}
