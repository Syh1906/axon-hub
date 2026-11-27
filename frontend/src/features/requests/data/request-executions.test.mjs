import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { z } from 'zod';

function loadModule(path, dependencies) {
  const source = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
  }).outputText;
  const exports = {};
  new Function('require', 'exports', source)((name) => {
    assert.ok(name in dependencies, `未隔离的依赖：${name}`);
    return dependencies[name];
  }, exports);
  return exports;
}

const pagination = loadModule('../../../gql/pagination.ts', { zod: { z } });
const pricingEvidence = loadModule('../../../components/cost-pricing-evidence.ts', { zod: { z } });
const usageSchemas = loadModule('./usage-logs-schema.ts', {
  zod: { z },
  '@/gql/pagination': pagination,
  '@/features/channels/data': { channelSchema: z.object({}) },
  '@/components/cost-pricing-evidence': pricingEvidence,
});
// 执行真实执行记录与分页校验；本用例不涉及关联实体的内部字段。
const schemas = loadModule('./schema.ts', {
  zod: { z },
  '@/gql/pagination': pagination,
  '@/features/apikeys/data/schema': { apiKeySchema: z.object({}) },
  '@/features/channels/data': { channelSchema: z.object({}) },
  './usage-logs-schema': usageSchemas,
});

const execution = {
  id: 'execution-1',
  createdAt: '2026-11-07T14:00:00Z',
  updatedAt: '2026-11-07T14:00:01Z',
  modelID: 'test-model',
  errorMessage: null,
  status: 'completed',
};

test('请求汇总保留多笔费用、缺价与归档的未知细分', () => {
  for (const amount of [0.05, 0, null]) {
    const summary = usageSchemas.requestUsageSummarySchema.parse({
      usageRecords: 2, archivedRecords: 1, unpricedRecords: amount === null ? 2 : 0,
      promptTokens: 1500, completionTokens: 150, totalTokens: 1650,
      totalCost: amount, costPriceMultiplier: null, costMultiplierMixed: false, costItems: null,
      channelCost: null, channelCostMultiplier: null, channelCostMultiplierMixed: false,
      promptWriteCachedTokens5m: null, promptWriteCachedTokens1h: null,
    });
    assert.equal(summary.totalCost, amount);
    assert.equal(summary.channelCost, null);
    assert.equal(summary.costMultiplierMixed, false);
    assert.equal(summary.promptWriteCachedTokens5m, null);
    assert.equal(summary.usageRecords, 2);
  }
});
function connection(nodes = [execution]) {
  return {
    edges: nodes.map((node) => ({ node, cursor: node.id })),
    pageInfo: { hasNextPage: false, hasPreviousPage: false },
    totalCount: nodes.length,
  };
}

test('非空轻量执行记录不需要请求正文和响应正文', () => {
  const result = schemas.requestExecutionConnectionSchema.parse(connection());
  assert.equal(result.edges[0].node.id, execution.id);
  assert.equal(Object.hasOwn(result.edges[0].node, 'requestBody'), false);
  assert.equal(Object.hasOwn(result.edges[0].node, 'responseBody'), false);
});

test('请求结果保留采用执行的状态码，允许缺少客户端交付状态', () => {
  const result = schemas.requestSchema.parse({
    id: 'request-1', createdAt: execution.createdAt, updatedAt: execution.updatedAt,
    source: 'api', modelID: 'test-model', status: 'completed', stream: true,
    clientStatusCode: null,
    resultExecution: { id: execution.id, responseStatusCode: 200, outboundServiceTier: 'fast' },
    executions: { edges: [{ node: { ...execution, id: 'loser', responseStatusCode: 500 }, cursor: 'loser' }] },
  });
  assert.equal(result.clientStatusCode, null);
  assert.equal(result.resultExecution.responseStatusCode, 200);
  assert.equal(result.resultExecution.outboundServiceTier, 'fast');
  assert.equal(result.executions.edges[0].node.responseStatusCode, 500);
});

test('完整详情保留 JSON、null 正文和响应分片', () => {
  const bodies = { requestBody: { model: 'test-model', messages: [] }, responseBody: null, responseChunks: [{ delta: 'ok' }] };
  const result = schemas.requestExecutionConnectionSchema.parse(connection([{ ...execution, ...bodies }]));
  for (const [key, value] of Object.entries(bodies)) assert.deepEqual(result.edges[0].node[key], value);
  const response = { choices: [{ message: { content: 'ok' } }] };
  assert.deepEqual(schemas.requestExecutionSchema.parse({ ...execution, requestBody: null, responseBody: response }).responseBody, response);
});

test('空执行列表仍然通过，缺失必要字段或状态错误仍然拒绝', () => {
  assert.equal(schemas.requestExecutionConnectionSchema.parse(connection([])).totalCount, 0);
  for (const field of ['id', 'modelID', 'status', 'errorMessage']) {
    const invalid = { ...execution };
    delete invalid[field];
    assert.equal(schemas.requestExecutionConnectionSchema.safeParse(connection([invalid])).success, false, field);
  }
  assert.equal(schemas.requestExecutionSchema.safeParse({ ...execution, status: 'unknown' }).success, false);
});

