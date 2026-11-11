import { QueryClient } from '@tanstack/react-query';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { z } from 'zod';
import * as upstreamRefresh from './channel-upstream-refresh.ts';
import { channelsWithoutPendingRefresh, hasUsableUpstreamRate, invalidateChannelUpstreamStates, publishUpstreamStates, readCachedUpstreamStates, refreshUpstreamChannels } from './channel-upstream-refresh.ts';

test('balance-only sources continue refreshing when their rate is unsupported', async () => {
  const calls = [];
  const states = ['DEEPSEEK', 'MOONSHOT', 'OPENAI_BILLING'].map((provider) => ({
    channelId: provider, settings: { enabled: true }, keys: [{ keyRef: provider, disabled: false,
      snapshot: { balanceState: 'SUCCESS', rateState: 'UNSUPPORTED' } }],
  }));
  const result = await refreshUpstreamChannels(states, { channelIds: states.map(s => s.channelId) }, async (id, ref) => {
    calls.push([id, ref]);
    return { channels: [states.find(s => s.channelId === id)], refreshed: 1, skipped: 0, failed: 0 };
  });
  assert.equal(result.refreshed, 3);
  assert.equal(result.failed, 0);
  assert.equal(calls.length, 3);
});

const now = Date.now();
const key = (id, extra = {}) => ({
  keyRef: id,
  disabled: false,
  snapshot: {
    balanceState: 'SUCCESS',
    rateState: 'SUCCESS',
    rate: { syncedAt: new Date(now).toISOString(), observedAt: new Date(now).toISOString(), validUntil: null },
  },
  ...extra,
});
const channel = (id, keys = [key('a'), key('b')]) => ({ channelId: id, settings: { enabled: true }, keys });

test('refresh is restricted to requested page/filter IDs, including an empty page', async () => {
  const states = [channel('page-a'), channel('page-b'), channel('filtered-out')];
  const calls = [];
  const refresh = async (id, ref) => {
    calls.push(`${id}/${ref}`);
    return { channels: [states.find(s => s.channelId === id)], refreshed: 1, skipped: 0, failed: 0 };
  };
  await refreshUpstreamChannels(states, { channelIds: ['page-b'], force: true }, refresh);
  assert.deepEqual(calls, ['page-b/a', 'page-b/b']);
  calls.length = 0;
  await refreshUpstreamChannels(states, { channelIds: [] }, refresh);
  assert.deepEqual(calls, []);
});

test('unsupported key endpoints do not prevent querying a configured New API account', async () => {
  const state = channel('account', [key('a', { accountRef: 'selected', snapshot: { balanceState: 'UNSUPPORTED', rateState: 'UNSUPPORTED' } })]);
  state.accounts = [{ ref: 'selected', provider: 'NEW_API', hasToken: true }];
  let calls = 0;
  await refreshUpstreamChannels([state], { channelIds: ['account'] }, async () => {
    calls++;
    return { channels: [state], refreshed: 1, skipped: 0, failed: 0 };
  });
  assert.equal(calls, 1);
});

test('preview rejects old observed data even when just synchronized, and expires at the upstream boundary', () => {
  const value = key('a');
  assert.equal(hasUsableUpstreamRate(value, now), true);
  value.snapshot.rate.observedAt = new Date(now - 31 * 60_000).toISOString();
  assert.equal(hasUsableUpstreamRate(value, now), false);
  value.snapshot.rate.observedAt = new Date(now).toISOString();
  value.snapshot.rate.validUntil = new Date(now).toISOString();
  assert.equal(hasUsableUpstreamRate(value, now), false);
  value.snapshot.rate.validUntil = null;
  value.snapshot.rateState = 'ERROR';
  assert.equal(hasUsableUpstreamRate(value, now), true);
  value.snapshot.rateState = 'AUTH_REQUIRED';
  assert.equal(hasUsableUpstreamRate(value, now), false);
});

