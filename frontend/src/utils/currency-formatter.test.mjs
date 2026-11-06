import assert from 'node:assert/strict';
import test from 'node:test';
import { formatCurrencyValue } from './currency-formatter.ts';

test('currency formatting preserves locale, currency and precision', () => {
  for (const locale of ['zh-CN', 'en-US']) {
    for (const digits of [0, 2, 8]) {
      const options = { style: 'currency', currency: 'USD', minimumFractionDigits: digits, maximumFractionDigits: digits };
      assert.equal(formatCurrencyValue(1234.56789, locale, options), new Intl.NumberFormat(locale, options).format(1234.56789));
    }
  }
});
test('repeated table cells reuse one formatter', () => {
  const Original = Intl.NumberFormat;
  let constructed = 0;
  Intl.NumberFormat = class extends Original {
    constructor(...args) {
      super(...args);
      constructed++;
    }
  };
  try {
    for (let i = 0; i < 1000; i++) formatCurrencyValue(i, 'de-DE', { style: 'currency', currency: 'EUR' });
    assert.equal(constructed, 1);
  } finally {
    Intl.NumberFormat = Original;
  }
});
