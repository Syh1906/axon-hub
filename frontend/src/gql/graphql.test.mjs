import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import test from 'node:test';
import ts from 'typescript';

const sourcePath = join(import.meta.dirname, 'graphql.ts');
const source = readFileSync(sourcePath, 'utf8');
const transpiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2023,
  },
}).outputText
  .replaceAll("import { ensureFreshAccessToken } from '@/lib/auth-session';", 'const ensureFreshAccessToken = async () => "";')
  .replaceAll("import { toast } from 'sonner';", 'const toast = { error() {} };')
  .replaceAll("import { getTokenFromStorage, removeTokenFromStorage } from '@/stores/authStore';", 'const getTokenFromStorage = () => ""; const removeTokenFromStorage = () => {};')
  .replaceAll("import i18n from '@/lib/i18n';", 'const i18n = { t: (key) => key };')
  .replaceAll("'@/lib/read-deadline'", JSON.stringify(pathToFileURL(join(import.meta.dirname, '../lib/read-deadline.ts')).href));

const moduleUrl = `data:text/javascript;base64,${Buffer.from(transpiled).toString('base64')}`;
const { isUnauthorizedGraphQLError, graphqlRequest } = await import(moduleUrl);

test('does not classify upstream unauthorized provider failures as login expiration', () => {
  const error = {
    message: 'model fetch returned error: failed to fetch models: GET - https://provider.example/v1/models with status 401 Unauthorized',
    path: ['syncChannelModels'],
  };

  assert.equal(isUnauthorizedGraphQLError(error), false);
});

test('classifies explicit GraphQL authentication codes as login expiration', () => {
  assert.equal(isUnauthorizedGraphQLError({ extensions: { code: 'UNAUTHENTICATED' } }), true);
});

test('a stalled query times out without being mistaken for authentication failure', async (t) => {
  t.mock.method(globalThis, 'fetch', async (_url, { signal }) => new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  }));
  await assert.rejects(graphqlRequest('query Stats { stats }', undefined, undefined, { timeoutMs: 10 }),
    (error) => error.status === 408 && !error.isAuthError);
});

test('mutations keep their original signal and are not given a read deadline', async (t) => {
  const controller = new AbortController();
  t.mock.method(globalThis, 'fetch', async (_url, init) => {
    assert.equal(init.signal, controller.signal);
    return Response.json({ data: { saved: true } });
  });
  assert.deepEqual(await graphqlRequest('# update\nmutation Save { saved }', undefined, undefined,
    { signal: controller.signal, timeoutMs: 1 }), { saved: true });
});

test('the deadline includes a stalled response body', async (t) => {
  t.mock.method(globalThis, 'fetch', async (_url, { signal }) => ({
    ok: true, status: 200, headers: new Headers({ 'content-type': 'application/json' }),
    json: () => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(signal.reason), { once: true })),
  }));
  await assert.rejects(graphqlRequest('{ stats }', undefined, undefined, { timeoutMs: 10 }), (error) => error.status === 408);
});

const templatesSource = readFileSync(join(import.meta.dirname, '../features/channels/data/templates.ts'), 'utf8');
const templateDocument = (name) => templatesSource.match(new RegExp(`const ${name} = \x60([\\s\\S]*?)\x60;`))[1];
for (const name of ['CREATE_CHANNEL_OVERRIDE_TEMPLATE', 'UPDATE_CHANNEL_OVERRIDE_TEMPLATE']) {
  test(`${name} preserves write semantics with a leading fragment`, async (t) => {
    const query = templateDocument(name).replace('${TEMPLATE_FRAGMENT}', templateDocument('TEMPLATE_FRAGMENT'));
    const controller = new AbortController();
    t.mock.method(globalThis, 'fetch', async (_url, init) => {
      assert.equal(init.signal, controller.signal);
      assert.equal(JSON.parse(init.body).query, query);
      await new Promise(resolve => setTimeout(resolve, 10));
      return Response.json({ data: { saved: true } });
    });
    assert.deepEqual(await graphqlRequest(query, undefined, undefined, { signal: controller.signal, timeoutMs: 1 }), { saved: true });
  });
}

test('the real template listing still receives a read deadline', async (t) => {
  const query = templateDocument('QUERY_CHANNEL_OVERRIDE_TEMPLATES').replace('${TEMPLATE_FRAGMENT}', templateDocument('TEMPLATE_FRAGMENT'));
  t.mock.method(globalThis, 'fetch', async (_url, { signal }) => new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  }));
  await assert.rejects(graphqlRequest(query, undefined, undefined, { timeoutMs: 10 }), (error) => error.status === 408);
});
