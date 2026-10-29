import assert from 'node:assert/strict';
import test from 'node:test';
import { openSelfUsagePage } from './session.ts';

test('usage shortcuts initialize isolated tabs without navigating the parent or exposing a key in URLs', () => {
  const tabs = [];
  const original = globalThis.window;
  globalThis.window = {
    open(url, target) {
      assert.equal(url, 'about:blank');
      assert.equal(target, '_blank');
      const values = new Map();
      const tab = {
        sessionStorage: { setItem: (key, value) => values.set(key, value) },
        opener: {},
        location: {
          replace: (url) => {
            tab.url = url;
          },
        },
        values,
      };
      tabs.push(tab);
      return tab;
    },
  };
  try {
    assert.equal(openSelfUsagePage('fixture-a'), true);
    assert.equal(openSelfUsagePage('fixture-b'), true);
    assert.equal(tabs[0].values.get('axonhub.selfUsage.apiKey'), 'fixture-a');
    assert.equal(tabs[1].values.get('axonhub.selfUsage.apiKey'), 'fixture-b');
    for (const tab of tabs) {
      assert.equal(tab.url, '/self-usage');
      assert.equal(tab.opener, null);
    }
  } finally {
    globalThis.window = original;
  }
});

test('blocked windows and blocked storage fail without leaving a tab authenticated with another key', () => {
  const original = globalThis.window;
  try {
    globalThis.window = { open: () => null };
    assert.equal(openSelfUsagePage('fixture'), false);
    let closed = false;
    globalThis.window = {
      open: () => ({
        sessionStorage: {
          setItem() {
            throw new Error('disabled');
          },
        },
        close() {
          closed = true;
        },
      }),
    };
    assert.equal(openSelfUsagePage('fixture'), false);
    assert.equal(closed, true);
  } finally {
    globalThis.window = original;
  }
});
