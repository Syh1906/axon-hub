import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import ts from 'typescript';

const compile = (source) => ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText;
const moduleURL = (source) => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`;
const utils = compile(readFileSync(join(import.meta.dirname, '../../../lib/utils.ts'), 'utf8'))
  .replaceAll("'clsx'", JSON.stringify(import.meta.resolve('clsx')))
  .replaceAll("'tailwind-merge'", JSON.stringify(import.meta.resolve('tailwind-merge')));
const source = compile(readFileSync(join(import.meta.dirname, 'consumption.ts'), 'utf8'))
  .replaceAll("import { useCallback } from 'react';", 'const useCallback = (fn) => fn;')
  .replaceAll("import { useQuery } from '@tanstack/react-query';", 'const useQuery = (options) => options;')
  .replaceAll("import { graphqlRequest } from '@/gql/graphql';", 'const graphqlRequest = () => { throw new Error("unexpected network"); };')
  .replaceAll("import { useAuthStore } from '@/stores/authStore';", 'let userID = "viewer"; export const setTestUserID = (id) => { userID = id; }; const useAuthStore = (select) => select({ auth: { user: { id: userID } } });')
  .replaceAll("'@/lib/utils'", JSON.stringify(moduleURL(utils)))
  .replaceAll("'zod'", JSON.stringify(import.meta.resolve('zod')));
const { useConsumption, setTestUserID } = await import(moduleURL(source));
const input = { period: 'YESTERDAY', dimension: 'API_KEY_GROUP', metric: 'COST' };
const data = {
  entries: [
    { id: 'gid://axonhub/APIKeyGroup/1', name: '开发组', models: [{ model: 'model-a' }], metrics: { totalCost: 7.3 } },
    { id: 'gid://axonhub/APIKeyGroup/2', name: 'Team B', models: [], metrics: { totalCost: 3.07 } },
  ],
  rangeStart: '2026-11-07T00:00:00Z', rangeEnd: '2026-11-08T00:00:00Z', timezone: 'UTC', currencyCode: 'CNY',
  generatedAt: new Date().toISOString(),
};

test('group search and selection reuse the complete cached summary without a new request', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let calls = 0;
  const queryFn = async () => { calls++; return data; };
  const options = useConsumption(input, 'usage', 'project-1');
  await client.fetchQuery({ ...options, queryFn });
  for (const filter of [{ search: ' TEAM ' }, { groupRef: 2 }, { groupRef: 2, search: 'team' }]) {
    const selected = useConsumption({ ...input, ...filter }, 'usage', 'project-1');
    assert.deepEqual(selected.queryKey, options.queryKey);
    await client.fetchQuery({ ...selected, queryFn });
    const observer = new QueryObserver(client, { ...selected, queryFn });
    assert.deepEqual(observer.getCurrentResult().data.entries, [data.entries[1]]);
    observer.destroy();
  }
  assert.equal(calls, 1);
  assert.equal(client.getQueryData(options.queryKey), data);
  client.clear();
});

test('local filtering preserves metrics, model details and empty results', () => {
  const options = useConsumption({ ...input, search: '开发' }, 'usage', 'project-1');
  assert.equal(options.select(data).entries[0], data.entries[0]);
  assert.deepEqual(useConsumption({ ...input, groupRef: 99 }, 'usage', 'project-1').select(data).entries, []);
  assert.equal(useConsumption(input, 'usage', 'project-1').select(data), data);
});

test('key and user searches retain server matching for undisplayed names and email', () => {
  for (const dimension of ['API_KEY', 'USER']) {
    const filter = { ...input, dimension, search: 'person@example.test', groupRef: 2 };
    const options = useConsumption(filter, 'usage', 'project-1');
    assert.equal(options.queryKey.at(-1).search, filter.search);
    assert.equal(options.queryKey.at(-1).groupRef, 2);
    assert.equal(options.gcTime, 300_000, 'each server-filtered result keeps the shorter cache lifetime');
    assert.equal(options.select(data), data);
  }
});

test('query caches isolate projects and views and keep historical refresh finite', () => {
  const options = useConsumption(input, 'usage', 'project-1');
  assert.notDeepEqual(options.queryKey, useConsumption(input, 'usage', 'project-2').queryKey);
  assert.notDeepEqual(options.queryKey, useConsumption(input, 'leaderboard', 'project-1').queryKey);
  assert.equal(useConsumption(input, 'usage', null).enabled, false);
  assert.equal(options.staleTime({ state: {} }), 300_000);
  assert.equal(options.refetchInterval({ state: {} }), 300_000);
  assert.equal(options.gcTime, 1_800_000);
  assert.equal(useConsumption({ ...input, period: 'TODAY' }, 'usage', 'project-1').staleTime({ state: {} }), 60_000);
});

test('different users cannot reuse the same statistics cache', () => {
  const first = useConsumption(input, 'usage', 'project-1');
  try {
    setTestUserID('another-viewer');
    assert.notDeepEqual(first.queryKey, useConsumption(input, 'usage', 'project-1').queryKey);
    setTestUserID(undefined);
    assert.equal(useConsumption(input, 'usage', 'project-1').enabled, false);
  } finally {
    setTestUserID('viewer');
  }
});

test('a failed refresh retains the cached summary and exposes the error for retry', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const options = useConsumption({ ...input, search: '开发' }, 'usage', 'project-1');
  await client.fetchQuery({ ...options, queryFn: async () => data });
  const observer = new QueryObserver(client, { ...options, queryFn: async () => { throw new Error('read timed out'); } });
  const result = await observer.refetch();
  assert.equal(result.error.message, 'read timed out');
  assert.deepEqual(result.data.entries, [data.entries[0]]);
  observer.destroy();
  client.clear();
});

test('returning after reporting midnight reloads yesterday even within five minutes', async (t) => {
  const beforeMidnight = Date.parse('2026-11-08T15:59:00Z');
  let now = beforeMidnight;
  t.mock.method(Date, 'now', () => now);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const options = useConsumption(input, 'usage', 'project-1');
  let calls = 0;
  const queryFn = async () => {
    calls++;
    return { ...data, timezone: 'Asia/Shanghai', generatedAt: new Date(now).toISOString(),
      rangeStart: calls === 1 ? '2026-11-06T16:00:00Z' : '2026-11-07T16:00:00Z' };
  };
  await client.fetchQuery({ ...options, queryFn });
  now = Date.parse('2026-11-08T16:01:00Z');
  const updated = await client.fetchQuery({ ...options, queryFn });
  assert.equal(calls, 2);
  assert.equal(updated.rangeStart, '2026-11-07T16:00:00Z');
  client.clear();
});

test('active relative views refresh at reporting midnight and on wake after it', (t) => {
  const before = Date.parse('2026-11-08T15:59:30Z');
  let now = before;
  t.mock.method(Date, 'now', () => now);
  const query = { state: { data: { ...data, timezone: 'Asia/Shanghai', generatedAt: new Date(before).toISOString() }, dataUpdatedAt: before } };
  for (const period of ['TODAY', 'YESTERDAY', 'WEEK', 'MONTH']) {
    const options = useConsumption({ ...input, period }, 'usage', 'project-1');
    now = before;
    assert.equal(options.refetchInterval(query), 30_000);
    assert.equal(options.staleTime(query), 30_000);
    assert.equal(options.refetchOnWindowFocus(query), false);
    now = before + 31_000;
    assert.equal(options.refetchOnWindowFocus(query), true);
  }
  const custom = useConsumption({ ...input, period: 'CUSTOM' }, 'usage', 'project-1');
  assert.equal(custom.staleTime(query), 300_000);
  assert.equal(custom.refetchOnWindowFocus(query), false);
});

test('a successful response crossing midnight refreshes promptly without accelerating failed reads', (t) => {
  const now = Date.parse('2026-11-08T16:00:01Z');
  t.mock.method(Date, 'now', () => now);
  const query = { state: { data: { ...data, timezone: 'Asia/Shanghai', generatedAt: '2026-11-08T15:59:59Z' }, dataUpdatedAt: now, error: null } };
  const options = useConsumption(input, 'usage', 'project-1');
  assert.equal(options.staleTime(query), 0);
  assert.equal(options.refetchInterval(query), 1_000);
  query.state.error = new Error('read timed out');
  assert.equal(options.refetchInterval(query), 300_000);
});
