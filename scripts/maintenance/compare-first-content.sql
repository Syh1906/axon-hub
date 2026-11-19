-- PostgreSQL / psql。按本地已证实的模型和实际渠道执行，窗口采用左闭右开。
-- psql -X -v start_at=... -v end_at=... -v model=... -v effort=... -v channel_a=... -v channel_b=... -f compare-first-content.sql
\set ON_ERROR_STOP on
BEGIN READ ONLY;
SET LOCAL statement_timeout = '10s';

-- 先保留失败和取消的分母。一笔请求可以依次使用两条线路，所以此表按渠道尝试计数。
SELECT e.channel_id, e.status, count(*) AS attempts, count(DISTINCT r.id) AS requests,
       count(*) FILTER (WHERE e.metrics_first_token_latency_ms >= 30000) AS slow_starts,
       count(*) FILTER (WHERE e.metrics_first_token_latency_ms >= 60000) AS minute_starts,
       percentile_disc(0.5) WITHIN GROUP (ORDER BY e.metrics_first_token_latency_ms - 60000)
           FILTER (WHERE r.status = 'completed' AND e.status = 'completed' AND e.metrics_first_token_latency_ms >= 60000)
           AS minute_to_first_content_p50_ms,
       percentile_disc(0.5) WITHIN GROUP (ORDER BY e.metrics_latency_ms - 60000)
           FILTER (WHERE r.status = 'completed' AND e.status = 'completed' AND e.metrics_first_token_latency_ms >= 60000)
           AS minute_to_completion_p50_ms
FROM requests r JOIN request_executions e ON e.request_id = r.id
WHERE r.created_at >= :'start_at'::timestamptz AND r.created_at < :'end_at'::timestamptz
  AND r.stream AND r.source = 'api' AND r.model_id = :'model' AND r.reasoning_effort = :'effort'
  AND r.format IN ('openai/responses', 'openai/responses-ws')
  AND e.format IN ('openai/responses', 'openai/responses-ws')
  AND e.channel_id IN (:'channel_a'::int, :'channel_b'::int)
GROUP BY e.channel_id, e.status ORDER BY 1, 2;

-- 只从一次成功执行、唯一用量行中取可比较的样本。首字来自当次执行。
WITH samples AS MATERIALIZED (
    SELECT r.id, r.created_at, e.channel_id, e.upstream_model_id, e.reasoning_effort, r.project_id,
           e.selection->>'priority' AS priority, e.selection->>'upstream_ref' AS endpoint,
           e.selection->>'client_transport' AS ingress, e.selection->>'upstream_transport' AS egress,
           e.cost_snapshot #>> '{serviceTier,outbound}' AS tier,
           CASE WHEN u.cost_snapshot #>> '{serviceTier,observed}' = 'true'
                THEN NULLIF(u.cost_snapshot #>> '{serviceTier,upstream}', '') END AS reported_tier,
           (e.request_body #>> '{_axon_payload,size}')::bigint AS bytes,
           u.prompt_tokens AS tokens, u.prompt_cached_tokens::float / NULLIF(u.prompt_tokens, 0) AS cache_ratio,
           e.metrics_first_token_latency_ms AS ms
    FROM requests r
    JOIN request_executions e ON e.request_id = r.id
    JOIN usage_logs u ON u.request_id = r.id AND u.channel_id = e.channel_id
    WHERE r.created_at >= :'start_at'::timestamptz AND r.created_at < :'end_at'::timestamptz
      AND r.stream AND r.source = 'api' AND r.status = 'completed' AND e.status = 'completed'
      AND r.format IN ('openai/responses', 'openai/responses-ws')
      AND e.format IN ('openai/responses', 'openai/responses-ws')
      AND r.model_id = :'model' AND r.reasoning_effort = :'effort'
      AND e.channel_id IN (:'channel_a'::int, :'channel_b'::int)
      AND e.metrics_first_token_latency_ms IS NOT NULL AND e.upstream_model_id <> ''
      AND e.cost_snapshot->'serviceTier' IS NOT NULL
      AND u.cost_snapshot #>> '{serviceTier,conflict}' IS DISTINCT FROM 'true'
      AND NULLIF(e.selection->>'upstream_ref', '') IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM request_executions x WHERE x.request_id = r.id AND x.id <> e.id)
      AND NOT EXISTS (SELECT 1 FROM usage_logs x WHERE x.request_id = r.id AND x.id <> u.id)
), candidates AS (
    SELECT a.endpoint AS a_endpoint, b.endpoint AS b_endpoint, a.upstream_model_id, a.reasoning_effort,
           a.ingress, a.egress, a.project_id, a.priority, a.tier, a.reported_tier, a.ms AS a_ms, b.ms AS b_ms,
           row_number() OVER (PARTITION BY a.id, b.endpoint ORDER BY abs(extract(epoch FROM b.created_at - a.created_at)), b.id) AS a_rank,
           row_number() OVER (PARTITION BY b.id, a.endpoint ORDER BY abs(extract(epoch FROM b.created_at - a.created_at)), a.id) AS b_rank
    FROM samples a JOIN samples b ON a.upstream_model_id = b.upstream_model_id AND a.reasoning_effort = b.reasoning_effort
       AND a.ingress = b.ingress AND a.egress = b.egress AND a.tier = b.tier
       AND a.reported_tier IS NOT DISTINCT FROM b.reported_tier
       AND a.project_id = b.project_id AND a.priority = b.priority
       AND b.created_at BETWEEN a.created_at - interval '10 minutes' AND a.created_at + interval '10 minutes'
       AND a.bytes > 0 AND b.bytes BETWEEN a.bytes * 0.8 AND a.bytes * 1.2
       AND a.tokens > 0 AND b.tokens BETWEEN a.tokens * 0.8 AND a.tokens * 1.2
       AND abs(b.cache_ratio - a.cache_ratio) <= 0.1
    WHERE a.channel_id = :'channel_a'::int AND b.channel_id = :'channel_b'::int AND a.channel_id <> b.channel_id
)
-- 两侧均缺少上游档位回报时单列。渠道内的不同端点/账号来源分别比较。
SELECT a_endpoint, b_endpoint, upstream_model_id, reasoning_effort, ingress, egress, project_id, priority, tier, reported_tier,
       count(*) AS matched_pairs,
       percentile_disc(0.5) WITHIN GROUP (ORDER BY a_ms) AS a_p50_ms,
       percentile_disc(0.5) WITHIN GROUP (ORDER BY b_ms) AS b_p50_ms,
       percentile_disc(0.95) WITHIN GROUP (ORDER BY a_ms) AS a_p95_ms,
       percentile_disc(0.95) WITHIN GROUP (ORDER BY b_ms) AS b_p95_ms,
       percentile_disc(0.5) WITHIN GROUP (ORDER BY a_ms - b_ms) AS paired_difference_p50_ms
FROM candidates WHERE a_rank = 1 AND b_rank = 1
GROUP BY a_endpoint, b_endpoint, upstream_model_id, reasoning_effort, ingress, egress, project_id, priority, tier, reported_tier
ORDER BY a_endpoint, b_endpoint, upstream_model_id, reasoning_effort, ingress, egress, project_id, priority, tier, reported_tier;
ROLLBACK;
