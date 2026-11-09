package qb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// This file contains the real-PostgreSQL parity tests for the DISTINCT ON
// dashboard query rewrites (#2754), plus the gate-free non-PostgreSQL
// fallback tests.
//
// Parity semantics: for an identical adversarial fixture, the baseline
// (ThroughputModeRowNumber) and candidate (ThroughputModeDistinctOn) queries
// must return exactly the same multiset of rows for:
//
//   - BuildThroughputQuery by channel and by model (sufficient limit),
//   - BuildDailyModelThroughputQuery and BuildDailyChannelThroughputQuery
//     with a per-day limit that actually excludes groups,
//   - BuildDailyPerformanceStatsQuery for model and channel grouping.
//
// Parity is asserted two ways: a sorted in-Go multiset comparison, and
// PostgreSQL EXCEPT ALL in both directions (baseline EXCEPT candidate,
// candidate EXCEPT baseline) as single statements, so the shared $1
// parameter stays valid inside one statement.
//
// The fixture additionally carries hand-computed expected aggregate values so
// the edge cases (created_at ties between executions with different ids,
// status filtering, zero-latency filtering, production-nullable channel_id and
// metrics_latency_ms, duplicate usage-log rows, the exact window boundary,
// streaming effective latency including TTFT >= latency, multiple dates, and
// per-day limit cutoff) are proven by the fixture itself,
// independent of which implementation the parity check compares.
//
// The PostgreSQL parity test is gated on AXONHUB_TEST_PG_DSN and skips cleanly
// when unset. It creates a unique temporary schema pinned via search_path
// and drops it in t.Cleanup, matching the repo convention (see
// internal/server/biz/quota_routing_migration_postgres_test.go).

// paritySince is the inclusive lower bound of the dashboard window:
// 2024-03-01T02:00:00Z. Rows exactly at this instant are included; rows one
// millisecond before are not.
var paritySince = time.Date(2024, 3, 1, 2, 0, 0, 0, time.UTC)

// dayBase returns the UTC midnight of fixture day 1..4 (2024-03-01..04).
func dayBase(day int) time.Time {
	return time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(day-1) * 24 * time.Hour)
}

type parityChannel struct {
	id   int
	name string
	typ  string
}

type parityModel struct {
	id   string
	name string
}

type parityRequest struct {
	id      int
	modelID string
}

// parityExecution is one seeded request_executions row. Execution ids start
// at 301 so they cannot collide with request ids (1..23), keeping the
// created_at tie-break on id unambiguous.
type parityExecution struct {
	id          int
	requestID   int
	channelID   int
	status      string
	created     time.Time
	latencyMS   int64 // 0 means the zero-latency filter case
	firstByteMS int64
	ttftMS      int64 // 0 means metrics_first_token_latency_ms IS NULL
	stream      bool
	noChannel   bool // channel_id is written as IS NULL (nullable in production)
	latencyNull bool // metrics_latency_ms is written as IS NULL (nullable in production)
}

type parityUsage struct {
	requestID int
	completed int64
}

