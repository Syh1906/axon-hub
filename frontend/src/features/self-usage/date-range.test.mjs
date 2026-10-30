import assert from 'node:assert/strict';
import test from 'node:test';
import { calendarDay, presetRange, validRange, chartDay } from './utils/date-range.ts';

test('presets use the configured timezone around UTC midnight', () => {
  const now = new Date('2026-09-25T00:30:00Z');
  assert.deepEqual(presetRange('today', 'America/Los_Angeles', now), { start: '2026-09-24', end: '2026-09-24' });
  assert.deepEqual(presetRange('yesterday', 'Asia/Kolkata', now), { start: '2026-09-24', end: '2026-09-24' });
  assert.deepEqual(presetRange('last7', 'America/Los_Angeles', now), { start: '2026-09-18', end: '2026-09-24' });
  assert.deepEqual(presetRange('last30', 'Asia/Kolkata', now), { start: '2026-08-27', end: '2026-09-25' });
  assert.deepEqual(presetRange('thisMonth', 'America/Los_Angeles', new Date('2026-10-01T01:00:00Z')), {
    start: '2026-09-01',
    end: '2026-09-30',
  });
});

test('spring and fall DST transitions retain calendar days', () => {
  assert.deepEqual(presetRange('last7', 'America/Los_Angeles', new Date('2026-03-09T06:30:00Z')), {
    start: '2026-03-02',
    end: '2026-03-08',
  });
  assert.deepEqual(presetRange('last7', 'America/Los_Angeles', new Date('2026-11-02T08:30:00Z')), {
    start: '2026-10-27',
    end: '2026-11-02',
  });
  assert.equal(calendarDay(new Date('2026-03-08T18:00:00Z'), 'Asia/Kathmandu'), '2026-03-08');
  assert.equal(chartDay('2026-03-08', 'en-US'), '03/08');
  assert.equal(chartDay('2026-10-30', 'zh-CN'), '10/30');
});

test('custom spans count inclusive calendar days and reject invalid dates', () => {
  assert.equal(validRange({ start: '2026-01-01', end: '2026-03-31' }), true);
  assert.equal(validRange({ start: '2026-01-01', end: '2026-04-01' }), false);
  assert.equal(validRange({ start: '2026-03-08', end: '2026-03-08' }), true);
  assert.equal(validRange({ start: '2026-02-30', end: '2026-03-01' }), false);
  assert.equal(validRange({ start: '2026-03-09', end: '2026-03-08' }), false);
});
