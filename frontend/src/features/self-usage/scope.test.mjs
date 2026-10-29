import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./scope.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { sameUsageScope } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

test('log-only reassignment detects a different scope even when group names are reused', () => {
  const summary = { scopeID: 'group:1', apiKeyName: 'key · A', groupName: 'A' };
  assert.equal(sameUsageScope(summary, { ...summary }), true);
  assert.equal(sameUsageScope(summary, { ...summary, scopeID: 'group:2' }), false);
  assert.equal(sameUsageScope(summary, { ...summary, groupName: 'renamed' }), false);
  assert.equal(sameUsageScope(summary, { ...summary, apiKeyName: 'renamed key · A' }), false);
  assert.equal(sameUsageScope(summary, { scopeID: 'key:1', apiKeyName: 'key · owner', groupName: null }), false);
});
