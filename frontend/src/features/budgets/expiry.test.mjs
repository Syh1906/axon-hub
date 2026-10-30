import assert from 'node:assert/strict';
import test from 'node:test';
import { extendBudgetExpiry } from './expiry.ts';

test('calendar month renewal clamps month end and preserves time', () => {
  const now = new Date(2026, 0, 1, 9);
  const expiry = '2026-01-31T16:45';
  const next = extendBudgetExpiry(expiry, 1, now);
  assert.deepEqual([next.getFullYear(), next.getMonth(), next.getDate(), next.getHours(), next.getMinutes()], [2026, 1, 28, 16, 45]);
  assert.equal(extendBudgetExpiry(expiry, 3, now).getDate(), 30);
});
test('calendar year renewal handles leap day', () => {
  const next = extendBudgetExpiry('2028-02-29T12:30', 12, new Date(2028, 0, 1));
  assert.deepEqual([next.getFullYear(), next.getMonth(), next.getDate(), next.getHours()], [2029, 1, 28, 12]);
});
test('expired and unlimited dates extend from now, future expiry extends from expiry', () => {
  const now = new Date(2026, 9, 30, 10, 20);
  for (const expiry of ['', '2026-01-01T10:20']) {
    assert.equal(extendBudgetExpiry(expiry, 1, now).getTime(), new Date(2026, 10, 30, 10, 20).getTime());
  }
  assert.equal(extendBudgetExpiry('2026-12-31T14:00', 1, now).getTime(), new Date(2027, 0, 31, 14).getTime());
});
test('calendar shortcut preserves local clock across DST', () => {
  const previous = process.env.TZ;
  try {
    process.env.TZ = 'America/New_York';
    const next = extendBudgetExpiry('2026-02-28T09:00', 1, new Date('2026-02-01T00:00:00Z'));
    assert.equal(next.toISOString(), '2026-03-28T13:00:00.000Z');
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
});
