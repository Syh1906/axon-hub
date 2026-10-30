import assert from 'node:assert/strict';
import test from 'node:test';
import { formatSystemTime } from './system-time.ts';

test('record times use the configured zone across midnight, independent of the host zone', () => {
  const previous = process.env.TZ;
  try {
    process.env.TZ = 'America/Los_Angeles';
    assert.equal(formatSystemTime('2026-10-30T16:28:20Z', 'Asia/Shanghai', 'yyyy-MM-dd HH:mm:ss', 'zh-CN'), '2026-10-31 00:28:20');
    assert.equal(formatSystemTime('2026-10-30T16:28:20Z', 'UTC', 'yyyy-MM-dd', 'en-US'), '2026-10-30');
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
});

test('absolute instants retain milliseconds and respect daylight-saving changes', () => {
  assert.equal(formatSystemTime('2026-03-08T06:59:59.123Z', 'America/New_York', 'HH:mm:ss.SSS', 'en-US'), '01:59:59.123');
  assert.equal(formatSystemTime('2026-03-08T07:00:00Z', 'America/New_York', 'HH:mm', 'en-US'), '03:00');
});
