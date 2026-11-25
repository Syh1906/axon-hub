package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

// 渠道元信息独立于统计缓存，改标签不触发历史用量重新聚合。
// 一次轻量投影确定全站重名，搜索、项目过滤和排序不改变辨认方式。
func (s *ConsumptionService) withChannelIdentity(ctx context.Context, input ConsumptionFilter, result *ConsumptionResult) (*ConsumptionResult, error) {
	if input.Dimension != ConsumptionDimensionChannel || len(result.Entries) == 0 {
		return result, nil
	}
	identity, err := loadConsumptionChannelIdentity(ctx, s.client, input.Dimension)
	if err != nil {
		return nil, err
	}
	return identity.apply(result), nil
}

type consumptionChannelIdentity struct {
	channels []*ent.Channel
	counts   map[string]int
	version  string
}

// A page reads this small projection once. Its version changes downloaded
// labels without changing the fingerprint used to compute numeric day totals.
func loadConsumptionChannelIdentity(ctx context.Context, client *ent.Client, dimension ConsumptionDimension) (*consumptionChannelIdentity, error) {
	if dimension != ConsumptionDimensionChannel {
		return nil, nil
	}
	channels, err := client.Channel.Query().Select(channel.FieldID, channel.FieldName, channel.FieldType, channel.FieldTags, channel.FieldRemark).Order(ent.Asc(channel.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(channels))
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	// 让已缓存旧版标签顺序的客户端重新读取渠道展示信息。
	_ = encoder.Encode("configured-tag-order")
	for _, ch := range channels {
		counts[ch.Name]++
		_ = encoder.Encode([]any{ch.ID, ch.Name, ch.Type, ch.Tags, ch.Remark})
	}
	return &consumptionChannelIdentity{channels: channels, counts: counts, version: hex.EncodeToString(hash.Sum(nil)[:16])}, nil
}

func (identity *consumptionChannelIdentity) revision(numeric string) string {
	if identity == nil {
		return numeric
	}
	return numeric + ":" + identity.version
}

func (identity *consumptionChannelIdentity) apply(result *ConsumptionResult) *ConsumptionResult {
	if identity == nil {
		return result
	}
	// 只复制结果外壳和实体；缓存中的指标和模型明细保持只读共享。
	copyResult := *result
	copyResult.Entries = make([]*ConsumptionEntry, len(result.Entries))
	byID := make(map[int]*ConsumptionEntry, len(result.Entries))
	for i, entry := range result.Entries {
		copyEntry := *entry
		copyEntry.ChannelTags = []string{}
		copyEntry.ChannelRemark = ""
		copyResult.Entries[i] = &copyEntry
		byID[entry.ID.ID] = &copyEntry
	}
	for _, ch := range identity.channels {
		entry := byID[ch.ID]
		if entry == nil {
			continue
		}
		entry.Name = ch.Name
		if ch.Remark != nil {
			entry.ChannelRemark = *ch.Remark
		}
		entry.ChannelProvider = objects.ChannelProvider(string(ch.Type))
		entry.DuplicateName = identity.counts[ch.Name] > 1
		entry.ChannelTags = append([]string{}, ch.Tags...)
	}
	return &copyResult
}
