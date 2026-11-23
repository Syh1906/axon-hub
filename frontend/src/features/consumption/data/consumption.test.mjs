import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import ts from 'typescript';
import i18next from 'i18next';

await i18next.init({ lng: 'zh-CN', fallbackLng: 'en', resources: Object.fromEntries(['zh-CN', 'en'].map((lang) => [lang, { translation: JSON.parse(readFileSync(join(import.meta.dirname, '../../../locales', lang, 'channels.json'), 'utf8')) }])) });

const compile = (source) => ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText;
const moduleURL = (source) => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`;
const utils = compile(readFileSync(join(import.meta.dirname, '../../../lib/utils.ts'), 'utf8'))
  .replaceAll("'clsx'", JSON.stringify(import.meta.resolve('clsx')))
  .replaceAll("'tailwind-merge'", JSON.stringify(import.meta.resolve('tailwind-merge')));
const slices = compile(readFileSync(join(import.meta.dirname, 'consumption-slices.ts'), 'utf8'));
const source = compile(readFileSync(join(import.meta.dirname, 'consumption.ts'), 'utf8'))
  .replaceAll("import { useCallback, useState } from 'react';", 'const useCallback = (fn) => fn; let state; const useState = () => [state, (next) => { state = next; }];')
  .replaceAll("import { useTranslation } from 'react-i18next';", `import i18next from ${JSON.stringify(import.meta.resolve('i18next'))}; const useTranslation = () => ({ t: i18next.getFixedT(i18next.language) });`)
  .replaceAll("import { useQuery, useQueryClient } from '@tanstack/react-query';", 'let testError; export const setTestError = (err) => {testError = err;}; const useQuery = (options) => testError ? {...options,isError:true,error:testError} : options; let testClient; export const setTestClient = (value) => { testClient = value; state = undefined; }; const useQueryClient = () => testClient;')
  .replaceAll("import { graphqlRequest } from '@/gql/graphql';", 'const graphqlRequest = () => { throw new Error("unexpected network"); };')
  .replaceAll("import { useAuthStore } from '@/stores/authStore';", 'let userID = "viewer"; export const setTestUserID = (id) => { userID = id; }; const useAuthStore = (select) => select({ auth: { user: { id: userID } } });')
  .replaceAll("'./consumption-slices'", JSON.stringify(moduleURL(slices)))
  .replaceAll("'@/lib/utils'", JSON.stringify(moduleURL(utils)))
  .replaceAll("'zod'", JSON.stringify(import.meta.resolve('zod')));
const { useConsumption, useConsumptionGroupOptions, setTestUserID, setTestClient, setTestError } = await import(moduleURL(source));
const input = { period: 'YESTERDAY', dimension: 'API_KEY_GROUP', metric: 'COST' };

test('同名渠道按标签搜索后保留辨认信息并复用完整统计缓存', async () => {
  const channels = {
    ...data,
    entries: [
      { ...data.entries[0], name: 'same', channelProvider: 'codex', channelTags: ['小为'], duplicateName: true },
      { ...data.entries[1], name: 'same', channelProvider: 'codex', channelTags: ['裂魂'], duplicateName: true },
      { ...data.entries[1], id: 'unique', name: 'unique', channelProvider: 'openai', channelTags: ['备用'], duplicateName: false },
    ],
  };
  const filter = { ...input, dimension: 'CHANNEL' };
  const base = useConsumption(filter, 'leaderboard', null);
  const selected = useConsumption({ ...filter, search: '小为' }, 'leaderboard', null);
  assert.deepEqual(selected.queryKey, base.queryKey);
  assert.deepEqual(selected.select(channels).entries, [channels.entries[0]]);
  assert.equal(selected.select(channels).entries[0].duplicateName, true);
  assert.equal(useConsumption({ ...filter, search: 'CODEX' }, 'leaderboard', null).select(channels).entries.length, 2);
  assert.equal(useConsumption({ ...filter, search: '备用' }, 'leaderboard', null).select(channels).entries[0].duplicateName, false);
  assert.equal(channels.entries.length, 3);
});

test('分组选项使用轻量查询且隔离用户、视图和项目', () => {
  const options = useConsumptionGroupOptions(input, 'usage', 'project-1');
  const filtered = useConsumptionGroupOptions({ ...input, dimension: 'API_KEY', groupRef: 2, search: 'test' }, 'usage', 'project-1');
  assert.deepEqual(options.queryKey, filtered.queryKey);
  assert.notDeepEqual(options.queryKey, useConsumptionGroupOptions(input, 'usage', 'project-2').queryKey);
  assert.notDeepEqual(options.queryKey, useConsumptionGroupOptions(input, 'leaderboard', 'project-1').queryKey);
  assert.equal(useConsumptionGroupOptions(input, 'usage', null).enabled, false);
  assert.equal(useConsumptionGroupOptions(input, 'usage', 'project-1', false).enabled, false);
  assert.match(options.queryFn.toString(), /ConsumptionGroupOptions/);
  assert.doesNotMatch(options.queryFn.toString(), /consumptionLeaderboard|usageStatistics/);
});
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
    // 同页筛选只更新 select，不重新挂载查询或主动调用 fetchQuery。
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
  assert.equal(options.staleTime, 0);
  assert.equal(options.refetchInterval, false);
  assert.equal(options.gcTime, 1_800_000);
  assert.equal(useConsumption({ ...input, period: 'TODAY' }, 'usage', 'project-1').staleTime, 0);
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

test('统计只在进入页面、改变条件或明确刷新时更新，不在悬停和窗口聚焦时轮询', () => {
  for (const period of ['TODAY', 'YESTERDAY', 'WEEK', 'MONTH', 'ALL', 'CUSTOM']) {
    const options = useConsumption({ ...input, period }, 'usage', 'project-1');
    assert.equal(options.refetchInterval, false);
    assert.equal(options.refetchOnWindowFocus, false);
    assert.equal(options.refetchOnMount, 'always');
    assert.equal(options.staleTime, 0);
  }
});

test('服务商搜索匹配中文名和带空格名称，切换语言仍复用查询缓存', async () => {
  const result = { ...data, entries: [
    { ...data.entries[0], name: 'provider-one', channelProvider: 'zhipu', channelTags: [], duplicateName: true },
    { ...data.entries[1], name: 'provider-two', channelProvider: 'claudecode', channelTags: [], duplicateName: true },
  ] };
  const filter = { ...input, dimension: 'CHANNEL' };
  const base = useConsumption(filter, 'leaderboard', null);
  for (const [search, index] of [['智谱', 0], ['Claude Code', 1], ['ZHIPU', 0]]) {
    const selected = useConsumption({ ...filter, search }, 'leaderboard', null);
    assert.deepEqual(selected.queryKey, base.queryKey);
    assert.deepEqual(selected.select(result).entries, [result.entries[index]]);
  }
  await i18next.changeLanguage('en');
  try {
    const selected = useConsumption({ ...filter, search: i18next.t('channels.providers.zhipu') }, 'leaderboard', null);
    assert.deepEqual(selected.queryKey, base.queryKey);
    assert.deepEqual(selected.select(result).entries, [result.entries[0]]);
  } finally { await i18next.changeLanguage('zh-CN'); }
});

test('收起行从同一份已核对数据恢复，正在读取的旧选择随观察者切换取消', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  setTestClient(client);
  const [a, b] = data.entries.map((entry) => entry.id);
  try {
    const summary = useConsumption(input, 'usage', 'project-1');
    const one = useConsumption(input, 'usage', 'project-1', true, [a]);
    const both = useConsumption(input, 'usage', 'project-1', true, [a, b]);
    client.setQueryData(summary.queryKey, { ...data, entries: data.entries.map((entry) => ({ ...entry, models: [] })) });
    client.setQueryData(both.queryKey, data);
    assert.equal(both.changeDetailSelection([a], true), true);
    assert.deepEqual(client.getQueryData(one.queryKey).entries[0].models, data.entries[0].models);
    assert.equal(one.changeDetailSelection([], true), true);
    assert.ok(client.getQueryData(summary.queryKey).entries.every((entry) => entry.models.length === 0));

    const observer = new QueryObserver(client, { ...both, queryFn: ({ signal }) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(signal.reason), { once: true });
    }) });
    const unsubscribe = observer.subscribe(() => {});
    const active = client.getQueryCache().find({ queryKey: both.queryKey });
    assert.equal(active.state.fetchStatus, 'fetching');
    let reads = 0;
    observer.setOptions({ ...useConsumption(input, 'usage', 'project-1', true, [], true),
      queryFn: () => { reads++; return data; } });
    assert.equal(active.state.fetchStatus, 'idle');
    assert.equal(observer.getCurrentResult().fetchStatus, 'idle');
    assert.equal(reads, 0);
    unsubscribe();
  } finally {
    setTestClient(undefined);
    client.clear();
  }
});

test('第二行首次加载时收起或失败，保留已成功的第一行', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  setTestClient(client);
  const [a, b] = data.entries.map((entry) => entry.id);
  const options = (ids, reused = false) => useConsumption(input, 'usage', 'project-1', true, ids, reused);
  try {
    const one = options([a]);
    await client.fetchQuery({ ...one, queryFn: async () => data });
    one.changeDetailSelection([a, b], false);
    const both = options([a, b]);
    let aborted = false;
    const observer = new QueryObserver(client, { ...one, refetchOnMount: false });
    const unsubscribe = observer.subscribe(() => {});
    observer.setOptions({ ...both, queryFn: ({ signal }) => new Promise((_resolve, reject) =>
      signal.addEventListener('abort', () => { aborted = true; reject(signal.reason); }, { once: true })) });
    await new Promise((resolve) => setTimeout(resolve, 10));
    assert.equal(client.getQueryData(one.queryKey), undefined);
    assert.equal(client.getQueryData(both.queryKey), undefined);
    assert.equal(both.changeDetailSelection([a], true), true);
    let reads = 0;
    observer.setOptions({ ...options([a], true), queryFn: () => { reads++; return data; } });
    assert.equal(aborted, true);
    assert.equal(reads, 0);
    assert.deepEqual(observer.getCurrentResult().data.entries[0].models, data.entries[0].models);
    assert.equal(both.changeDetailSelection([b], true), false);
    unsubscribe();

    const failing = options([a, b]);
    const clientError = new Error('fixture failure');
    setTestError(clientError);
    const failed = options([a, b]);
    assert.equal(failed.error, clientError);
    assert.deepEqual(failed.data.entries[0].models, data.entries[0].models);
    assert.equal(useConsumption(input, 'usage', 'project-2', true, [a]).data, undefined);
    assert.equal(client.getQueryData(failing.queryKey), undefined);
  } finally {
    setTestError(undefined);
    setTestClient(undefined);
    client.clear();
  }
});
