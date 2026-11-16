import { QueryClient } from '@tanstack/react-query';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import ts from 'typescript';

const source = ts
  .transpileModule(readFileSync(join(import.meta.dirname, 'channel-usage.ts'), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
  })
  .outputText.replaceAll("'@tanstack/react-query'", JSON.stringify(import.meta.resolve('@tanstack/react-query')))
  .replaceAll("'zod'", JSON.stringify(import.meta.resolve('zod')))
  .replace(
    "import { channelTodayUsageSchema } from './schema';",
    'const channelTodayUsageSchema = z.object({ channelId: z.string(), totalCost: z.number(), models: z.array(z.object({ model: z.string(), totalCost: z.number() })) });'
  )
  .replace(
    "import { graphqlRequest } from '@/gql/graphql';",
    'let request; export const setRequest = (value) => { request = value; }; const graphqlRequest = (...args) => request(...args);'
  );
const { refreshChannelTodayUsage, channelTodayUsageQueryOptions, setRequest } = await import(
  `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
);

test('刷新单渠道只请求该渠道，同时替换所有列表中对应汇总与模型', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    const a = { channelId: 'a', totalCost: 1, models: [{ model: 'm', totalCost: 1 }] };
    const b = { channelId: 'b', totalCost: 2, models: [] };
    const updated = { ...a, totalCost: 4, models: [{ model: 'm', totalCost: 4 }] };
    client.setQueryData(['channel-today-usage', ['a', 'b']], [a, b]);
    setRequest(async (_query, variables) => {
      assert.deepEqual(variables.channelIds, ['a']);
      return { channelTodayUsage: [updated] };
    });
    await refreshChannelTodayUsage(client, ['a']);
    assert.deepEqual(client.getQueryData(['channel-today-usage', ['a', 'b']]), [updated, b]);
    const options = channelTodayUsageQueryOptions(['a']);
    assert.equal(options.refetchInterval, false);
    assert.equal(options.refetchOnMount, 'always');
    assert.equal(options.refetchOnWindowFocus, false);
  } finally {
    client.clear();
  }
});

test('今日用量刷新失败保留同一份汇总和模型，不以空结果覆盖', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    const previous = [{ channelId: 'a', totalCost: 1, models: [{ model: 'm', totalCost: 1 }] }];
    client.setQueryData(['channel-today-usage', ['a']], previous);
    setRequest(async () => {
      throw new Error('read failed');
    });
    await assert.rejects(refreshChannelTodayUsage(client, ['a']), /read failed/);
    assert.deepEqual(client.getQueryData(['channel-today-usage', ['a']]), previous);
  } finally {
    client.clear();
  }
});

test('手动刷新不被较早开始的列表请求覆盖', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    const a = { channelId: 'a', totalCost: 1, models: [] };
    const b = { channelId: 'b', totalCost: 2, models: [] };
    const updated = { ...a, totalCost: 4 };
    client.setQueryData(['channel-today-usage', ['a', 'b']], [a, b]);
    let finishEarlier;
    setRequest((_query, variables) =>
      variables.channelIds.length === 2
        ? new Promise((resolve) => {
            finishEarlier = resolve;
          })
        : Promise.resolve({ channelTodayUsage: [updated] })
    );
    const earlier = client.fetchQuery({ ...channelTodayUsageQueryOptions(['a', 'b']), staleTime: 0 }).catch(() => {});
    await refreshChannelTodayUsage(client, ['a']);
    finishEarlier({ channelTodayUsage: [a, b] });
    await earlier;
    assert.deepEqual(client.getQueryData(['channel-today-usage', ['a', 'b']]), [updated, b]);
  } finally {
    client.clear();
  }
});

test('首次列表读取中刷新仍补齐同批其他渠道', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    const rows = [
      { channelId: 'a', totalCost: 4, models: [] },
      { channelId: 'b', totalCost: 2, models: [] },
    ];
    let first = true;
    let finishEarlier;
    setRequest((_query, variables) => {
      assert.deepEqual(variables.channelIds, ['a', 'b']);
      if (first) {
        first = false;
        return new Promise((resolve) => {
          finishEarlier = resolve;
        });
      }
      return Promise.resolve({ channelTodayUsage: rows });
    });
    const earlier = client.fetchQuery(channelTodayUsageQueryOptions(['a', 'b'])).catch(() => {});
    await refreshChannelTodayUsage(client, ['a']);
    finishEarlier({ channelTodayUsage: rows.map((row) => ({ ...row, totalCost: 1 })) });
    await earlier;
    assert.deepEqual(client.getQueryData(['channel-today-usage', ['a', 'b']]), rows);
  } finally {
    client.clear();
  }
});

test('首次加载中刷新非首个渠道失败后，原列表可显示错误并重试', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    let first = true;
    let finishEarlier;
    setRequest((_query, variables) => {
      assert.deepEqual(variables.channelIds, ['a', 'b']);
      if (first) {
        first = false;
        return new Promise((resolve) => {
          finishEarlier = resolve;
        });
      }
      throw new Error('refresh failed');
    });
    const key = ['channel-today-usage', ['a', 'b']];
    const earlier = client.fetchQuery(channelTodayUsageQueryOptions(['a', 'b'])).catch(() => {});
    await assert.rejects(refreshChannelTodayUsage(client, ['b']), /refresh failed/);
    finishEarlier({ channelTodayUsage: [] });
    await earlier;
    assert.equal(client.getQueryState(key).status, 'error');
    assert.equal(client.getQueryState(key).fetchStatus, 'idle');
    const rows = [
      { channelId: 'a', totalCost: 1, models: [] },
      { channelId: 'b', totalCost: 4, models: [] },
    ];
    setRequest(async () => ({ channelTodayUsage: rows }));
    await client.fetchQuery(channelTodayUsageQueryOptions(['a', 'b']));
    assert.deepEqual(client.getQueryData(key), rows);
  } finally {
    client.clear();
  }
});

test('首次列表读取已失败时，刷新任一渠道恢复原批次', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    setRequest(async () => {
      throw new Error('initial read failed');
    });
    await assert.rejects(client.fetchQuery(channelTodayUsageQueryOptions(['a', 'b'])), /initial read failed/);
    const rows = [
      { channelId: 'a', totalCost: 1, models: [] },
      { channelId: 'b', totalCost: 4, models: [] },
    ];
    setRequest(async (_query, variables) => {
      assert.deepEqual(variables.channelIds, ['a', 'b']);
      return { channelTodayUsage: rows };
    });
    await refreshChannelTodayUsage(client, ['b']);
    assert.equal(client.getQueryState(['channel-today-usage', ['a', 'b']]).status, 'success');
    assert.deepEqual(client.getQueryData(['channel-today-usage', ['a', 'b']]), rows);
  } finally {
    client.clear();
  }
});
