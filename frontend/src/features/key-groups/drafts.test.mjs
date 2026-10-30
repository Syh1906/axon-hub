import assert from 'node:assert/strict';
import test from 'node:test';
import { importedGroup, materialize, newDraft, newKey, parseTable, csvCell } from './drafts.ts';
import { pendingKey, readPending, savePending } from './pending.ts';

test('pasted Excel and CSV retain quoted notes, commas and embedded newlines', () => {
  assert.deepEqual(parseTable('A\t研发\r\nB\t运维'), [
    ['A', '研发'],
    ['B', '运维'],
  ]);
  assert.deepEqual(parseTable('"A","note, one"\r\n"B","line 1\nline ""2"""'), [
    ['A', 'note, one'],
    ['B', 'line 1\nline "2"'],
  ]);
  assert.throws(() => parseTable('"A'), /unclosedQuote/);
  assert.equal(csvCell('a"b'), '"a""b"');
});
test('defaults change inherited rows without overwriting explicit overrides', () => {
  const defaults = { keys: [newKey('Codex'), newKey('Claude')] };
  const rows = [
    { ...newDraft(), name: 'A' },
    { ...newDraft(), name: 'B', overrides: { keys: [newKey('Only')] } },
    { ...newDraft(), name: 'skip', selected: false },
  ];
  assert.deepEqual(
    materialize(rows, defaults, true).map((r) => r.keys.length),
    [2, 1]
  );
  defaults.keys.push(newKey('Third'));
  assert.deepEqual(
    materialize(rows, defaults, true).map((r) => r.keys.length),
    [3, 1]
  );
  rows[1].overrides = undefined;
  assert.deepEqual(
    materialize(rows, defaults, true).map((r) => r.keys.length),
    [3, 3]
  );
});
test('same key name across target groups keeps stable independent row IDs', () => {
  const rows = [
    { ...newDraft(1), name: 'Codex' },
    { ...newDraft(2), name: 'Codex' },
  ];
  const result = materialize(rows, { keys: [{ ...newKey(), templateID: 7 }] }, false);
  assert.deepEqual(
    result.map((r) => r.groupRef),
    [1, 2]
  );
  assert.notEqual(result[0].rowID, result[1].rowID);
  assert.equal(result[1].keys[0].templateID, 7);
  assert.equal(result[1].keys[0].name, 'Codex');
});

test('存储不可写时收起弹窗仍保留原批次，账号和项目保持隔离', () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: {
      getItem() {
        throw new Error('storage disabled');
      },
      setItem() {
        throw new Error('storage disabled');
      },
      removeItem() {
        throw new Error('storage disabled');
      },
    },
  });
  const key = pendingKey('project-a', 'user-a');
  const batch = { id: 'original-operation', rows: [{ rowID: 'original-row', action: 'createGroup', name: 'A' }] };
  try {
    savePending(key, batch);
    assert.deepEqual(readPending(key), batch);
    assert.equal(readPending(pendingKey('project-a', 'user-b')), null);
    assert.equal(readPending(pendingKey('project-b', 'user-a')), null);
    savePending(key, null);
    assert.equal(readPending(key), null);
  } finally {
    if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
    else delete globalThis.sessionStorage;
  }
});

test('导入归属遵循整份清单列映射，单列只继承一个明确目标', () => {
  const groups = [{ id: 7, name: '使用者 A' }];
  const table = parseTable('Codex,使用者 A\nCLI');
  const width = Math.max(...table.map((row) => row.length));
  const resolve = (row, columns = width, targets = []) => importedGroup(row, columns, 1, targets, groups, '未分组');
  assert.deepEqual(resolve(table[0]), { groupRef: 7 });
  assert.deepEqual(resolve(table[1]), { error: 'kg.importGroupRequired' });
  assert.deepEqual(resolve(['CLI', '  ']), { error: 'kg.importGroupRequired' });
  assert.deepEqual(resolve(['CLI', '未分组']), { groupRef: 0 });
  assert.deepEqual(resolve(['CLI', '未知组']), { error: 'kg.unknownGroup', name: '未知组' });
  assert.deepEqual(resolve(['CLI'], 1), { error: 'kg.importTargetRequired' });
  assert.deepEqual(resolve(['CLI'], 1, [7, 8]), { error: 'kg.importTargetRequired' });
  assert.deepEqual(resolve(['CLI'], 1, [7]), { groupRef: 7 });
  assert.deepEqual(resolve(['CLI'], 1, [0]), { groupRef: 0 });
});