// buildParityFixture 保留同时间重试、跨日尝试、空计时、取消、重复用量及时间边界。
// 首字节是独立的合成计时字段；非流式和缺少唯一执行用量关联的记录不贡献速度。
// R10 为 10 tok/s，R9 为 20 tok/s，R15 为 200 tok/s；R18 的重复用量不猜测归属。
// 日性能保留仅有首字的日期；渠道按全部尝试，模型按请求最终执行统计。
func buildParityFixture() (channels []parityChannel, models []parityModel, requests []parityRequest, executions []parityExecution, usages []parityUsage) {
	ch := func(id int, name, typ string) parityChannel { return parityChannel{id: id, name: name, typ: typ} }
	md := func(id, name string) parityModel { return parityModel{id: id, name: name} }
	rq := func(id int, modelID string) parityRequest { return parityRequest{id: id, modelID: modelID} }
	tx := func(id, requestID, channelID, ds, h int, latency, firstByte, ttft int64, stream bool) parityExecution {
		return parityExecution{
			id: id, requestID: requestID, channelID: channelID, status: "completed",
			created:   dayBase(ds).Add(time.Duration(h) * time.Hour),
			latencyMS: latency, firstByteMS: firstByte, ttftMS: ttft, stream: stream,
		}
	}
	ul := func(requestID int, completed int64) parityUsage {
		return parityUsage{requestID: requestID, completed: completed}
	}

	channels = []parityChannel{
		ch(1, "Alpha", "openai"),
		ch(2, "Beta", "anthropic"),
		ch(3, "Gamma", "local"),
	}
	models = []parityModel{
		md("m1", "model-one"),
		md("m2", "model-two"),
		md("m3", "model-three"),
	}
	requests = []parityRequest{
		rq(1, "m1"), rq(2, "m2"), rq(3, "m1"), rq(4, "m2"), rq(5, "m1"),
		rq(6, "m1"), rq(7, "m3"), rq(8, "m2"), rq(9, "m2"), rq(10, "m1"),
		rq(11, "m3"), rq(12, "m2"), rq(13, "m1"), rq(14, "m1"), rq(15, "m2"),
		rq(16, "m1"), rq(17, "m1"), rq(18, "m2"), rq(19, "m2"), rq(20, "m2"),
		rq(21, "m1"), rq(22, "m2"), rq(23, "m3"),
	}
	executions = []parityExecution{
		tx(301, 1, 1, 1, 3, 10000, 0, 0, false),
		tx(302, 2, 1, 1, 5, 500, 0, 0, false),
		tx(303, 6, 1, 1, 10, 600, 0, 0, false),  // tie, lower id
		tx(304, 6, 1, 1, 10, 3000, 0, 0, false), // tie, higher id wins
		{
			id:        305,
			requestID: 7,
			channelID: 1,
			status:    "failed",
			created:   dayBase(1).Add(11 * time.Hour),
			latencyMS: 800,
		},
		{
			id:        306,
			requestID: 7,
			channelID: 1,
			status:    "canceled",
			created:   dayBase(1).Add(11 * time.Hour),
			latencyMS: 900,
		},
		tx(307, 8, 3, 1, 12, 0, 0, 0, false),   // zero latency
		tx(308, 4, 2, 1, 1, 0, 0, 0, false),    // pre-window, zero latency
		tx(309, 5, 3, 1, 0, 2000, 0, 0, false), // pre-window
		tx(310, 9, 2, 1, 4, 4000, 0, 0, true),
		tx(311, 10, 1, 1, 2, 2000, 0, 0, true), // 流式首字未知，但首字节为 0
		tx(312, 16, 1, 1, 2, 300, 0, 0, false), // exact window boundary
		{
			id:        313,
			requestID: 17,
			channelID: 2,
			status:    "completed",
			created:   paritySince.Add(-time.Millisecond),
			latencyMS: 4000,
			ttftMS:    500,
			stream:    true,
		},
		tx(314, 18, 1, 2, 1, 1000, 0, 0, false),
		tx(315, 19, 2, 2, 2, 500, 0, 0, false),
		tx(316, 20, 1, 2, 3, 2000, 3000, 3000, true), // 首字节与首字均超出总耗时，排除两个指标
		tx(317, 21, 2, 2, 4, 3000, 4000, 4000, true), // 首字节与首字均超出总耗时，排除两个指标
		tx(318, 22, 2, 2, 5, 1500, 400, 400, true),   // 首字 400 ms，后续同渠道执行使本条用量归属未知
		tx(319, 23, 3, 3, 1, 4000, 0, 0, false),
		tx(320, 15, 2, 3, 2, 1000, 500, 500, true), // 首字节 500 ms，生成窗口 500 ms
		tx(321, 16, 1, 3, 3, 4000, 0, 0, false),
		tx(322, 17, 1, 3, 4, 4000, 0, 0, false),
		tx(323, 18, 2, 4, 1, 2500, 0, 0, true), // 流式首字未知，但首字节为 0
		tx(324, 19, 2, 4, 2, 1000, 0, 0, false),
		tx(325, 20, 2, 4, 3, 1000, 0, 0, false),
		tx(326, 21, 1, 4, 4, 1000, 0, 0, false),
		tx(327, 22, 2, 4, 5, 1200, 2000, 2000, true), // 首字节与首字均超出总耗时，排除两个指标
		tx(328, 23, 3, 4, 6, 800, 1000, 1000, true),  // 首字节与首字均超出总耗时，排除两个指标
		{
			id:        329,
			requestID: 11,
			noChannel: true, // channel_id IS NULL: by-model keeps it, by-channel join drops it
			status:    "completed",
			created:   dayBase(3).Add(7 * time.Hour),
			latencyMS: 6000,
		},
		{
			id:          330,
			requestID:   17,
			channelID:   1,
			status:      "completed",
			created:     dayBase(3).Add(5 * time.Hour),
			latencyNull: true, // NULL latency: filtered before latest-per-request selection
		},
	}
	usages = []parityUsage{
		ul(1, 100), ul(2, 100), ul(3, 300), ul(4, 300), ul(5, 200),
		ul(6, 60), ul(7, 10), ul(8, 50), ul(9, 80), ul(10, 20),
		ul(11, 300), ul(12, 80), ul(13, 90), ul(14, 70), ul(15, 100),
		ul(16, 450), ul(17, 450), ul(18, 250), ul(18, 125),
		ul(19, 500), ul(20, 300), ul(21, 600), ul(22, 55), ul(23, 400),
	}
	return channels, models, requests, executions, usages
}

