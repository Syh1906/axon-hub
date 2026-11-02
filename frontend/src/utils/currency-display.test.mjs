import assert from 'node:assert/strict';
import test from 'node:test';
import { currencyFractionDigits } from './currency-display.ts';

const money = (value, decimals, min, max) =>
  new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', ...currencyFractionDigits(decimals, min, max) }).format(value);

test('the default preserves each view’s existing precision, including while loading', () => {
  for (const decimals of [null, undefined]) {
    assert.equal(money(1.23456789, decimals, 4, 6), '$1.234568');
    assert.equal(money(1.23456789, decimals, 2, 8), '$1.23456789');
    assert.equal(money(1, decimals, 6), '$1.000000');
    assert.equal(money(1, decimals, 6, 8), '$1.000000');
  }
});

test('explicit precision rounds only the rendered value and pads trailing zeroes', () => {
  const value = 1.23456789;
  assert.equal(money(value, 2, 6, 8), '$1.23');
  assert.equal(money(1, 2, 4, 6), '$1.00');
  assert.equal(money(1.999, 2, 4, 6), '$2.00');
  assert.equal(value, 1.23456789);
  assert.equal(money(value, null, 2, 8), '$1.23456789');
});

test('zero and eight places are valid explicit choices', () => {
  assert.equal(money(1.56, 0, 6, 8), '$2');
  assert.equal(money(0, 0, 6, 8), '$0');
  assert.equal(money(1.23456789, 8, 4, 6), '$1.23456789');
  assert.equal(money(0, 8), '$0.00000000');
  assert.equal((1.23456789).toLocaleString('en-US', currencyFractionDigits(2, undefined, 6)), '1.23');
});
