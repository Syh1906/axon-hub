import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./utils/default-tags.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { readDefaultTags, saveDefaultTags, resolveChannelTagSelection } = await import(
  `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
);
let value = null;
globalThis.localStorage = {
  getItem: () => value,
  setItem: (_, next) => {
    value = next;
  },
};
test('empty defaults and explicit clear show all tags', () => {
  value = null;
  assert.deepEqual(readDefaultTags(), []);
  saveDefaultTags(['A']);
  saveDefaultTags([]);
  assert.deepEqual(readDefaultTags(), []);
});
test('single and multiple defaults round trip without duplicate tags', () => {
  saveDefaultTags(['A']);
  assert.deepEqual(readDefaultTags(), ['A']);
  saveDefaultTags(['A', 'B', 'A']);
  assert.deepEqual(readDefaultTags(), ['A', 'B']);
});
test('invalid stored preference cannot become a filter', () => {
  for (const invalid of ['bad json', '{}', '[1]', '["A",null]']) {
    value = invalid;
    assert.deepEqual(readDefaultTags(), []);
  }
});

test('multiple tags match any and zero matches never dispatch an unfiltered query', () => {
  const channels = [
    { id: '1', tags: ['A'] },
    { id: '2', tags: ['B', 'A'] },
    { id: '3', tags: ['C'] },
  ];
  assert.deepEqual(resolveChannelTagSelection(['A', 'B'], channels), { ids: ['1', '2'], disableQuery: false });
  assert.deepEqual(resolveChannelTagSelection(['deleted', 'renamed'], channels), { ids: [], disableQuery: true });
  assert.equal(resolveChannelTagSelection(['A', 'B']).disableQuery, true);
  assert.equal(resolveChannelTagSelection([], channels).disableQuery, false);
  assert.equal(resolveChannelTagSelection(['A'], channels).disableQuery, false);
});
