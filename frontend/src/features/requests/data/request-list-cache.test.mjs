import assert from 'node:assert/strict';
import test from 'node:test';
import { shareRequestList } from './request-list-cache.ts';

const row = (id, tokens = 1) => ({ id, createdAt: new Date('2026-10-31T00:00:00Z'), usageLogs: { tokens } });
const page = (...nodes) => ({ edges: nodes.map((node) => ({ node, cursor: node.id })), totalCount: 100 });

test('unchanged poll reuses the entire connection including parsed dates', () => {
  const previous = page(row('a'));
  assert.equal(shareRequestList(previous, page(row('a'))), previous);
});
test('new head reuses shifted rows and updates nested usage even without a changed date', () => {
  const previous = page(row('a'), row('b'));
  const next = shareRequestList(previous, page(row('new'), row('a'), row('b', 2)));
  assert.equal(next.edges[1].node, previous.edges[0].node);
  assert.notEqual(next.edges[2].node, previous.edges[1].node);
  assert.equal(next.edges[2].node.usageLogs.tokens, 2);
});
test('appending history keeps existing pages and records stable', () => {
  const previous = { pages: [page(row('a'))], pageParams: [null] };
  const next = shareRequestList(previous, { pages: [page(row('a')), page(row('b'))], pageParams: [null, 'a'] });
  assert.equal(next.pages[0], previous.pages[0]);
  assert.equal(next.pages[1].edges[0].node.id, 'b');
});
