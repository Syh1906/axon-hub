import assert from 'node:assert/strict';
import test from 'node:test';
import { createChannelControlStore } from './channel-control-store.ts';

test('只通知变化渠道，保留其他控件的数据及相同值引用', () => {
  const store = createChannelControlStore();
  let first = 0, second = 0;
  const stop = store.subscribe('a', () => first++);
  store.subscribe('b', () => second++);
  store.publish('a', { liveUsage: { inFlight: 1, rpm: 3 } });
  const load = store.get('a').liveUsage;
  store.publish('a', { liveUsage: { inFlight: 1, rpm: 3 } });
  assert.equal(first, 1);
  assert.equal(second, 0);
  store.publish('a', { probePoints: [{ timestamp: 1, totalRequestCount: 2, successRequestCount: 2 }] });
  assert.equal(store.get('a').liveUsage, load);
  assert.equal(first, 2);
  stop();
  store.publish('a', { status: 'disabled' });
  assert.equal(first, 2);
});

test('控件读取失败保留上次成功结果，恢复只清除错误状态', () => {
  const store = createChannelControlStore();
  const usage = { channelId: 'a', requestCount: 2, totalCost: 1, unpricedCount: 0, timezone: 'UTC', currencyCode: 'USD' };
  store.publish('a', { todayUsage: usage, todayUsageError: false });
  store.publish('a', { todayUsageError: true });
  assert.deepEqual(store.get('a').todayUsage, usage);
  store.publish('a', { todayUsageError: false });
  assert.deepEqual(store.get('a'), { todayUsage: usage, todayUsageError: false });
});

test('滚动离开的渠道释放动态数据，保留仍在列表中的对象', () => {
  const store = createChannelControlStore();
  store.publish('a', { status: 'disabled' });
  store.publish('b', { status: 'enabled' });
  const retained = store.get('b');
  store.retain(['b']);
  assert.equal(store.get('a'), undefined);
  assert.equal(store.get('b'), retained);
});
