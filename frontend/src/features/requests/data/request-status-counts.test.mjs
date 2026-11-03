import { z } from 'zod';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { requestStatusCountVariables } from './request-log-variables.ts';

const source = ts.transpileModule(readFileSync(new URL('./request-status-counts.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
}).outputText;

// 执行真实 hook 并取回其查询配置，只隔离当前测试不需要的 React 与登录上下文。
function queryOptions(onRequest) {
  const exports = {};
  const dependencies = {
    zod: { z },
    '@tanstack/react-query': { useQuery: (options) => options },
    '@/gql/graphql': {
      graphqlRequest: async () => {
        onRequest();
        return { requestLogStatusCounts: [{ status: 'failed', count: 2 }] };
      },
    },
    '@/stores/projectStore': { useSelectedProjectId: () => null },
    '@/hooks/usePermissions': { usePermissions: () => ({ hasScope: () => true }) },
    './request-log-variables': { requestStatusCountVariables },
    './schema': { requestStatusSchema: z.string() },
  };
  new Function('require', 'exports', source)((name) => {
    assert.ok(name in dependencies, `未隔离的依赖：${name}`);
    return dependencies[name];
  }, exports);
  return (open) => exports.useRequestLogStatusCounts({}, open, false);
}

for (const productionFocus of [false, true]) {
  test(`关闭周期刷新时，焦点与网络恢复不额外统计（生产焦点默认：${productionFocus}）`, async () => {
    let calls = 0;
    const options = queryOptions(() => calls++);
    const client = new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: productionFocus } } });
    const observer = new QueryObserver(client, options(true));
    const unsubscribe = observer.subscribe(() => {});
    try {
      await observer.refetch();
      assert.equal(calls, 1);
      client.getQueryCache().onFocus();
      await new Promise(setImmediate);
      assert.equal(calls, 1);
      client.getQueryCache().onOnline();
      await new Promise(setImmediate);
      assert.equal(calls, 1);
      // 显式刷新仍能取得新计数。
      await observer.refetch();
      assert.equal(calls, 2);
    } finally {
      unsubscribe();
      client.clear();
    }
  });
}

test('关闭菜单不统计，重新打开刷新计数', async () => {
  let calls = 0;
  const options = queryOptions(() => calls++);
  const client = new QueryClient();
  const observer = new QueryObserver(client, options(false));
  const unsubscribe = observer.subscribe(() => {});
  try {
    client.getQueryCache().onFocus();
    client.getQueryCache().onOnline();
    await new Promise(setImmediate);
    assert.equal(calls, 0);
    observer.setOptions(options(true));
    await observer.refetch({ cancelRefetch: false });
    assert.equal(calls, 1);
    observer.setOptions(options(false));
    client.getQueryCache().onFocus();
    client.getQueryCache().onOnline();
    await new Promise(setImmediate);
    assert.equal(calls, 1);
    observer.setOptions(options(true));
    await observer.refetch({ cancelRefetch: false });
    assert.equal(calls, 2);
  } finally {
    unsubscribe();
    client.clear();
  }
});
