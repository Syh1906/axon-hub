package biz

import (
	"context"
	"slices"
)

// SnapshotChannels 在取得各渠道观察锁后统一读取配置，避免旧配置覆盖并发更新。
// 锁按 ID 排序获取；返回的配置与快照来自同一轮观察。
func (g *HealthGate) SnapshotChannels(ctx context.Context, ids []int, resolve func() (map[int]HealthGateConfig, error)) (map[int][]HealthGateModelSnapshot, map[int]HealthGateConfig, error) {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	for _, id := range ids {
		lock := g.observationLock(id)
		if err := lock.Acquire(ctx, 1); err != nil {
			return nil, nil, err
		}
		defer lock.Release(1)
	}
	configs, err := resolve()
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	result := make(map[int][]HealthGateModelSnapshot, len(ids))
	for _, id := range ids {
		cfg, current := configs[id]
		result[id] = g.snapshotLocked(id, cfg, current)
	}
	return result, configs, nil
}
