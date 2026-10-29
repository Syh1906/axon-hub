import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./data/default-groups.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { defaultGroupsKey, readDefaultGroups, saveDefaultGroups, groupFilter, selectedGroupOptions } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
const values = new Map();
globalThis.localStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) };

test('defaults persist independently per user and project', () => {
  const key = defaultGroupsKey('user-1', 'project-1');
  saveDefaultGroups(key, [11, 22, 11, 0]);
  assert.deepEqual(readDefaultGroups(key), [11, 22, 0]);
  assert.deepEqual(readDefaultGroups(defaultGroupsKey('user-2', 'project-1')), []);
  assert.deepEqual(readDefaultGroups(defaultGroupsKey('user-1', 'project-2')), []);
  saveDefaultGroups(key, []);
  assert.deepEqual(readDefaultGroups(key), []);
});
test('filters distinguish all, ungrouped and any selected group', () => {
  assert.equal(groupFilter([]), undefined);
  assert.deepEqual(groupFilter([0]), { or: [{ groupRefIsNil: true }] });
  assert.deepEqual(groupFilter([11, 22, 0]), { or: [{ groupRefIn: [11, 22] }, { groupRefIsNil: true }] });
  assert.deepEqual(groupFilter([999]), { or: [{ groupRefIn: [999] }] });
});
test('invalid stored values are rejected and write errors propagate', () => {
  for (const text of ['bad', '{}', '[-1]', '["11"]', '[null]']) {
    values.set('invalid', text);
    assert.deepEqual(readDefaultGroups('invalid'), []);
  }
  const original = localStorage.setItem;
  localStorage.setItem = () => { throw new Error('storage unavailable'); };
  assert.throws(() => saveDefaultGroups('x', [11]), /storage unavailable/);
  localStorage.setItem = original;
});

test('removed defaults remain visible and removable without silently widening the filter', () => {
  const saved = [11, 22];
  const options = selectedGroupOptions(saved, [{ id: 22, name: 'retained' }], id => `unavailable #${id}`);
  assert.deepEqual(options, [{ id: 11, name: 'unavailable #11' }, { id: 22, name: 'retained' }]);
  assert.deepEqual(saved, [11, 22]);
  const draft = saved.filter(id => id !== options[0].id);
  saveDefaultGroups('removed-default', draft);
  assert.deepEqual(readDefaultGroups('removed-default'), [22]);
  assert.deepEqual(groupFilter(saved), { or: [{ groupRefIn: [11, 22] }] });
});