// newParitySchema creates a unique temporary schema on the DSN, creates the
// bare tables the qb queries read, seeds the fixture, and wires cleanup.
// usage_logs.request_id is intentionally non-unique here so the fixture
// exercises duplicate usage rows, as allowed by the production schema.
func newParitySchema(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)

	adminDB := stdlib.OpenDB(*config.Copy())
	require.NoError(t, adminDB.PingContext(context.Background()))

	schema := "qb_par_" + uuid.NewString()[:8]
	_, err = adminDB.ExecContext(context.Background(), `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = adminDB.Close()
	})

	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	require.NoError(t, db.PingContext(context.Background()))
	t.Cleanup(func() { _ = db.Close() })

	seedParityFixture(t, db, "postgres")
	return db
}

func seedParityFixture(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	ddl := []string{
		`CREATE TABLE channels (
			id INT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL
		)`,
		`CREATE TABLE models (
			model_id TEXT PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE requests (
			id INT PRIMARY KEY,
			model_id TEXT NOT NULL
		)`,
		`CREATE TABLE request_executions (
			id BIGINT PRIMARY KEY,
			request_id INT NOT NULL,
			channel_id INT NULL,
			status TEXT NOT NULL,
			metrics_latency_ms BIGINT NULL,
			metrics_first_token_latency_ms BIGINT,
			metrics_first_byte_latency_ms BIGINT,
			project_id INT NOT NULL DEFAULT 1,
			format TEXT NOT NULL DEFAULT 'openai/chat_completions',
			stream BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE TABLE usage_logs (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			request_id INT NOT NULL,
			completion_tokens BIGINT NOT NULL,
			channel_id INT NULL,
			project_id INT NOT NULL DEFAULT 1,
			cost_snapshot JSONB,
			completion_audio_tokens BIGINT DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL
		)`,
	}
	ddl = append(ddl, `CREATE TABLE budget_charges(execution_id BIGINT, project_id INT, request_id INT, usage_log_id BIGINT)`)
	ctx := context.Background()
	for _, stmt := range ddl {
		if dialect == "sqlite3" {
			stmt = strings.ReplaceAll(stmt, "BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY", "INTEGER PRIMARY KEY")
		}

		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err, "DDL failed: %s", strings.TrimSpace(stmt))
	}

	channels, models, requests, executions, usages := buildParityFixture()
	for _, c := range channels {
		_, err := db.ExecContext(ctx, `INSERT INTO channels (id, name, type) VALUES ($1, $2, $3)`, c.id, c.name, c.typ)
		require.NoError(t, err)
	}
	for _, m := range models {
		_, err := db.ExecContext(ctx, `INSERT INTO models (model_id, name) VALUES ($1, $2)`, m.id, m.name)
		require.NoError(t, err)
	}
	for _, r := range requests {
		_, err := db.ExecContext(ctx, `INSERT INTO requests (id, model_id) VALUES ($1, $2)`, r.id, r.modelID)
		require.NoError(t, err)
	}
	for _, e := range executions {
		// NULL handling uses explicit flags, matched to the production schema
		// where channel_id and metrics_latency_ms are nullable; a literal 0
		// latency is the zero-latency filter case, never a NULL sentinel.
		var channel any
		if !e.noChannel {
			channel = e.channelID
		}
		var lat any
		if !e.latencyNull {
			lat = e.latencyMS
		}
		var ttft any
		if e.ttftMS > 0 {
			ttft = e.ttftMS
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO request_executions (id, request_id, channel_id, status, metrics_latency_ms, metrics_first_token_latency_ms, stream, created_at, metrics_first_byte_latency_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			e.id, e.requestID, channel, e.status, lat, ttft, e.stream, e.created, e.firstByteMS,
		)
		require.NoError(t, err, "insert execution id=%d", e.id)
	}
	latest := map[int]parityExecution{}
	for _, e := range executions {
		if e.id > latest[e.requestID].id {
			latest[e.requestID] = e
		}
	}
	for _, u := range usages {
		var channel any
		if e, ok := latest[u.requestID]; ok && !e.noChannel {
			channel = e.channelID
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO usage_logs (request_id, completion_tokens, created_at, channel_id) VALUES ($1, $2, $3, $4)`,
			u.requestID, u.completed, dayBase(1), channel,
		)
		require.NoError(t, err, "insert usage for request_id=%d", u.requestID)
	}
}

