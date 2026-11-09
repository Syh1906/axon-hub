package usageanalytics_test

import (
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/looplj/axonhub/internal/pkg/sqlite"
	"github.com/looplj/axonhub/internal/pkg/usageanalytics"
	"github.com/stretchr/testify/require"
)

func TestOutputSpeedUsesActualFirstByteWindow(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	for _, tc := range []struct {
		name             string
		tokens, duration int
		first            any
		want             any
	}{
		{"扣除等待首字节", 100, 1500, 500, 100.0},
		{"首字节不是首个内容", 100, 2500, 500, 50.0},
		{"无首字节证据", 100, 1500, nil, nil},
		{"窗口不足100毫秒", 100, 1500, 1450, nil},
		{"时序无效", 100, 1500, 1600, nil},
		{"集中缓冲后输出", 10000, 10000, 9900, nil},
		{"无输出", 0, 1500, 500, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expression := usageanalytics.OutputSpeedSQL("tokens", "duration", "first_byte", "stream", "format")
			query := fmt.Sprintf("SELECT %s FROM (SELECT ? AS tokens, ? AS duration, ? AS first_byte, TRUE AS stream, 'openai/responses' AS format)", expression)
			var got sql.NullFloat64
			require.NoError(t, db.QueryRow(query, tc.tokens, tc.duration, tc.first).Scan(&got))
			if tc.want == nil {
				require.False(t, got.Valid)
			} else {
				require.True(t, got.Valid)
				require.InDelta(t, tc.want, got.Float64, 1e-9)
			}
		})
	}
}
