import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

// 执行当前 fork 共用的 Token 单元格，验证合并后仍保留明细与计数口径。
const source = readFileSync(new URL('../../../components/request-usage-cells.tsx', import.meta.url), 'utf8');
const ast = ts.createSourceFile('request-usage-cells.tsx', source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const node = ast.statements.find((item) => ts.isFunctionDeclaration(item) && item.name?.text === 'TokensCell');
assert.ok(node);
const output = ts.transpileModule(node.getText(ast), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023, jsx: ts.JsxEmit.React },
}).outputText;
const prelude = `import React from '${import.meta.resolve('react')}';
const useTranslation = () => ({ t: (key) => key, i18n: { language: 'en' } });
const useState = () => [false, () => {}];
const HoverCard = 'hover-card', HoverCardTrigger = 'trigger', HoverCardContent = 'content';`;
const { TokensCell } = await import(`data:text/javascript;base64,${Buffer.from(prelude + output).toString('base64')}`);
function text(node) {
  if (node == null || typeof node === 'boolean') return '';
  if (Array.isArray(node)) return node.map(text).join('');
  return typeof node === 'object' ? text(node.props.children) : String(node);
}
function render(values) { return TokensCell({ prompt: 1000, completion: 50, cached: 250, reasoning: 7, writeCached: 0, ...values }); }

test('请求列沿用共用 Token 单元格', () => {
  const columns = readFileSync(new URL('./requests-columns.tsx', import.meta.url), 'utf8');
  assert.match(columns, /<TokensCell/);
  assert.match(columns, /cached=\{log\.promptCachedTokens/);
});
test('输入包含缓存，总量只计输入和输出，明细显示净输入及推理', () => {
  const output = text(render({}));
  assert.match(output, /requests.columns.totalTokens1,050/);
  assert.match(output, /requests.tokenDetails.newInput: 750/);
  assert.match(output, /requests.columns.reasoning: 7/);
  assert.match(output, /requests.tokenDetails.cacheHit250/);
});
test('无推理时隐藏推理摘要，零输入不产生 NaN', () => {
  const output = text(render({ prompt: 0, cached: 0, completion: 0, reasoning: 0 }));
  assert.doesNotMatch(output, /requests.columns.reasoning|NaN|Infinity/);
  assert.match(output, /requests.columns.totalTokens0/);
});
test('缓存写入在明细中单独展示，不重复加入总量', () => {
  const output = text(render({ writeCached: 100 }));
  assert.match(output, /requests.tokenDetails.cacheWrite100/);
  assert.match(output, /requests.columns.totalTokens1,050/);
});
