package biz

import (
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestRecordedLatencyPreservesActualMillisecondsAndMissingFirstToken(t *testing.T) {
	at := time.Now()
	perf := &PerformanceRecord{StartTime: at, EndTime: at.Add(7 * time.Millisecond), Stream: true,
		FirstTokenTime: lo.ToPtr(at.Add(3 * time.Millisecond))}
	perf.MarkFirstByte(at.Add(time.Millisecond))
	actual := perf.LatencyMetrics()
	require.EqualValues(t, 7, *actual.LatencyMs)
	require.EqualValues(t, 3, *actual.FirstTokenLatencyMs)
	require.EqualValues(t, 1, *actual.FirstByteLatencyMs)
	// Routing protection remains separate from recorded facts.
	first, total, _ := perf.Calculate()
	require.EqualValues(t, MinLatencyMs, first)
	require.EqualValues(t, MinLatencyMs, total)
	perf.FirstTokenTime = nil
	require.Nil(t, perf.LatencyMetrics().FirstTokenLatencyMs)
	require.Nil(t, (*PerformanceRecord)(nil).LatencyMetrics())
}
