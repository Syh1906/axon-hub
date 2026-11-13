import { QueryClient } from '@tanstack/react-query';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./consumption-slices.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText;
const { combineConsumptionSlices, reportingMidnight, loadConsumptionSlices } = await import(
  `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
);
const metric = (overrides = {}) => ({
  ...Object.fromEntries(
    [
      'requestCount',
      'totalTokens',
      'totalCost',
      'unpricedRequestCount',
      'pricedRequestCount',
      'tokenPricedRequestCount',
      'speedSampleCount',
      'cacheRequests',
      'cacheReadTokens',
      'cacheInputTokens',
      'cacheCreationCost',
      'pricedCost',
      'tokenPricedCost',
      'pricedTokens',
      'successCount',
      'outcomeCount',
      'firstTokenSum',
      'firstTokenCount',
      'speedSum',
      'coefficientSamples',
      'coefficientEligible',
      'coefficientTheoretical',
      'coefficientObserved',
    ].map((k) => [k, 0])
  ),
  cacheHitRate: null,
  successRate: null,
  avgFirstTokenMs: null,
  avgTokensPerSecond: null,
  avgCostPerRequest: null,
  avgCostPerMillionTokens: null,
  cacheCoefficient: null,
  ...overrides,
});
const day = (date, metrics) => ({
  revision: date,
  cacheSamples: [],
  performanceSamples: [],
  entries: [{ id: '1', name: '测试', metrics, models: [{ model: 'model-a', metrics: { ...metrics } }] }],
  generatedAt: new Date().toISOString(),
  rangeStart: `${date}T00:00:00.000Z`,
  rangeEnd: new Date(Date.parse(`${date}T00:00:00Z`) + 86400000).toISOString(),
  timezone: 'UTC',
  currencyCode: 'USD',
});

test('合并使用真实样本分子分母并保留未计价和模型', () => {
  const a = day(
    '2026-11-01',
    metric({
      requestCount: 10,
      totalCost: 20,
      pricedCost: 20,
      pricedRequestCount: 10,
      speedSum: 1000,
      speedSampleCount: 10,
      cacheReadTokens: 50,
      cacheInputTokens: 100,
      cacheRequests: 1,
    })
  );
  const b = day(
    '2026-11-02',
    metric({
      requestCount: 2,
      totalCost: 1,
      pricedCost: 1,
      pricedRequestCount: 1,
      unpricedRequestCount: 1,
      speedSum: 10,
      speedSampleCount: 1,
      cacheReadTokens: 20,
      cacheInputTokens: 100,
      cacheRequests: 1,
    })
  );
  const before = JSON.stringify([a, b]);
  const result = combineConsumptionSlices([a, b], { ...a, rangeEnd: b.rangeEnd }, { metric: 'COST', dimension: 'API_KEY_GROUP' });
  const m = result.entries[0].metrics;
  assert.equal(m.requestCount, 12);
  assert.equal(m.avgCostPerRequest, 21 / 11);
  assert.equal(m.avgTokensPerSecond, 1010 / 11);
  assert.equal(m.cacheHitRate, 0.35);
  assert.equal(m.unpricedRequestCount, 1);
  assert.equal(result.entries[0].models.length, 1);
  assert.equal(JSON.stringify([a, b]), before);
});

test('系统自然日覆盖夏令时和半小时时区', () => {
  assert.equal(reportingMidnight('2026-03-08', 'America/New_York'), '2026-03-08T05:00:00.000Z');
  assert.equal(reportingMidnight('2026-03-09', 'America/New_York'), '2026-03-09T04:00:00.000Z');
  assert.equal(reportingMidnight('2026-11-13', 'Asia/Kolkata'), '2026-11-12T18:30:00.000Z');
});

test('月数据覆盖的历史范围直接复用，刷新失败不交付部分总额', async () => {
  const client = new QueryClient();
  const key = ['test-days'];
  let calls = 0;
  const d1 = day('2026-11-01', metric({ totalCost: 1 })),
    d2 = day('2026-11-02', metric({ totalCost: 2 }));
  const metadata = { rangeStart: d1.rangeStart, rangeEnd: d2.rangeEnd, timezone: 'UTC', currencyCode: 'USD' };
  const fetchPage = async (after, known, asOf, verify) => {
    calls++;
    const d = after ? d2 : d1;
    return {
      ...metadata,
      resolvedAt: '2026-11-13T00:00:00Z',
      pending: false,
      days: [{ start: d.rangeStart, revision: d.revision }],
      slices: verify ? [] : [d],
      next: verify || after ? null : d2.rangeStart,
    };
  };
  const filter = { period: 'CUSTOM', startDate: '2026-11-01', endDate: '2026-11-02', dimension: 'API_KEY_GROUP', metric: 'COST' };
  const result = await loadConsumptionSlices(client, key, filter, fetchPage, new AbortController().signal);
  assert.equal(result.entries[0].metrics.totalCost, 3);
  assert.equal(calls, 3);
  const reused = await loadConsumptionSlices(
    client,
    key,
    { ...filter, endDate: '2026-11-01' },
    async () => {
      throw Error('unexpected fetch');
    },
    new AbortController().signal
  );
  assert.equal(reused.entries[0].metrics.totalCost, 1);
  await assert.rejects(
    loadConsumptionSlices(
      client,
      ['failure'],
      filter,
      async (after) => {
        if (after) throw Error('timeout');
        return {
          ...metadata,
          resolvedAt: '2026-11-13T00:00:00Z',
          pending: false,
          days: [{ start: d1.rangeStart, revision: d1.revision }],
          slices: [d1],
          next: d2.rangeStart,
        };
      },
      new AbortController().signal
    ),
    /timeout/
  );
  client.clear();
});

test('缓存系数合并原始样本后再应用整数阈值', () => {
  const m = metric({ coefficientSamples: 20, coefficientEligible: 20, coefficientTheoretical: 10000, coefficientObserved: 5000 });
  const a = day('2026-11-01', m),
    b = day('2026-11-02', m);
  for (const d of [a, b])
    d.cacheSamples = [{ channelID: '1', model: 'model-a', samples: 20, eligible: 20, theoretical: 10000, observed: 5000 }];
  const result = combineConsumptionSlices([a, b], a, { metric: 'CACHE', dimension: 'CHANNEL' });
  assert.equal(result.entries[0].metrics.cacheCoefficient, 3000);
});

test('跨日保留当天没有可见缓存行的窗口样本', () => {
  const a = day('2026-11-01', metric());
  a.entries = [];
  a.cacheSamples = [{ channelID: '1', model: 'model-a', samples: 10, eligible: 10, theoretical: 10000, observed: 0 }];
  const b = day('2026-11-02', metric({ requestCount: 1, totalCost: 2, cacheRequests: 1 }));
  b.cacheSamples = [{ channelID: '1', model: 'model-a', samples: 1, eligible: 1, theoretical: 1000, observed: 1000 }];
  const result = combineConsumptionSlices([a, b], b, { metric: 'CACHE', dimension: 'CHANNEL' });
  assert.equal(result.entries[0].metrics.coefficientSamples, 11);
  assert.equal(result.entries[0].metrics.cacheCoefficient, 272);
  assert.equal(result.entries[0].models[0].metrics.cacheCoefficient, 272);
  assert.equal(result.entries[0].metrics.totalCost, 2);
});

test('版本未变化不重复下载，分页间修正后重新组合完整结果', async () => {
  const client = new QueryClient();
  const key = ['consumptionDays', 'manifest'];
  let revision = 'old',
    checked = 0,
    downloads = 0;
  const filter = { period: 'CUSTOM', startDate: '2026-11-01', endDate: '2026-11-02', dimension: 'API_KEY_GROUP', metric: 'COST' };
  const fetchPage = async (after, known = [], asOf, verify) => {
    if (verify) {
      checked++;
      if (checked === 1) {
        revision = 'new';
        return { pending: true };
      }
      return { pending: false };
    }
    const date = after ? '2026-11-02' : '2026-11-01';
    const d = day(date, metric({ totalCost: revision === 'old' ? 1 : 2 }));
    d.revision = revision;
    const unchanged = known.some((k) => k.start === d.rangeStart && k.revision === d.revision);
    if (!unchanged) downloads++;
    return {
      rangeStart: '2026-11-01T00:00:00.000Z',
      rangeEnd: '2026-11-03T00:00:00.000Z',
      timezone: 'UTC',
      currencyCode: 'USD',
      resolvedAt: '2026-11-13T00:00:00Z',
      pending: false,
      days: [{ start: d.rangeStart, revision }],
      slices: unchanged ? [] : [d],
      next: after ? null : '2026-11-02T00:00:00.000Z',
    };
  };
  const result = await loadConsumptionSlices(client, key, filter, fetchPage, new AbortController().signal);
  assert.equal(result.entries[0].metrics.totalCost, 4);
  assert.equal(downloads, 4);
  await loadConsumptionSlices(client, key, filter, fetchPage, new AbortController().signal, false);
  assert.equal(downloads, 4, '仅验证已有版本，不重传日期载荷');
  client.clear();
});

test('取消待生成读取不启动后续轮询', async () => {
  const client = new QueryClient();
  const controller = new AbortController();
  let calls = 0;
  await assert.rejects(
    loadConsumptionSlices(
      client,
      ['consumptionDays', 'cancel'],
      { period: 'TODAY' },
      async () => {
        calls++;
        controller.abort();
        return { pending: true };
      },
      controller.signal
    )
  );
  assert.equal(calls, 1);
  client.clear();
});

test('历史缓存刷新只发送目标范围版本，核对后保留本地复用', async () => {
  const client = new QueryClient();
  const key = ['consumptionDays', 'range'];
  const days = {};
  for (let i = 0; i < 65; i++) {
    const date = new Date(Date.parse('2026-09-01T00:00:00Z') + i * 86400000).toISOString().slice(0, 10);
    days[date] = { ...day(date, metric({ totalCost: 1 })), generatedAt: '2026-09-01T00:00:00Z' };
  }
  client.setQueryData(key, { timezone: 'UTC', currencyCode: 'USD', days });
  const filter = { period: 'CUSTOM', startDate: '2026-11-01', endDate: '2026-11-02', dimension: 'API_KEY', metric: 'COST' };
  const fetchPage = async (after, known, asOf, verify) => {
    if (verify) return { pending: false };
    assert.deepEqual(
      known.map((d) => d.start.slice(0, 10)),
      ['2026-11-01', '2026-11-02']
    );
    return {
      rangeStart: days['2026-11-01'].rangeStart,
      rangeEnd: days['2026-11-02'].rangeEnd,
      timezone: 'UTC',
      currencyCode: 'USD',
      resolvedAt: new Date().toISOString(),
      pending: false,
      days: known,
      slices: [],
      next: null,
    };
  };
  const loaded = await loadConsumptionSlices(client, key, filter, fetchPage, new AbortController().signal, false);
  assert.equal(loaded.entries[0].metrics.totalCost, 2);
  const reused = await loadConsumptionSlices(
    client,
    key,
    { ...filter, endDate: '2026-11-01' },
    async () => {
      throw Error('不应请求网络');
    },
    new AbortController().signal
  );
  assert.equal(reused.entries[0].metrics.totalCost, 1);
  client.clear();
});

test('全范围核对失败不把未验证日期写入共享缓存', async () => {
  const client = new QueryClient();
  const key = ['consumptionDays', 'unverified'];
  const d = day('2026-11-01', metric({ totalCost: 1 }));
  const filter = { period: 'CUSTOM', startDate: '2026-11-01', endDate: '2026-11-01', dimension: 'API_KEY', metric: 'COST' };
  await assert.rejects(
    loadConsumptionSlices(
      client,
      key,
      filter,
      async (after, known, asOf, verify) =>
        verify
          ? { pending: true }
          : {
              ...d,
              resolvedAt: new Date().toISOString(),
              days: [{ start: d.rangeStart, revision: d.revision }],
              slices: [d],
              next: null,
              pending: false,
            },
      new AbortController().signal
    ),
    /统计数据/
  );
  assert.equal(client.getQueryData(key), undefined);
  client.clear();
});