test('an older in-flight read cannot replace the completed refresh', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const queryKey = ['channel-upstream', ['c']];
  client.setQueryData(queryKey, [{ ...channel('c'), version: 'cached' }]);
  let finish;
  const reading = client
    .fetchQuery({
      queryKey,
      queryFn: () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    })
    .catch(() => {});
  await publishUpstreamStates(client, [{ ...channel('c'), version: 'fresh' }]);
  finish([{ ...channel('c'), version: 'old' }]);
  await reading;
  assert.equal(client.getQueryData(queryKey)[0].version, 'fresh');
  client.clear();
});

test('refresh serializes keys within a channel, limits channels to three, and continues after a failed key', async () => {
  const states = ['1', '2', '3', '4'].map((id) => channel(id, [key('a'), key('b'), key('c')]));
  const active = new Set();
  const visited = [];
  let max = 0;
  const result = await refreshUpstreamChannels(states, { channelIds: states.map((c) => c.channelId) }, async (id, ref) => {
    assert.equal(active.has(id), false);
    active.add(id);
    max = Math.max(max, active.size);
    visited.push(`${id}/${ref}`);
    await new Promise(setImmediate);
    active.delete(id);
    if (id === '1' && ref === 'b') throw new Error('timeout');
    return { channels: [channel(id)], refreshed: 1, skipped: 0, failed: 0 };
  });
  assert.equal(max, 3);
  assert.equal(visited.length, 12);
  assert.deepEqual(
    visited.filter((v) => v.startsWith('1/')),
    ['1/a', '1/b', '1/c']
  );
  assert.equal(result.refreshed, 4);
  assert.equal(result.failed, 1);
});

test('automatic batches skip disabled sync and unsupported keys; explicit refresh can query them', async () => {
  const state = channel('c', [key('a', { snapshot: { balanceState: 'UNSUPPORTED', rateState: 'UNSUPPORTED' } })]);
  let calls = 0;
  const refresh = async () => {
    calls++;
    return { channels: [state], refreshed: 1, skipped: 0, failed: 0 };
  };
  await refreshUpstreamChannels([state], { channelIds: ['c'] }, refresh);
  assert.equal(calls, 0);
  state.settings.enabled = false;
  await refreshUpstreamChannels([state], { channelIds: ['c'], includeDisabled: true, keyRef: 'a' }, refresh);
  assert.equal(calls, 1);
  await assert.rejects(refreshUpstreamChannels([state], { channelIds: ['c'], keyRef: 'missing' }, refresh));
});


test('incremental channel pages refresh only new channels, while filter changes recheck the page', async () => {
  const { channelsNeedingPageRefresh } = await import('./channel-upstream-refresh.ts');
  assert.deepEqual(channelsNeedingPageRefresh([], ['a', 'b']), ['a', 'b']);
  assert.deepEqual(channelsNeedingPageRefresh(['a', 'b'], ['a', 'b', 'c', 'd']), ['c', 'd']);
  assert.deepEqual(channelsNeedingPageRefresh(['a', 'b', 'c'], ['a', 'b']), []);
  assert.deepEqual(channelsNeedingPageRefresh(['a', 'b'], ['a', 'b']), []);
  assert.deepEqual(channelsNeedingPageRefresh(['a', 'b'], ['c', 'b']), ['c', 'b']);
});

test('reentering a page skips channels already refreshing and can retry after that work ends', async () => {
  const client = new QueryClient();
  let finish;
  const mutation = client.getMutationCache().build(client, {
    mutationKey: ['channel-upstream-refresh'],
    mutationFn: () => new Promise(resolve => { finish = resolve; }),
  });
  const pending = mutation.execute({ channelIds: ['a', 'b'], silent: true });
  try {
    await Promise.resolve();
    assert.deepEqual(channelsWithoutPendingRefresh(client, ['a', 'b', 'c']), ['c']);
    while (!finish) await Promise.resolve();
    finish({ channels: [], refreshed: 0, skipped: 0, failed: 0 });
    await pending;
    assert.deepEqual(channelsWithoutPendingRefresh(client, ['a', 'b', 'c']), ['a', 'b', 'c']);
  } finally { client.clear(); }
});