function requestQueries(canViewChannels, onRequest, onError) {
  const requests = loadModule('./requests.ts', {
    '@tanstack/react-query': { useQuery: (options) => options },
    '@/gql/graphql': { graphqlRequest: onRequest },
    'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
    '@/stores/projectStore': { useSelectedProjectId: () => 'selected-project' },
    '@/stores/authStore': { useAuthStore: (select) => select({ auth: { user: { id: 'test-user' } } }) },
    '@/hooks/use-cursor-list': {},
    '@/hooks/use-error-handler': { useErrorHandler: () => ({ handleError: onError }) },
    '@/hooks/usePermissions': {},
    '@/components/cost-pricing-evidence': { COST_PRICING_EVIDENCE_FIELDS: '' },
    '../../../hooks/useRequestPermissions': { useRequestPermissions: () => ({ canViewChannels }) },
    './request-list-cache': {},
    './request-log-variables': loadModule('./request-log-variables.ts', {}),
    './schema': schemas,
  });
  return requests;
}

test('页码总数按当前筛选独立读取，滚动模式关闭统计', async () => {
  let calls = 0;
  const { useRequestLogTotal } = requestQueries(true, async (query, variables, headers) => {
    calls++;
    assert.match(query, /requestLogs\(first: 1, where: \$where, filter: \$filter\)\s*\{\s*totalCount\s*\}/);
    assert.doesNotMatch(query, /\bedges\b/);
    assert.deepEqual(variables.where, { projectID: 'selected-project', statusIn: ['failed'], clientSessionID: 'session' });
    assert.deepEqual(variables.filter.channelTagsIn, ['target']);
    assert.equal(variables.filter.collapseContinuations, false);
    assert.equal(variables.filter.offset, undefined);
    assert.equal(headers['X-Project-ID'], 'selected-project');
    return { requestLogs: { totalCount: 41 } };
  });
  const where = { statusIn: ['failed'], clientSessionID: 'session', channelTagsIn: ['target'] };
  assert.equal(useRequestLogTotal(where, false).enabled, false);
  const query = useRequestLogTotal(where, true);
  assert.equal(await query.queryFn({ signal: new AbortController().signal }), 41);
  assert.equal(calls, 1);
  assert.notDeepEqual(query.queryKey, useRequestLogTotal({ ...where, statusIn: ['completed'] }, true).queryKey);
});

test('总数统计成功或失败后按一分钟节流，手动重试立即生效', (t) => {
  const now = 1_000_000;
  t.mock.timers.enable({ apis: ['Date'], now });
  const { refreshRequestLogTotal } = requestQueries(true);
  let calls = 0;
  const total = { dataUpdatedAt: 0, errorUpdatedAt: now, refetch: (options) => {
    assert.equal(options.cancelRefetch, false);
    calls++;
  } };
  t.mock.timers.tick(3_000);
  refreshRequestLogTotal(total);
  assert.equal(calls, 0);
  refreshRequestLogTotal(total, true);
  assert.equal(calls, 1);
  t.mock.timers.tick(57_000);
  refreshRequestLogTotal(total);
  assert.equal(calls, 2);
  total.dataUpdatedAt = Date.now();
  t.mock.timers.tick(59_999);
  refreshRequestLogTotal(total);
  assert.equal(calls, 2);
  t.mock.timers.tick(1);
  refreshRequestLogTotal(total);
  assert.equal(calls, 3);
});

test('抽屉跨页保留会话、项目与时间方向', async () => {
  const { fetchAdjacentRequestPage } = requestQueries(false, async (_query, variables, headers) => {
    assert.equal(variables.orderBy.direction, 'ASC');
    assert.equal(variables.where.clientSessionID, 'sample');
    assert.equal(variables.where.projectID, 'project');
    assert.equal(headers['X-Project-ID'], 'project');
    assert.equal(variables.filter.collapseContinuations, false);
    if (variables.after) assert.deepEqual([variables.after, variables.first], ['end', 50]);
    else assert.deepEqual([variables.before, variables.last], ['start', 50]);
    return { requests: connection([]) };
  });
  for (const [direction, cursor] of [['after', 'end'], ['before', 'start']]) {
    await fetchAdjacentRequestPage({ direction, cursor, pageSize: 50, where: { clientSessionID: 'sample' }, permissions: {}, projectId: 'project' });
  }
});

for (const canViewChannels of [false, true]) {
  for (const includePayload of [false, true, undefined]) {
    test(`真实执行查询保留权限与正文开关（渠道权限=${canViewChannels}，正文=${includePayload}）`, async () => {
      const controller = new AbortController();
      let queryCalls = 0;
      const { useRequestExecutions } = requestQueries(canViewChannels, async (query, variables, headers, options) => {
        queryCalls++;
        const hasPayload = includePayload !== false;
        assert.match(query, /reasoningEvidence\s*\{\s*requested\s+reported\s*\}/);
        for (const field of ['requestHeaders', 'responseHeaders', 'requestBody', 'responseBody', 'responseChunks']) {
          assert.equal(new RegExp(`\\b${field}\\b`).test(query), hasPayload, field);
        }
        assert.equal(/\bchannel\s*\{/.test(query), canViewChannels);
        assert.equal(/\bselection\s*\{/.test(query), canViewChannels);
        assert.deepEqual(variables, { requestID: 'request-1', first: 20 });
        assert.deepEqual(headers, { 'X-Project-ID': 'explicit-project' });
        assert.equal(options.signal, controller.signal);
        return { node: { executions: connection([hasPayload ? { ...execution, requestBody: {}, responseBody: null } : execution]) } };
      }, (error) => { throw error; });
      const options = useRequestExecutions('request-1', { first: 20 }, { includePayload, projectId: 'explicit-project' });
      assert.equal(options.enabled, true);
      assert.equal(options.queryKey.at(-1), includePayload !== false);
      const result = await options.queryFn({ signal: controller.signal });
      assert.equal(result.edges[0].node.id, execution.id);
      assert.equal(queryCalls, 1);
    });
  }
}
