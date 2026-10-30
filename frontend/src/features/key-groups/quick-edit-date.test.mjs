import assert from 'node:assert/strict';
import test from 'node:test';
import { localExpiry, expiryInstant } from './quick-edit-date.ts';

test('expiry edits round trip in UTC, Shanghai and across daylight-saving boundaries', () => {
  const original = process.env.TZ;
  try {
    for (const [zone, text, iso] of [
      ['UTC', '2026-12-30T23:59', '2026-12-30T23:59:00.000Z'],
      ['Asia/Shanghai', '2026-12-30T23:59', '2026-12-30T15:59:00.000Z'],
      ['America/New_York', '2026-03-08T03:30', '2026-03-08T07:30:00.000Z'],
      ['America/New_York', '2026-11-01T02:30', '2026-11-01T07:30:00.000Z'],
    ]) {
      process.env.TZ = zone;
      assert.equal(expiryInstant(text).toISOString(), iso);
      assert.equal(localExpiry(iso), text);
    }
    assert.equal(localExpiry(null), '');
    process.env.TZ = 'America/New_York';
    assert.equal(Number.isNaN(expiryInstant('2026-03-08T02:30').getTime()), true);
    const repeatedHour = '2026-11-01T06:30:45.000Z';
    assert.equal(expiryInstant(localExpiry(repeatedHour), repeatedHour).toISOString(), repeatedHour);
    assert.equal(Number.isNaN(expiryInstant('2026-02-30T12:00').getTime()), true);
  } finally {
    if (original === undefined) delete process.env.TZ;
    else process.env.TZ = original;
  }
});