test('automatic refresh reuses fresh batch members and reads only missing or invalidated channels', async () => {
  const client = new QueryClient();
  client.setQueryData(['channel-upstream', ['a', 'b']], [channel('a'), channel('b')]);
  client.setQueryData(['channel-upstream', ['expired']], [channel('expired')], { updatedAt: Date.now() - 31_000 });
  const calls = [];
  const read = async ids => { calls.push(ids); return ids.map(id => channel(id)); };
  try {
    const subset = await readCachedUpstreamStates(client, ['b'], read, true);
    assert.deepEqual(subset.map(state => state.channelId), ['b']);
    assert.deepEqual(calls, []);
    await readCachedUpstreamStates(client, ['a', 'missing', 'expired'], read, true);
    assert.deepEqual(calls, [['missing', 'expired']]);
    await client.invalidateQueries({ queryKey: ['channel-upstream'], refetchType: 'none' });
    await readCachedUpstreamStates(client, ['a', 'b'], read, true);
    assert.deepEqual(calls.at(-1), ['a', 'b']);
  } finally { client.clear(); }
});

test('manual refresh reads the current key set even while an older snapshot is fresh', async () => {
  const client = new QueryClient();
  const old = channel('a', [key('removed')]);
  const current = channel('a', [key('added')]);
  client.setQueryData(['channel-upstream', ['a']], [old]);
  const refreshed = [];
  try {
    const states = await readCachedUpstreamStates(client, ['a'], async () => [current], false);
    await refreshUpstreamChannels(states, { channelIds: ['a'], keyRef: 'added', force: true }, async (_id, keyRef) => {
      refreshed.push(keyRef);
      return { channels: [current], refreshed: 1, skipped: 0, failed: 0 };
    });
    assert.deepEqual(refreshed, ['added']);
  } finally { client.clear(); }
});

test('a key edit invalidates every containing batch and preserves unrelated snapshots', async () => {
  const client = new QueryClient();
  client.setQueryData(['channel-upstream', ['a', 'b']], [channel('a'), channel('b')]);
  client.setQueryData(['channel-upstream', ['a']], [channel('a')]);
  client.setQueryData(['channel-upstream', ['c']], [channel('c')]);
  const reads = [];
  try {
    await invalidateChannelUpstreamStates(client, ['a']);
    assert.equal(client.getQueryState(['channel-upstream', ['a', 'b']]).isInvalidated, true);
    assert.equal(client.getQueryState(['channel-upstream', ['a']]).isInvalidated, true);
    assert.equal(client.getQueryState(['channel-upstream', ['c']]).isInvalidated, false);
    const states = await readCachedUpstreamStates(client, ['a', 'c'], async ids => {
      reads.push(ids);
      return [channel('a', [key('new-key')])];
    }, true);
    assert.deepEqual(reads, [['a']]);
    assert.deepEqual(states[0].keys.map(k => k.keyRef), ['new-key']);
  } finally { client.clear(); }
});

