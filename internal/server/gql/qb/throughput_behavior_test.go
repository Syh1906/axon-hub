package qb

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/looplj/axonhub/internal/pkg/sqlite"
)

func TestThroughputQueriesAverageOnlyValidTextStreams(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:speed-query?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE channels(id INTEGER, name TEXT, type TEXT);
CREATE TABLE requests(id INTEGER, model_id TEXT);
CREATE TABLE models(model_id TEXT, name TEXT);
CREATE TABLE request_executions(id INTEGER, request_id INTEGER, channel_id INTEGER, status TEXT, stream BOOLEAN, format TEXT, metrics_latency_ms INTEGER, metrics_first_token_latency_ms INTEGER, metrics_first_byte_latency_ms INTEGER, created_at TEXT);
CREATE TABLE usage_logs(id INTEGER PRIMARY KEY, request_id INTEGER, channel_id INTEGER, completion_tokens INTEGER, cost_snapshot TEXT, completion_audio_tokens INTEGER DEFAULT 0);
INSERT INTO channels VALUES(1,'channel','openai'); INSERT INTO models VALUES('model','model');
INSERT INTO requests VALUES(1,'model'),(2,'model'),(3,'model'),(4,'model'),(5,'model'),(6,'model'),(7,'model'),(8,'model'),(9,'model');
INSERT INTO request_executions VALUES
(0,1,1,'failed',1,'openai/chat_completions',2000,1500,1000,'2026-11-01 01:00:00'),
(1,1,1,'completed',1,'openai/chat_completions',2000,1500,1000,'2026-11-01 01:00:00'),
(2,2,1,'failed',1,'openai/chat_completions',4000,2000,1000,'2026-11-01 01:00:00'),
(3,3,1,'completed',0,'openai/chat_completions',60000,NULL,59000,'2026-11-01 01:00:00'),
(4,4,1,'completed',1,'openai/image_generations',60000,NULL,59000,'2026-11-01 01:00:00'),
(5,5,1,'completed',1,'openai/chat_completions',2000,1500,NULL,'2026-11-01 01:00:00'),
(6,6,1,'completed',1,'gemini/contents',2000,1500,1000,'2026-11-01 01:00:00'),
(7,7,1,'completed',1,'openai/chat_completions',2000,1500,1000,'2026-11-01 01:00:00'),
(8,8,1,'completed',1,'openai/chat_completions',2000,1500,1000,'2026-11-01 01:00:00'),
(9,9,1,'canceled',1,'openai/chat_completions',2000,1500,1000,'2026-11-01 01:00:00');
INSERT INTO usage_logs(request_id,channel_id,completion_tokens) VALUES(1,1,100),(2,1,1500),(3,1,4000),(4,1,4000),(5,1,4000),(6,1,4000),(7,1,4000),(7,1,4000),(8,1,4000),(9,1,300);
UPDATE usage_logs SET cost_snapshot='{"images":[{"source":"file"}]}' WHERE request_id=6;
UPDATE usage_logs SET completion_audio_tokens=4000 WHERE request_id=8;
ALTER TABLE request_executions ADD COLUMN project_id INTEGER DEFAULT 1;
ALTER TABLE usage_logs ADD COLUMN project_id INTEGER DEFAULT 1;
CREATE TABLE budget_charges(execution_id INTEGER, project_id INTEGER, request_id INTEGER, usage_log_id INTEGER);`)
	require.NoError(t, err)
	for _, mode := range []ThroughputQueryMode{ThroughputModeRowNumber, ThroughputModeMaxID} {
		for _, query := range []string{
			BuildThroughputQuery(false, ThroughputQueryByChannel, 10, mode),
			BuildThroughputQuery(false, ThroughputQueryByModel, 10, mode),
			BuildDailyModelThroughputQuery("sqlite3", "UTC", 0, 10, mode),
			BuildDailyChannelThroughputQuery("sqlite3", "UTC", 0, 10, mode),
			BuildDailyPerformanceStatsQuery("sqlite3", "UTC", 0, DailyThroughputByModel, "?", mode),
			BuildDailyPerformanceStatsQuery("sqlite3", "UTC", 0, DailyThroughputByChannel, "?", mode),
		} {
			args := []any{"2026-10-31 00:00:00"}
			if mode == ThroughputModeMaxID && query == BuildThroughputQuery(false, ThroughputQueryByChannel, 10, mode) || mode == ThroughputModeMaxID && query == BuildThroughputQuery(false, ThroughputQueryByModel, 10, mode) {
				args = append(args, args[0])
			}
			rows, err := db.Query(query, args...)
			require.NoError(t, err)
			columns, err := rows.Columns()
			require.NoError(t, err)
			require.True(t, rows.Next())
			values, destinations := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			require.NoError(t, rows.Scan(destinations...))
			for i, column := range columns {
				if column == "throughput" {
					require.Equal(t, (100.0+500.0+300.0)/3, values[i])
				}
				if column == "request_count" {
					count := 3
					if strings.Contains(query, "speed_sample_count") {
						count = 9
						if strings.Contains(query, "se.channel_id as id") {
							count = 10
						}
					}
					require.EqualValues(t, count, values[i])
				}
				if column == "speed_sample_count" {
					require.EqualValues(t, 3, values[i])
				}
				if column == "first_token_sample_count" {
					count := 7
					if strings.Contains(query, "se.channel_id as id") {
						count = 8
					}
					require.EqualValues(t, count, values[i])
				}
			}
			require.False(t, rows.Next())
			require.NoError(t, rows.Close())
		}
	}
}
