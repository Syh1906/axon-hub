import assert from 'node:assert/strict';
import test from 'node:test';
import { getTokensPerSecondValue } from './tokens-per-second.ts';

test('流式使用生成耗时，非流式标识整次平均，缺失指标不伪造速度', () => {
  const request = {
    stream: true,
    metricsLatencyMs: 2000,
    metricsFirstTokenLatencyMs: 500,
    usageLogs: { edges: [{ node: { completionTokens: 300 } }] },
  };
  assert.equal(getTokensPerSecondValue(request), 200);
  assert.equal(getTokensPerSecondValue({ ...request, stream: false }), 150);
  for (const first of [null, undefined, 2000, 2200, -1])
    assert.equal(getTokensPerSecondValue({ ...request, metricsFirstTokenLatencyMs: first }), null);
  assert.equal(getTokensPerSecondValue({ ...request, metricsLatencyMs: 0 }), null);
  assert.equal(getTokensPerSecondValue({ ...request, usageLogs: null }), null);
});
