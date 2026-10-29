import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./utils/bulk-associations.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { appendAssociations, clampAssociationPriority, MAX_ASSOCIATION_PRIORITY, buildBulkAssociations } = await import(
  `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
);
const rule = (channelId, modelId) => ({ type: 'channel_model', priority: 0, disabled: false, channelModel: { channelId, modelId } });
test('append preserves original rules and skips repeated channel/model pairs', () => {
  const original = [rule(1, 'a')];
  const result = appendAssociations(original, [rule(1, 'a'), rule(2, 'a'), rule(2, 'a'), rule(2, 'b')]);
  assert.equal(original.length, 1);
  assert.deepEqual(result, [rule(1, 'a'), rule(2, 'a'), rule(2, 'b')]);
  assert.equal(result[0], original[0]);
});
test('disabled and conditional rules do not swallow a new unconditional association', () => {
  const disabled = { ...rule(1, 'a'), disabled: true };
  const conditional = { ...rule(2, 'a'), when: { enabled: true } };
  assert.equal(appendAssociations([disabled, conditional], [rule(1, 'a'), rule(2, 'a')]).length, 4);
});
test('tag ordering does not create duplicate rules; different target models remain distinct', () => {
  const a = { type: 'channel_tags_model', channelTagsModel: { channelTags: ['A', 'B'], modelId: 'a' } };
  const b = { type: 'channel_tags_model', channelTagsModel: { channelTags: ['B', 'A'], modelId: 'a' } };
  assert.equal(appendAssociations([a], [b]).length, 1);
  assert.equal(appendAssociations([a], [{ ...b, channelTagsModel: { ...b.channelTagsModel, modelId: 'b' } }]).length, 2);
});
test('batch supports more than ten rules', () =>
  assert.equal(
    appendAssociations(
      [],
      Array.from({ length: 30 }, (_, i) => rule(i, 'a'))
    ).length,
    30
  ));

test('bulk priority uses the existing editor range and whole numbers', () => {
  assert.equal(MAX_ASSOCIATION_PRIORITY, 10);
  assert.equal(clampAssociationPriority(11), 10);
  assert.equal(clampAssociationPriority(-1), 0);
  assert.equal(clampAssociationPriority(2.5), 2);
  assert.equal(clampAssociationPriority(0), 0);
});

const previewSource = ts.transpileModule(readFileSync(new URL('./utils/group-channel-models.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { groupChannelModels } = await import(`data:text/javascript;base64,${Buffer.from(previewSource).toString('base64')}`);
test('same-channel preview merges distinct models without mutating inputs or repeating keys', () => {
  const input = [
    { channel: { id: '1' }, models: [{ requestModel: 'a' }] },
    { channel: { id: '1' }, models: [{ requestModel: 'b' }, { requestModel: 'a' }] },
    { channel: { id: '2' }, models: [{ requestModel: 'a' }] },
  ];
  const copy = structuredClone(input);
  const output = groupChannelModels(input);
  assert.deepEqual(
    output.map((item) => [item.channel.id, item.models.map((model) => model.requestModel)]),
    [
      ['1', ['a', 'b']],
      ['2', ['a']],
    ]
  );
  assert.deepEqual(input, copy);
});
test('appending preserves all nested condition data, including disabled conditions', () => {
  const existing = {
    ...rule(1, 'a'),
    when: {
      enabled: false,
      condition: {
        type: 'group',
        conditions: [
          {
            type: 'group',
            conditions: [{ type: 'group', conditions: [{ type: 'condition', field: 'model', operator: 'eq', value: 'a' }] }],
          },
        ],
      },
    },
  };
  const snapshot = structuredClone(existing);
  const result = appendAssociations([existing], [rule(2, 'b')]);
  assert.deepEqual(result[0], snapshot);
  assert.deepEqual(existing, snapshot);
});

test('bulk builder supports all existing association types without freezing request models', () => {
  const common = { priority: 2, disabled: false };
  assert.deepEqual(
    buildBulkAssociations('channel_request', [3, 4], [], 'must-not-be-saved', 2),
    [3, 4].map((channelId) => ({ ...common, type: 'channel_request', channelRequest: { channelId } }))
  );
  assert.deepEqual(buildBulkAssociations('channel_model', [3], [], ' model-a ', 2), [
    { ...common, type: 'channel_model', channelModel: { channelId: 3, modelId: 'model-a' } },
  ]);
  assert.deepEqual(buildBulkAssociations('channel_regex', [3], [], '^gpt-', 2), [
    { ...common, type: 'channel_regex', channelRegex: { channelId: 3, pattern: '^gpt-' } },
  ]);
  assert.deepEqual(buildBulkAssociations('channel_tags_regex', [], ['one'], '^gpt-', 2), [
    { ...common, type: 'channel_tags_regex', channelTagsRegex: { channelTags: ['one'], pattern: '^gpt-' } },
  ]);
  assert.deepEqual(buildBulkAssociations('channel_tags_model', [], ['one'], 'gpt', 2), [
    { ...common, type: 'channel_tags_model', channelTagsModel: { channelTags: ['one'], modelId: 'gpt' } },
  ]);
  assert.deepEqual(buildBulkAssociations('model', [], [], 'gpt', 2), [{ ...common, type: 'model', modelId: { modelId: 'gpt' } }]);
  assert.deepEqual(buildBulkAssociations('regex', [], [], '^gpt-', 2), [{ ...common, type: 'regex', regex: { pattern: '^gpt-' } }]);
  assert.deepEqual(buildBulkAssociations('channel_tags_model', [], [], 'gpt', 2), []);
  assert.deepEqual(buildBulkAssociations('regex', [], [], '', 2), []);
});

test('all bulk rule types deduplicate but preserve exclusions and conditional rules', () => {
  for (const type of ['channel_request', 'channel_model', 'channel_regex', 'channel_tags_model', 'channel_tags_regex', 'model', 'regex']) {
    const rules = buildBulkAssociations(type, [3], ['a'], 'gpt', 0);
    assert.equal(appendAssociations(rules, rules).length, 1, type);
  }
  const limited = { type: 'regex', priority: 0, disabled: false, regex: { pattern: '^gpt-', exclude: [{ channelIds: [3] }] } };
  assert.equal(appendAssociations([limited], buildBulkAssociations('regex', [], [], '^gpt-', 0)).length, 2);
});