// collectRows renders every result row as a deterministic string and returns
// them sorted, i.e. a comparable multiset (ordering ignored, duplicates kept).
func collectRows(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)
	var out []string
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		out = append(out, renderRow(raw))
	}
	require.NoError(t, rows.Err())
	slices.Sort(out)
	return out
}

func renderRow(vals []any) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		switch x := v.(type) {
		case nil:
			parts[i] = "NULL"
		case float64:
			parts[i] = strconv.FormatFloat(x, 'g', -1, 64)
		case string:
			parts[i] = x
		case []byte:
			parts[i] = string(x)
		case time.Time:
			parts[i] = x.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		default:
			parts[i] = fmt.Sprintf("%v", x)
		}
	}
	return strings.Join(parts, "\x00")
}

// assertPGParity runs both queries and asserts exact multiset parity: a sorted
// in-Go comparison, then PostgreSQL EXCEPT ALL in both directions, each as a
// single statement with the shared $1 bound once per statement.
func assertPGParity(t *testing.T, db *sql.DB, label, baselineSQL, candidateSQL string, arg any) {
	t.Helper()
	ctx := context.Background()

	baselineRows, err := db.QueryContext(ctx, baselineSQL, arg)
	require.NoError(t, err, "baseline query (%s)", label)
	candidateRows, err := db.QueryContext(ctx, candidateSQL, arg)
	require.NoError(t, err, "candidate query (%s)", label)

	require.True(t,
		reflect.DeepEqual(collectRows(t, baselineRows), collectRows(t, candidateRows)),
		"row multiset mismatch for %s", label)

	// EXCEPT ALL preserves duplicate rows; wrapping each side in a subquery
	// keeps the trailing ORDER BY / LIMIT legal inside the set operand while
	// $1 stays bound once per statement.
	eq := func(lhs, rhs string) {
		t.Helper()
		var n int
		stmt := fmt.Sprintf(
			"SELECT COUNT(*) FROM (SELECT * FROM (%s) AS left_side EXCEPT ALL SELECT * FROM (%s) AS right_side) AS d",
			lhs, rhs)
		require.NoError(t, db.QueryRowContext(ctx, stmt, arg).Scan(&n),
			"EXCEPT ALL ran (%s)", label)
		if n != 0 {
			diffs, derr := db.QueryContext(ctx,
				fmt.Sprintf("SELECT * FROM (%s) AS left_side EXCEPT ALL SELECT * FROM (%s) AS right_side", lhs, rhs), arg)
			require.NoError(t, derr)
			t.Fatalf("%s: %d rows differ (left EXCEPT right): %v", label, n, collectRows(t, diffs))
		}
	}
	eq(baselineSQL, candidateSQL)
	eq(candidateSQL, baselineSQL)
}

