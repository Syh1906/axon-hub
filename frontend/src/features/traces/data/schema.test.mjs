import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { z } from 'zod';

function loadModule(path, dependencies) {
  const source = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
  }).outputText;
  const exports = {};
  new Function('require', 'exports', source)((name) => {
    assert.ok(name in dependencies, `未隔离的依赖：${name}`);
    return dependencies[name];
  }, exports);
  return exports;
}

const pagination = loadModule('../../../gql/pagination.ts', { zod: { z } });
const { segmentSchema, parseRawRootSegment } = loadModule('./schema.ts', {
  zod: { z },
  '@/gql/pagination': pagination,
});

function segment(id, fields = {}) {
  return {
    id,
    model: 'test-model',
    duration: 1,
    startTime: '2026-11-07T00:00:00Z',
    endTime: '2026-11-07T00:00:00.001Z',
    ...fields,
  };
}

function chain(depth) {
  const root = segment(1);
  let tail = root;
  for (let id = 2; id <= depth; id++) {
    const child = segment(id, { parentId: id - 1 });
    tail.children = [child];
    tail = child;
  }
  return { root, tail };
}

function assertCompleteChain(root, depth) {
  let current = root;
  for (let id = 1; id <= depth; id++) {
    assert.equal(current.id, id);
    assert.ok(current.startTime instanceof Date);
    assert.ok(current.endTime instanceof Date);
    assert.deepEqual(current.requestSpans, []);
    assert.deepEqual(current.responseSpans, []);
    if (id === depth) {
      assert.deepEqual(current.children, []);
    } else {
      assert.equal(current.children.length, 1);
      current = current.children[0];
    }
  }
}

test('1193 层追踪对象与 JSON 字符串都保留到最后一个请求', () => {
  const { root } = chain(1193);
  assertCompleteChain(parseRawRootSegment(root), 1193);
  assertCompleteChain(parseRawRootSegment(JSON.stringify(root)), 1193);
});

test('更长的合法单链不依赖 JavaScript 调用栈深度', () => {
  assertCompleteChain(segmentSchema.parse(chain(5000).root), 5000);
});

test('分支顺序、非空 span、日期转换和 null 字段保持不变', () => {
  const rawSpan = {
    id: 'tool-call',
    type: 'tool_use',
    startTime: '2026-11-07T00:00:00Z',
    endTime: '2026-11-07T00:00:00.001Z',
    value: { toolUse: { name: 'test-tool', arguments: '{}' } },
  };
  const raw = segment(1, {
    metadata: { inputTokens: 2, outputTokens: 3, totalTokens: 5 },
    requestSpans: [rawSpan],
    children: [
      segment(2, { parentId: null, metadata: null, requestSpans: null, responseSpans: null, children: null }),
      segment(3, { responseSpans: [rawSpan], unknownField: true }),
    ],
  });
  const parsed = segmentSchema.parse(raw);
  assert.deepEqual(parsed.children.map((child) => child.id), [2, 3]);
  assert.deepEqual(parsed.metadata, raw.metadata);
  assert.equal(parsed.requestSpans[0].value.toolUse.name, 'test-tool');
  assert.ok(parsed.requestSpans[0].startTime instanceof Date);
  assert.ok(parsed.children[1].responseSpans[0].endTime instanceof Date);
  for (const field of ['parentId', 'metadata', 'requestSpans', 'responseSpans', 'children']) {
    assert.equal(parsed.children[0][field], null, field);
  }
  assert.deepEqual(parsed.children[1].requestSpans, []);
  assert.deepEqual(parsed.children[1].children, []);
  assert.equal(Object.hasOwn(parsed.children[1], 'unknownField'), false);
  assert.notEqual(parsed, raw);
  assert.notEqual(parsed.children, raw.children);
  assert.equal(raw.requestSpans[0].startTime, '2026-11-07T00:00:00Z');
  assert.equal(Object.hasOwn(raw.children[1], 'children'), false);
});

test('深层字段和 span 仍执行真实校验，错误路径包含完整父子位置', () => {
  const { root, tail } = chain(1193);
  tail.model = 42;
  tail.responseSpans = [{ id: 'invalid-tool', type: 'tool_use', value: { toolUse: {} } }];
  const result = segmentSchema.safeParse(root);
  assert.equal(result.success, false);
  assert.ok(result.error instanceof z.ZodError);
  const prefix = Array.from({ length: 1192 }, () => ['children', 0]).flat();
  assert.deepEqual(result.error.issues.map((issue) => issue.path), [
    [...prefix, 'model'],
    [...prefix, 'responseSpans', 0, 'value', 'toolUse', 'name'],
  ]);
  assert.throws(() => parseRawRootSegment(root), z.ZodError);
  assert.equal(parseRawRootSegment(JSON.stringify(root)), null);
});

test('节点和 children 的非法结构仍然拒绝，缺省数组仍然补齐', () => {
  for (const children of [{}, 'invalid', [null], [42]]) {
    assert.equal(segmentSchema.safeParse(segment(1, { children })).success, false);
  }
  for (const field of ['id', 'model', 'duration', 'startTime', 'endTime']) {
    const invalid = segment(1);
    delete invalid[field];
    assert.equal(segmentSchema.safeParse(invalid).success, false, field);
  }
  assert.equal(segmentSchema.safeParse(segment(1, { startTime: 'invalid-date' })).success, false);
  assertCompleteChain(segmentSchema.parse(segment(1)), 1);
});

test('空值、损坏 JSON 和不支持的根类型沿用现有解析行为', () => {
  for (const raw of [null, undefined, '', false, 0, '{']) {
    assert.equal(parseRawRootSegment(raw), null);
  }
  assert.throws(() => parseRawRootSegment(42), /Invalid rawRootSegment type/);
  assert.throws(() => parseRawRootSegment([]), z.ZodError);
  assert.equal(parseRawRootSegment('{}'), null);
});
