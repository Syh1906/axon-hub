package biz

import "time"

// LatencyMetrics 保存本次执行的实测时间。路由评分的最小延迟保护
// 仅用于 Calculate，不应把不足 10 毫秒的真实计时改写为 10 毫秒。
func (m *PerformanceRecord) LatencyMetrics() *LatencyMetrics {
	if m == nil || m.StartTime.IsZero() {
		return nil
	}
	end := m.EndTime
	if end.IsZero() {
		end = time.Now()
	}
	latency := end.Sub(m.StartTime).Milliseconds()
	metrics := &LatencyMetrics{LatencyMs: &latency, FirstByteLatencyMs: m.FirstByteLatencyMs()}
	if m.Stream && m.FirstTokenTime != nil {
		first := m.FirstTokenTime.Sub(m.StartTime).Milliseconds()
		metrics.FirstTokenLatencyMs = &first
	}
	if m.Stream {
		if reasoning := m.CalculateReasoningDurationMs(); reasoning > 0 {
			metrics.ReasoningDurationMs = &reasoning
		}
	}
	return metrics
}