// TestThroughputQueryDistinctOnParity_Postgres checks exact row parity
// (ROW_NUMBER baseline vs DISTINCT ON candidate) for every targeted dashboard
// shape against a real PostgreSQL server with an adversarial fixture, plus
// hand-computed expected values that prove the fixture drives the edge cases.
//
// Gated on AXONHUB_TEST_PG_DSN; skips cleanly when unset.
func TestThroughputQueryDistinctOnParity_Postgres(t *testing.T) {
	dsn := os.Getenv("AXONHUB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("AXONHUB_TEST_PG_DSN not set; skipping real-Postgres DISTINCT ON parity test")
	}

	db := newParitySchema(t, dsn)

	// 单日最多 1 组：第一天保留 20 tok/s 的 Beta/m2，排除 10 tok/s 的 Alpha/m1。
	t.Run("build_throughput_by_channel", func(t *testing.T) {
		assertPGParity(t, db, "throughput-by-channel",
			BuildThroughputQuery(true, ThroughputQueryByChannel, 20, ThroughputModeRowNumber),
			BuildThroughputQuery(true, ThroughputQueryByChannel, 20, ThroughputModeDistinctOn),
			paritySince)
	})

	t.Run("build_throughput_by_model", func(t *testing.T) {
		assertPGParity(t, db, "throughput-by-model",
			BuildThroughputQuery(true, ThroughputQueryByModel, 20, ThroughputModeRowNumber),
			BuildThroughputQuery(true, ThroughputQueryByModel, 20, ThroughputModeDistinctOn),
			paritySince)
	})

	t.Run("daily_model_per_day_limit", func(t *testing.T) {
		assertPGParity(t, db, "daily-model",
			BuildDailyModelThroughputQuery("postgres", "UTC", 0, 1, ThroughputModeRowNumber),
			BuildDailyModelThroughputQuery("postgres", "UTC", 0, 1, ThroughputModeDistinctOn),
			paritySince)
	})

	t.Run("daily_channel_per_day_limit", func(t *testing.T) {
		assertPGParity(t, db, "daily-channel",
			BuildDailyChannelThroughputQuery("postgres", "UTC", 0, 1, ThroughputModeRowNumber),
			BuildDailyChannelThroughputQuery("postgres", "UTC", 0, 1, ThroughputModeDistinctOn),
			paritySince)
	})

	t.Run("daily_performance_stats_model", func(t *testing.T) {
		assertPGParity(t, db, "daily-perf-model",
			BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByModel, "$1", ThroughputModeRowNumber),
			BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByModel, "$1", ThroughputModeDistinctOn),
			paritySince)
	})

	t.Run("daily_performance_stats_channel", func(t *testing.T) {
		assertPGParity(t, db, "daily-perf-channel",
			BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByChannel, "$1", ThroughputModeRowNumber),
			BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByChannel, "$1", ThroughputModeDistinctOn),
			paritySince)
	})

	assertParityExpectedRows(t, db, "postgres")

	// Check the inclusive timestamp predicate by execution ID. R16's id 312 is
	// exactly at paritySince; id 313 is one millisecond before it. Request 17
	// also has a later in-window execution (id 322), so its request ID alone
	// cannot prove that the pre-window row was excluded.
	t.Run("window_boundary", func(t *testing.T) {
		ctx := context.Background()
		rows, err := db.QueryContext(ctx, `
			SELECT id FROM request_executions
			WHERE status = 'completed' AND metrics_latency_ms > 0 AND created_at >= $1
			ORDER BY id`, paritySince)
		require.NoError(t, err)
		defer rows.Close()

		var ids []int64
		for rows.Next() {
			var id int64
			require.NoError(t, rows.Scan(&id))
			ids = append(ids, id)
		}
		require.NoError(t, rows.Err())

		require.Contains(t, ids, int64(312), "execution exactly at the window start must be included")
		require.NotContains(t, ids, int64(313), "execution one millisecond before the window start must be excluded")
		require.Contains(t, ids, int64(304), "tie pair's higher id must be present")
		require.NotContains(t, ids, int64(307), "zero-latency execution must be filtered")
		require.NotContains(t, ids, int64(305), "failed execution must be filtered")
		require.NotContains(t, ids, int64(306), "canceled execution must be filtered")
		require.NotContains(t, ids, int64(309), "pre-window execution must be filtered")
		// NULL channel_id is a join concern, not an eligibility one: the
		// latest eligible execution with a NULL channel stays in the set.
		require.Contains(t, ids, int64(329), "NULL-channel execution must pass eligibility")
		// The later NULL-latency execution of R17 must be filtered before
		// latest-per-request selection, so the earlier in-window row wins.
		require.NotContains(t, ids, int64(330), "NULL-latency execution must be filtered")
	})
}

