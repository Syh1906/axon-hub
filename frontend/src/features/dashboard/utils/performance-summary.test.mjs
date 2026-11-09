import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./performance-summary.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText;
const { summarizePerformance } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

test('跨日平均各自按有效首字和速度样本数加权', () => {
  const samples = [
    { requestCount: 100, throughput: 100, speedSampleCount: 1, ttftMs: 1000, firstTokenSampleCount: 1 },
    { requestCount: 9, throughput: 10, speedSampleCount: 9, ttftMs: 100, firstTokenSampleCount: 9 },
    { requestCount: 10, throughput: null, speedSampleCount: 0, ttftMs: 0, firstTokenSampleCount: 10 },
  ];
  assert.deepEqual(summarizePerformance(samples), { avgThroughput: 19, avgTtft: 95 });
});

test('缺失或没有有效分母的指标不稀释其他日期的平均值', () => {
  const samples = [
    { throughput: 30, speedSampleCount: 2, ttftMs: 100, firstTokenSampleCount: 1 },
    { throughput: null, speedSampleCount: 8, ttftMs: null, firstTokenSampleCount: 9 },
    { throughput: 900, speedSampleCount: 0, ttftMs: 9000, firstTokenSampleCount: 0 },
  ];
  assert.deepEqual(summarizePerformance(samples), { avgThroughput: 30, avgTtft: 100 });
});

test('真实零毫秒保留为零，没有样本返回空值', () => {
  assert.deepEqual(summarizePerformance([
    { throughput: null, speedSampleCount: 0, ttftMs: 0, firstTokenSampleCount: 1 },
  ]), { avgThroughput: null, avgTtft: 0 });
  assert.deepEqual(summarizePerformance([]), { avgThroughput: null, avgTtft: null });
});
