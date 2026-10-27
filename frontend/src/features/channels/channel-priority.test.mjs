import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./utils/channel-priority.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext },
}).outputText;
const { channelDisplayPriority } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);
test('channel priority displays existing tag defaults without a filter', () => {
  const channel = { tags: ['Codex'], settings: { tagPriorities: [{ tag: 'Codex', priority: 14 }] } };
  assert.deepEqual(channelDisplayPriority(channel), { tag: 'Codex', priority: 14 });
});
test('multiple tags show the minimum current-tag value and explicit filters select their own value', () => {
  const channel = {
    tags: ['Codex', 'Shared'],
    settings: {
      tagPriorities: [
        { tag: 'Codex', priority: 14 },
        { tag: 'Shared', priority: 0 },
        { tag: 'Removed', priority: -10 },
      ],
    },
  };
  assert.deepEqual(channelDisplayPriority(channel), { tag: 'Shared', priority: 0 });
  assert.deepEqual(channelDisplayPriority(channel, 'Codex'), { tag: 'Codex', priority: 14 });
});
test('missing tag defaults remain inherited rather than becoming zero', () => {
  assert.deepEqual(channelDisplayPriority({ tags: ['Codex'] }), { tag: 'Codex', priority: undefined });
  assert.deepEqual(channelDisplayPriority({ tags: [] }), { tag: '', priority: undefined });
});
