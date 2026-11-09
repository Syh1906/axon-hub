package biz

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
)

// The fixture shadows every table the model query reads. It can exercise the
// PostgreSQL projection in a read-only transaction without creating a schema.
const consumptionProjectionFixture = `WITH
fixture_requests(id, project_id, channel_id, api_key_id, model_id, status, created_at, failure, result_execution_id) AS (
 VALUES
 (1,1,11,1,'fixture','completed',TIMESTAMPTZ '2026-11-09T08:00:00Z',NULL::jsonb,101),
 (2,1,11,1,'fixture','completed',TIMESTAMPTZ '2026-11-09T08:00:00Z',NULL::jsonb,NULL),
 (3,1,11,1,'fixture','failed',TIMESTAMPTZ '2026-11-09T08:00:00Z',NULL::jsonb,301),
 (4,1,11,1,'fixture','completed',TIMESTAMPTZ '2026-11-09T08:00:00Z',NULL::jsonb,401),
 (5,1,11,1,'fixture','completed',TIMESTAMPTZ '2026-11-09T08:00:00Z',NULL::jsonb,501),
 (6,1,11,1,'fixture','completed',TIMESTAMPTZ '2026-11-09T08:00:00Z',NULL::jsonb,601)
),
requests AS (SELECT fixture_requests.*, 'api'::text AS source FROM fixture_requests),
request_executions(id, request_id, project_id, channel_id, model_id, status, error_message,
 response_status_code, metrics_latency_ms, metrics_first_byte_latency_ms, metrics_first_byte_recorded,
 metrics_first_token_latency_ms, stream, format) AS (
 VALUES
 (101,1,1,11,'fixture','completed','',200,1200,200,TRUE,700,TRUE,'openai/chat_completions'),
 (102,1,1,12,'fixture','failed','provider failed',500,2200,200,TRUE,900,TRUE,'openai/chat_completions'),
 (200,2,1,11,'fixture','failed','provider failed',500,1200,200,TRUE,100,TRUE,'openai/chat_completions'),
 (201,2,1,11,'fixture','completed','',200,2200,200,TRUE,200,TRUE,'openai/chat_completions'),
 (202,2,1,12,'fixture','failed','provider failed',500,1200,200,TRUE,900,TRUE,'openai/chat_completions'),
 (301,3,1,11,'fixture','failed','provider failed',500,1000,NULL,FALSE,NULL,TRUE,'openai/chat_completions'),
 (401,4,1,11,'fixture','completed','',200,500,200,TRUE,0,FALSE,'openai/chat_completions'),
 (501,5,1,11,'fixture','completed','',200,100,0,TRUE,0,TRUE,'openai/responses'),
 (601,6,1,11,'fixture','completed','',200,1200,200,TRUE,300,TRUE,'openai/chat_completions')
),
fixture_usage(id,request_id,project_id,channel_id,prompt_tokens,completion_tokens,total_tokens,
 prompt_cached_tokens,prompt_write_cached_tokens,total_cost,cost_snapshot,completion_audio_tokens) AS (
 VALUES
 (1001,1,1,11,1000,100,1100,0,0,0.01::double precision,NULL::jsonb,0),
 (1002,1,1,12,1000,999,1999,0,0,5.0::double precision,NULL::jsonb,0),
 (2001,2,1,11,1000,200,1200,0,0,0.02::double precision,NULL::jsonb,0),
 (3001,3,1,11,1000,50,1050,0,0,0.03::double precision,NULL::jsonb,0),
 (4001,4,1,11,1000,100,1100,0,0,0.04::double precision,NULL::jsonb,0),
 (5001,5,1,11,10,10,20,0,0,0.05::double precision,'{"images":[{}]}'::jsonb,0)
),
usage_logs AS (SELECT fixture_usage.*, 1 AS api_key_id, 'fixture'::text AS model_id,
 TIMESTAMPTZ '2026-11-09T08:00:00Z' AS created_at, 0 AS completion_reasoning_tokens,
 NULL::jsonb AS cost_items, ''::text AS cost_price_reference_id,
 0 AS prompt_write_cached_tokens_5m, 0 AS prompt_write_cached_tokens_1h,
 NULL::bigint AS prompt_text_tokens, NULL::bigint AS prompt_image_tokens,
 NULL::bigint AS prompt_text_cached_tokens, NULL::bigint AS prompt_image_cached_tokens FROM fixture_usage),
budget_charges(execution_id,request_id,project_id,usage_log_id) AS (
 VALUES (101,1,1,1001),(102,1,1,1002)
),
api_keys(id,project_id,group_ref,name) AS (VALUES (1,1,1,'fixture')),
api_key_groups(id,project_id,name) AS (VALUES (1,1,'fixture')),
channels(id,name,deleted_at) AS (VALUES (11,'winner',0),(12,'backup',0)),
usage_archives AS (
 SELECT id, 'usage'::text AS kind, id AS source_id, request_id, project_id, api_key_id, channel_id, model_id,
 model_id AS request_model_id, created_at AS occurred_at, 'completed'::text AS request_status, 'api'::text AS request_source,
 prompt_tokens, completion_tokens, total_tokens, prompt_cached_tokens, prompt_write_cached_tokens,
 completion_reasoning_tokens, total_cost, 1 AS billing_version, TRUE AS pricing_complete,
 3 AS speed_version, 0 AS speed_count, 0::double precision AS speed_sum,
 0 AS first_token_count, 0::double precision AS first_token_sum, 0 AS success_count, 0 AS outcome_count,
 0 AS cache_requests, 0 AS cache_input, 0::double precision AS cache_creation_cost FROM usage_logs WHERE FALSE
)
`

