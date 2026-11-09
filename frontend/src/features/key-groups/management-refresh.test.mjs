import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { QueryClient, QueryObserver } from '@tanstack/react-query';

function managementAPI(client, onApply) {
  const source = ts.transpileModule(readFileSync(new URL('./data.ts', import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
  }).outputText;
  const dependencies = {
    '@tanstack/react-query': { useQueryClient: () => client },
    '@/gql/graphql': { graphqlRequest: async (_query, variables, headers) => {
      assert.deepEqual(headers, { 'X-Project-ID': 'project-a' });
      onApply(variables);
      return { applyKeyManagement: [{ rowID: 'edit', success: true, keyIDs: [17] }] };
    } },
    '@/stores/projectStore': { useSelectedProjectId: () => 'project-a' },
    '@/lib/utils': {},
    '@/hooks/usePermissions': {},
    '@/features/budgets/data': { budgetFields: '' },
  };
  const exports = {};
  new Function('require', 'exports', source)((name) => {
    assert.ok(name in dependencies, `未隔离的依赖：${name}`);
    return dependencies[name];
  }, exports);
  return exports.useManagementAPI();
}

function fixture(t) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  const subscriptions = [];
  t.after(() => {
    subscriptions.forEach((unsubscribe) => unsubscribe());
    client.clear();
  });
  const observe = (queryKey, queryFn) => {
    const observer = new QueryObserver(client, { queryKey, queryFn, initialData: 'before', staleTime: Infinity });
    subscriptions.push(observer.subscribe(() => {}));
  };
  return { client, observe };
}

test('成员保存后批量额度回读失败会向快捷编辑报错，重读不重复提交', async (t) => {
  const { client, observe } = fixture(t);
  const summaryKey = ['keyBudgets', 'viewer', 'project-a', 'summaries', [17, 18], false];
  let readFails = true;
  let applies = 0;
  observe(['managedGroupKeys', 'viewer', 'project-a', 3, 0], async () => 'members updated');
  observe(summaryKey, async () => {
    if (readFails) throw new Error('summary read failed');
    return 'budget updated';
  });
  const api = managementAPI(client, () => { applies++; });
  const result = await api.apply('same-operation', [{ rowID: 'edit', action: 'updateKey', targetID: 17 }]);
  assert.equal(result[0].success, true);
  await assert.rejects(api.refresh(false, { ownerID: 17, groupRef: 3 }), /summary read failed/);
  assert.equal(client.getQueryData(summaryKey), 'before');
  readFails = false;
  await api.refresh(false, { ownerID: 17, groupRef: 3 });
  assert.equal(client.getQueryData(summaryKey), 'budget updated');
  assert.equal(applies, 1);
});

test('其他密钥或项目的批量额度失败不阻塞当前成员回读', async (t) => {
  const { client, observe } = fixture(t);
  observe(['managedGroupKeys', 'viewer', 'project-a', 3, 0], async () => 'members updated');
  observe(['keyBudgets', 'viewer', 'project-a', 'summaries', [17, 18], true], async () => 'budget updated');
  observe(['keyBudgets', 'viewer', 'project-a', 'summaries', [19], false], async () => { throw new Error('unrelated key'); });
  observe(['keyBudgets', 'viewer', 'project-b', 'summaries', [17], false], async () => { throw new Error('unrelated project'); });
  const api = managementAPI(client, () => {});
  await api.refresh(false, { ownerID: 17, groupRef: 3 });
});

test('单条额度回读仍保留原来的失败检查', async (t) => {
  const { client, observe } = fixture(t);
  observe(['keyBudgets', 'viewer', 'project-a', 'key', 17], async () => { throw new Error('single read failed'); });
  const api = managementAPI(client, () => {});
  await assert.rejects(api.refresh(false, { ownerID: 17, groupRef: 3 }), /single read failed/);
});
