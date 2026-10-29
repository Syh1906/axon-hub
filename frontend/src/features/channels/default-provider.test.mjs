import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./utils/default-provider.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { readDefaultProvider, saveDefaultProvider, resolveChannelProviderSelection } = await import(
  `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
);
const providers = { openai: 'openai', openai_responses: 'openai', anthropic: 'anthropic' };
let stored = null;
globalThis.localStorage = {
  getItem: () => stored,
  setItem: (_, next) => {
    stored = next;
  },
};

test('default provider survives re-entry and clear restores all', () => {
  assert.equal(readDefaultProvider(providers), 'all');
  saveDefaultProvider('openai');
  assert.equal(readDefaultProvider(providers), 'openai');
  saveDefaultProvider('openai:image');
  assert.equal(readDefaultProvider(providers), 'openai:image');
  saveDefaultProvider('all');
  assert.equal(readDefaultProvider(providers), 'all');
});

test('invalid or unavailable preferences cannot become provider filters', () => {
  for (const value of ['bad json', '{}', 'null', '[]', '42', '""', '"removed"', '"removed:image"']) {
    stored = value;
    assert.equal(readDefaultProvider(providers), 'all');
  }
});

test('provider filtering resolves all protocols without waiting for channel counts', () => {
  assert.deepEqual(resolveChannelProviderSelection('openai', providers), {
    provider: 'openai',
    types: ['openai', 'openai_responses'],
    isImage: false,
  });
  assert.deepEqual(resolveChannelProviderSelection('openai:image', providers), {
    provider: 'openai',
    types: ['openai', 'openai_responses'],
    isImage: true,
  });
  assert.equal(resolveChannelProviderSelection('all', providers), undefined);
});

test('storage failure is surfaced on save and safely ignored on read', () => {
  const storage = globalThis.localStorage;
  globalThis.localStorage = {
    getItem: () => {
      throw new Error('storage unavailable');
    },
    setItem: () => {
      throw new Error('storage unavailable');
    },
  };
  try {
    assert.equal(readDefaultProvider(providers), 'all');
    assert.throws(() => saveDefaultProvider('openai'), /storage unavailable/);
  } finally {
    globalThis.localStorage = storage;
  }
});
