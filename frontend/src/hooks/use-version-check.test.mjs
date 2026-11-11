import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = readFileSync(new URL('./use-version-check.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 } }).outputText;
const moduleSource = compiled.replace(/^import .*;\r?\n/gm, '') + `
const useEffect = () => {};
const useCallback = fn => fn;
const useAuthStore = fn => fn({ auth: { user: { isOwner: true } } });
const useQuery = options => { captured = options; return {}; };
const CHECK_FOR_UPDATE_QUERY = 'query CheckForUpdate { checkForUpdate { hasUpdate } }';
const storage = new Map();
const localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) };
let captured;
export let checks = 0;
async function graphqlRequest(_query, _variables, _headers, init) {
  checks++;
  if (!(init.signal instanceof AbortSignal)) throw new Error('missing cancellation');
  throw new Error('GitHub unavailable');
}
export function options() { useVersionCheck(); return captured; }
`;
const moduleUrl = `data:text/javascript;base64,${Buffer.from(moduleSource).toString('base64')}`;
const check = await import(moduleUrl);

test('failed automatic update checks record their attempt and do not restart on every page entry', async () => {
  const first = check.options();
  assert.equal(first.enabled, true);
  assert.deepEqual(first.queryKey, ['checkForUpdate', false]);
  const signal = new AbortController().signal;
  await assert.rejects(first.queryFn({ signal }), /GitHub unavailable/);
  assert.equal(check.options().enabled, false);
  assert.equal(check.checks, 1);
  assert.equal(check.versionCheckDue(String(Date.now() - 600_001)), true);
});