// TestThroughputQueryNonPostgresFallback runs without any database: for
// non-PostgreSQL inputs, ThroughputModeDistinctOn must produce the identical
// ROW_NUMBER SQL, and for the PostgreSQL marker the builders must emit real
// DISTINCT ON SQL with exactly one $1 placeholder (so the combined EXCEPT ALL
// parity statements bind a single argument per statement).
func TestThroughputQueryNonPostgresFallback(t *testing.T) {
	for _, queryType := range []ThroughputQueryType{ThroughputQueryByChannel, ThroughputQueryByModel} {
		rowNumber := BuildThroughputQuery(false, queryType, 10, ThroughputModeRowNumber)
		got := BuildThroughputQuery(false, queryType, 10, ThroughputModeDistinctOn)
		require.Equal(t, rowNumber, got,
			"useDollarPlaceholders=false with DistinctOn must fall back to ROW_NUMBER")
		require.NotContains(t, got, "DISTINCT ON")
		require.Contains(t, got, "?")
		require.Zero(t, strings.Count(got, "$1"))
	}

	for _, queryType := range []ThroughputQueryType{ThroughputQueryByChannel, ThroughputQueryByModel} {
		rowNumber := BuildThroughputQuery(true, queryType, 10, ThroughputModeRowNumber)
		got := BuildThroughputQuery(true, queryType, 10, ThroughputModeDistinctOn)
		require.NotEqual(t, rowNumber, got, "postgres marker with DistinctOn must differ from ROW_NUMBER")
		require.Contains(t, got, "DISTINCT ON (request_id)")
		require.Equal(t, 1, strings.Count(got, "$1"), "exactly one $1 placeholder")
	}

	for _, dialect := range []string{"mysql", "sqlite3", "sqlite"} {
		require.Equal(t,
			BuildDailyModelThroughputQuery(dialect, "UTC", 0, 10, ThroughputModeRowNumber),
			BuildDailyModelThroughputQuery(dialect, "UTC", 0, 10, ThroughputModeDistinctOn),
			"dialect %s", dialect)
		require.Equal(t,
			BuildDailyChannelThroughputQuery(dialect, "UTC", 0, 10, ThroughputModeRowNumber),
			BuildDailyChannelThroughputQuery(dialect, "UTC", 0, 10, ThroughputModeDistinctOn),
			"dialect %s", dialect)
		require.Equal(t,
			BuildDailyPerformanceStatsQuery(dialect, "UTC", 0, DailyThroughputByModel, "?", ThroughputModeRowNumber),
			BuildDailyPerformanceStatsQuery(dialect, "UTC", 0, DailyThroughputByModel, "?", ThroughputModeDistinctOn),
			"dialect %s", dialect)
		require.Equal(t,
			BuildDailyPerformanceStatsQuery(dialect, "UTC", 0, DailyThroughputByChannel, "?", ThroughputModeRowNumber),
			BuildDailyPerformanceStatsQuery(dialect, "UTC", 0, DailyThroughputByChannel, "?", ThroughputModeDistinctOn),
			"dialect %s", dialect)
	}

	require.Contains(t, BuildDailyModelThroughputQuery("postgres", "UTC", 0, 10, ThroughputModeDistinctOn), "DISTINCT ON (request_id)")
	require.Contains(t, BuildDailyChannelThroughputQuery("postgres", "UTC", 0, 10, ThroughputModeDistinctOn), "DISTINCT ON (request_id)")
	require.Contains(t, BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByModel, "$1", ThroughputModeDistinctOn), "DISTINCT ON (se.request_id)")
	require.NotContains(t, BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByChannel, "$1", ThroughputModeDistinctOn), "DISTINCT ON", "channel performance retains all attempts")

	// Exactly one $1 per production query so the parity statements bind one
	// argument.
	require.Equal(t, 1, strings.Count(BuildThroughputQuery(true, ThroughputQueryByChannel, 20, ThroughputModeDistinctOn), "$1"))
	require.Equal(t, 1, strings.Count(BuildThroughputQuery(true, ThroughputQueryByModel, 20, ThroughputModeDistinctOn), "$1"))
	require.Equal(t, 1, strings.Count(BuildDailyModelThroughputQuery("postgres", "UTC", 0, 1, ThroughputModeDistinctOn), "$1"))
	require.Equal(t, 1, strings.Count(BuildDailyChannelThroughputQuery("postgres", "UTC", 0, 1, ThroughputModeDistinctOn), "$1"))
	require.Equal(t, 1, strings.Count(BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByModel, "$1", ThroughputModeDistinctOn), "$1"))
}