func TestConsumptionProjection_Postgres(t *testing.T) {
	dsn := os.Getenv("AXONHUB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未配置 AXONHUB_TEST_PG_DSN")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	db, err := stdsql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	tx, err := db.BeginTx(ctx, &stdsql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	start := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for name, filter := range map[string]ConsumptionFilter{
		"wide":         {Dimension: ConsumptionDimensionModel, Metric: ConsumptionMetricCost},
		"model search": {Dimension: ConsumptionDimensionModel, Metric: ConsumptionMetricCost, Search: lo.ToPtr("fixture")},
		"key group":    {Dimension: ConsumptionDimensionAPIKeyGroup, Metric: ConsumptionMetricCost, GroupRef: lo.ToPtr(1)},
	} {
		t.Run(name, func(t *testing.T) {
			driver := &consumptionQueryCapture{}
			svc := &ConsumptionService{client: ent.NewClient(ent.Driver(driver))}
			_, err := svc.queryGroupsSnapshot(authz.WithTestBypass(ctx), 1,
				filter, &start, start.Add(24*time.Hour))
			require.ErrorIs(t, err, errCaptureConsumptionQuery)
			var data []byte
			err = tx.QueryRowContext(ctx, consumptionProjectionFixture+"SELECT row_to_json(result) FROM ("+driver.query+") result", driver.args...).Scan(&data)
			require.NoError(t, err)
			var row consumptionGroup
			require.NoError(t, json.Unmarshal(data, &row))
			require.Equal(t, "fixture", row.Model)
			require.Equal(t, 6, row.Requests, "missing usage must not remove a request")
			require.EqualValues(t, 6469, row.Tokens)
			require.InDelta(t, 5.15, row.Cost, 1e-9)
			require.Equal(t, 1, row.UnpricedRequests)
			require.EqualValues(t, 5, row.SuccessCount)
			require.EqualValues(t, 6, row.OutcomeCount)
			require.EqualValues(t, 4, row.FirstTokenCount, "zero milliseconds is measured; absent and nonstream samples are unknown")
			require.EqualValues(t, 1200, row.FirstTokenSum)
			require.EqualValues(t, 2, row.SpeedCount, "winner, legacy unique usage, missing timing, images and missing usage stay distinct")
			require.InDelta(t, 200, row.SpeedSum, 1e-9)
		})
	}
}
