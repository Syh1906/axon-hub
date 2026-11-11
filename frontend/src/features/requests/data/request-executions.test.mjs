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
      totalCost: amount, costPriceMultiplier: null, costItems: null,
      promptWriteCachedTokens5m: null, promptWriteCachedTokens1h: null,
    });
    assert.equal(summary.totalCost, amount);
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
    resultExecution: { id: execution.id, responseStatusCode: 200 },
    executions: { edges: [{ node: { ...execution, id: 'loser', responseStatusCode: 500 }, cursor: 'loser' }] },
  });
  assert.equal(result.clientStatusCode, null);
  assert.equal(result.resultExecution.responseStatusCode, 200);
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

function executionQuery(canViewChannels, onRequest, onError) {
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
    './request-log-variables': {},
    './schema': schemas,
  });
  return requests.useRequestExecutions;
}

for (const canViewChannels of [false, true]) {
  for (const includePayload of [false, true, undefined]) {
    test(`真实执行查询保留权限与正文开关（渠道权限=${canViewChannels}，正文=${includePayload}）`, async () => {
      const controller = new AbortController();
      let queryCalls = 0;
      const useRequestExecutions = executionQuery(canViewChannels, async (query, variables, headers, options) => {
        queryCalls++;
        const hasPayload = includePayload !== false;
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
