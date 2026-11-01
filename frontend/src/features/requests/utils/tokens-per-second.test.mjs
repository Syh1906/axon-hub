import assert from 'node:assert/strict';
import test from 'node:test';
import { getTokensPerSecondValue, calculateTokensPerSecond } from './tokens-per-second.ts';

test('请求日志使用服务端与排行榜共享的测速结果，旧计时不能代替缺失结果', () => {
  const request = { stream: true, metricsLatencyMs: 2000, metricsFirstTokenLatencyMs: 500,
    usageLogs: { edges: [{ node: { completionTokens: 300 } }] }, outputTokensPerSecond: 150.4 };
  assert.equal(getTokensPerSecondValue(request), 150.4);
  assert.equal(calculateTokensPerSecond(request), '150 tok/s');
  for (const value of [null, undefined, 0, -1, NaN, Infinity])
    assert.equal(getTokensPerSecondValue({ ...request, outputTokensPerSecond: value }), null);
});
