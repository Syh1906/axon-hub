package biz

import "time"

func (m *PerformanceRecord) MarkFirstByte(at time.Time) {
	if !m.StartTime.IsZero() {
		m.firstByteLatency.CompareAndSwap(0, max(0, at.Sub(m.StartTime).Milliseconds())+1)
	}
}

func (m *PerformanceRecord) FirstByteLatencyMs() *int64 {
	value := m.firstByteLatency.Load()
	if value == 0 {
		return nil
	}
	latency := value - 1
	return &latency
}