test('the actual refresh hook reads new keys for manual work and reuses snapshots only when explicitly requested', async () => {
  const client = new QueryClient();
  const old = channel('a', [key('removed')]);
  const current = channel('a', [key('added')]);
  const calls = [];
  const source = ts.transpileModule(readFileSync(new URL('./channel-upstream.ts', import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
  }).outputText;
  const dependencies = {
    react: {}, zod: { z },
    '@tanstack/react-query': { useQueryClient: () => client, useMutation: options => options },
    'react-i18next': { useTranslation: () => ({ t: value => value }) },
    sonner: { toast: { success() {}, warning() {} } },
    '@/hooks/use-error-handler': { useErrorHandler: () => ({ handleError() {} }) },
    './schema': { channelUpstreamStateSchema: z.unknown() },
    './channel-upstream-refresh': upstreamRefresh,
    '@/gql/graphql': { graphqlRequest: async (query, variables) => {
      if (query.includes('query ChannelUpstreamStates')) {
        calls.push('read');
        return { channelUpstreamStates: [current] };
      }
      calls.push(variables.keyRef);
      return { refreshChannelUpstreams: { channels: [current], refreshed: 1, skipped: 0, failed: 0 } };
    } },
  };
  const exports = {};
  new Function('require', 'exports', source)((name) => {
    assert.ok(name in dependencies, `unexpected dependency: ${name}`);
    return dependencies[name];
  }, exports);
  try {
    client.setQueryData(['channel-upstream', ['a']], [old]);
    const mutation = exports.useRefreshChannelUpstreams();
    await mutation.mutationFn({ channelIds: ['a'], keyRef: 'added', force: true, includeDisabled: true });
    assert.deepEqual(calls, ['read', 'added']);
    calls.length = 0;
    await mutation.mutationFn({ channelIds: ['a'], silent: true, reuseSnapshot: true });
    assert.deepEqual(calls, ['added']);
    // 手动读取发现新快照，即便不用访问供应商，也必须发布到原批次。
    client.setQueryData(['channel-upstream', ['a', 'b']], [old, channel('b')]);
    await mutation.onSuccess({ channels: [current], refreshed: 0, failed: 0 }, { channelIds: ['a'] });
    assert.deepEqual(client.getQueryData(['channel-upstream', ['a', 'b']])[0].keys.map(k => k.keyRef), ['added']);
  } finally { client.clear(); }
});

test('a key edit cancels an unfinished first snapshot before another refresh can reuse it', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const queryKey = ['channel-upstream', ['a']];
  let finish;
  let canceled = false;
  const pending = client.fetchQuery({ queryKey, queryFn: ({ signal }) => new Promise(resolve => {
    finish = resolve;
    signal.addEventListener('abort', () => { canceled = true; });
  }) }).catch(() => undefined);
  try {
    await invalidateChannelUpstreamStates(client, ['a']);
    const next = channel('a', [key('new-key')]);
    const states = await readCachedUpstreamStates(client, ['a'], ids => client.fetchQuery({
      queryKey: ['channel-upstream', ids], queryFn: async () => [next], staleTime: 0,
    }), true);
    finish([channel('a', [key('removed')])]);
    await pending;
    assert.equal(canceled, true);
    assert.deepEqual(states[0].keys.map(k => k.keyRef), ['new-key']);
    assert.deepEqual(client.getQueryData(queryKey)[0].keys.map(k => k.keyRef), ['new-key']);
  } finally { client.clear(); }
});

for (const hook of ['useUpdateChannel', 'useUpdateChannelSettings', 'useDisableChannelAPIKey', 'useEnableChannelAPIKey',
  'useEnableAllChannelAPIKeys', 'useEnableSelectedChannelAPIKeys', 'useDeleteDisabledChannelAPIKeys']) {
  test(`${hook} invalidates the affected upstream snapshots after the actual save callback`, async () => {
    const source = ts.createSourceFile('channels.ts', readFileSync(new URL('./channels.ts', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true);
    const declaration = source.statements.find(node => ts.isFunctionDeclaration(node) && node.name?.text === hook);
    let callback;
    const visit = node => {
      if (ts.isPropertyAssignment(node) && node.name.getText(source) === 'onSuccess') callback = node.initializer.getText(source);
      ts.forEachChild(node, visit);
    };
    visit(declaration);
    assert.ok(callback);
    const client = new QueryClient();
    const pending = [];
    client.setQueryData(['channel-upstream', ['a', 'b']], [channel('a'), channel('b')]);
    client.setQueryData(['channel-upstream', ['other']], [channel('other')]);
    const onSuccess = new Function('queryClient', 'invalidateChannelUpstreamStates', 'invalidateChannelIdentityQueries', 'toast', 't',
      `return ${callback}`)(client, (queryClient, ids) => {
        const work = invalidateChannelUpstreamStates(queryClient, ids);
        pending.push(work);
        return work;
      }, () => {}, { success() {} }, value => value);
    try {
      onSuccess({ id: 'a' }, { channelID: 'a', input: { credentials: { apiKeys: ['synthetic-new-key'] } } });
      await Promise.all(pending);
      assert.equal(client.getQueryState(['channel-upstream', ['a', 'b']]).isInvalidated, true);
      assert.equal(client.getQueryState(['channel-upstream', ['other']]).isInvalidated, false);
    } finally { client.clear(); }
  });
}
