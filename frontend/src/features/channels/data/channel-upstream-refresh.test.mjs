import { QueryClient } from '@tanstack/react-query';
import assert from 'node:assert/strict';
import test from 'node:test';
import { hasUsableUpstreamRate, publishUpstreamStates, refreshUpstreamChannels } from './channel-upstream-refresh.ts';

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
