import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { QueryClient, QueryObserver } from '@tanstack/react-query';

const source = ts.transpileModule(readFileSync(new URL('./channel-identity-cache.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText;
const { invalidateChannelIdentityQueries } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

function readHook(path, name, dependencies) {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8');
  const ast = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true);
  const declaration = ast.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === name);
  const js = ts.transpile(declaration.getText(ast).replace(/^export /, ''), { target: ts.ScriptTarget.ES2022 });
  return new Function(...Object.keys(dependencies), js + '\nreturn ' + name)(...Object.values(dependencies));
}

test('编辑渠道保存标签后，只有观察中的普通和分页渠道榜重新读取', async () => {
  const client = new QueryClient();
  const shared = {
    useQueryClient: () => client,
    useAuthStore: (select) => select({ auth: { user: { id: 'viewer' } } }),
    useTranslation: () => ({}),
  };
  const daily = readHook('../../consumption/data/consumption.ts', 'useConsumption', {
    ...shared, useQuery: (options) => options, useCallback: (fn) => fn, consumptionQueryInput: (input) => input,
  });
  const ranked = readHook('../../consumption/data/ranked-pages.ts', 'useRankedPages', {
    ...shared, useInfiniteQuery: (options) => options, useMemo: (fn) => fn(),
  });
  const save = readHook('./channels.ts', 'useUpdateChannelSettings', {
    ...shared, useMutation: (options) => options, useErrorHandler: () => ({}), invalidateChannelIdentityQueries,
  })();
  const subscriptions = [];
  const reads = new Map();
  try {
    for (const [name, hook] of [['daily', daily], ['ranked', ranked]]) {
      for (const [view, dimension] of [['leaderboard', 'CHANNEL'], ['leaderboard', 'MODEL'], ['usage', 'CHANNEL']]) {
        const { queryKey, meta } = hook({ period: 'TODAY', dimension }, view, null, null, true);
        const label = [name, view, dimension].join('/');
        const options = {
          queryKey, meta, staleTime: Infinity,
          queryFn: () => {
            const count = (reads.get(label) ?? 0) + 1;
            reads.set(label, count);
            return count;
          },
        };
        await client.fetchQuery(options);
        subscriptions.push(new QueryObserver(client, options).subscribe(() => {}));
      }
    }
    save.onSuccess({ id: 'one' }, { input: { tags: ['B', 'A'] } });
    assert.deepEqual(Object.fromEntries(reads), {
      'daily/leaderboard/CHANNEL': 2, 'daily/leaderboard/MODEL': 1, 'daily/usage/CHANNEL': 1,
      'ranked/leaderboard/CHANNEL': 2, 'ranked/leaderboard/MODEL': 1, 'ranked/usage/CHANNEL': 1,
    });
  } finally {
    subscriptions.forEach((unsubscribe) => unsubscribe());
    client.clear();
  }
});