// 用现有内存数据库核对共用夹具的独立期望，PostgreSQL SQL 执行仍由上面的实库测试覆盖。
func TestThroughputQueryExpectedRows_SQLite(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:throughput-parity?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	seedParityFixture(t, db, "sqlite3")
	assertParityExpectedRows(t, db, "sqlite3")
}

func assertParityExpectedRows(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	placeholder := "?"
	if dialect == "postgres" {
		placeholder = "$1"
	}
	// 独立手算的预期同时校验新口径、日边界与两个独立样本数。
	for _, test := range []struct {
		name  string
		query string
		want  [][]any
	}{
		{"expected_channel_totals", BuildThroughputQuery(dialect == "postgres", ThroughputQueryByChannel, 20, ThroughputModeRowNumber), [][]any{
			{1, "Alpha", "openai", 1340, 21400, 1, 10},
			{2, "Beta", "anthropic", 1035, 8200, 2, 110},
		}},
		{"expected_model_totals", BuildThroughputQuery(dialect == "postgres", ThroughputQueryByModel, 20, ThroughputModeRowNumber), [][]any{
			{"m1", "model-one", 1230, 20000, 1, 10},
			{"m2", "model-two", 1135, 8700, 2, 110},
		}},
		{"expected_daily_channel", BuildDailyChannelThroughputQuery(dialect, "UTC", 0, 1, ThroughputModeRowNumber), [][]any{
			{"2024-03-01", 2, "Beta", 80, 1, 20},
			{"2024-03-03", 2, "Beta", 100, 1, 200},
		}},
		{"expected_daily_model", BuildDailyModelThroughputQuery(dialect, "UTC", 0, 1, ThroughputModeRowNumber), [][]any{
			{"2024-03-01", "m2", "model-two", 180, 1, 20},
			{"2024-03-03", "m2", "model-two", 100, 1, 200},
		}},
		{"expected_daily_performance_stats", BuildDailyPerformanceStatsQuery(dialect, "UTC", 0, DailyThroughputByModel, placeholder, ThroughputModeRowNumber), [][]any{
			{"2024-03-01", "m1", 180, 15000, nil, 3, 10, 1, 0},
			{"2024-03-01", "m2", 180, 4500, nil, 2, 20, 1, 0},
			{"2024-03-03", "m2", 100, 1000, 500, 1, 200, 1, 1},
		}},
		{"expected_daily_performance_stats_channel", BuildDailyPerformanceStatsQuery(dialect, "UTC", 0, DailyThroughputByChannel, placeholder, ThroughputModeRowNumber), [][]any{
			{"2024-03-01", 1, 290, 18100, nil, 8, 10, 1, 0},
			{"2024-03-01", 2, 80, 4000, nil, 1, 20, 1, 0},
			{"2024-03-02", 2, 0, 5000, 400, 3, nil, 0, 1},
			{"2024-03-03", 2, 100, 1000, 500, 1, 200, 1, 1},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := db.QueryContext(context.Background(), test.query, paritySince)
			require.NoError(t, err)
			want := make([]string, len(test.want))
			for i, row := range test.want {
				want[i] = renderRow(row)
			}
			require.ElementsMatch(t, want, collectRows(t, rows))
		})
	}

}
