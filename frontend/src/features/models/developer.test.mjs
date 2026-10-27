import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

function moduleURL(path, imports = {}) {
  const source = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
  }).outputText;
  const resolved = source.replace(
    /(from\s+['"])([^'"]+)(['"])/g,
    (_, prefix, name, suffix) => `${prefix}${imports[name] ?? import.meta.resolve(name)}${suffix}`
  );
  return `data:text/javascript;base64,${Buffer.from(resolved).toString('base64')}`;
}
const { normalizeModelDeveloper, resolveModelDeveloper } = await import(
  moduleURL('./data/developer.ts', {
    './constants': moduleURL('./data/constants.ts'),
  })
);

test('typed built-in developer names and labels resolve to the catalog ID', () => {
  const options = [
    { value: 'openai', label: 'OpenAI' },
    { value: 'alibaba', label: '阿里巴巴' },
  ];
  for (const value of ['OpenAI', 'openai', ' OPENAI ']) {
    assert.equal(resolveModelDeveloper(value, options), 'openai');
    assert.equal(normalizeModelDeveloper(value), 'openai');
  }
  assert.equal(resolveModelDeveloper('阿里巴巴', options), 'alibaba');
  assert.equal(normalizeModelDeveloper('Anthropic'), 'anthropic');
});

test('custom developer names preserve case and model-like identifiers', () => {
  for (const value of ['', 'MyProvider', 'myprovider', 'OpenAI-compatible', 'GPT-4o']) {
    assert.equal(normalizeModelDeveloper(value), value);
    assert.equal(resolveModelDeveloper(value, [{ value: 'openai', label: 'OpenAI' }]), value);
  }
});
