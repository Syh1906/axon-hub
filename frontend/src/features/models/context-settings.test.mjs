import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

function moduleURL(path, imports = {}) {
  const source = ts
    .transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
    })
    .outputText.replace(
      /(from\s+['"])([^'"]+)(['"])/g,
      (_, prefix, name, suffix) => `${prefix}${imports[name] ?? import.meta.resolve(name)}${suffix}`
    );
  return `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`;
}
const contextURL = moduleURL('./data/context-settings.ts');
const { modelContextSettingsSchema } = await import(contextURL);
const channelsURL = moduleURL('../channels/data/schema.ts', {
  '@/gql/pagination': moduleURL('../../gql/pagination.ts'),
  '@/features/models/data/context-settings': contextURL,
});
const { channelSettingsInputSchema } = await import(channelsURL);
const { mergeChannelSettingsForUpdate } = await import(moduleURL('../channels/utils/merge.ts'));

test('context configuration validates values without persisting inherited defaults', () => {
  assert.deepEqual(modelContextSettingsSchema.parse({ maxContextWindow: 872000 }), { maxContextWindow: 872000 });
  for (const value of [0, -1, 1.5, NaN, 2147483648]) {
    assert.equal(modelContextSettingsSchema.safeParse({ maxContextWindow: value }).success, false);
  }
  assert.equal(modelContextSettingsSchema.safeParse({ defaultContextWindow: 500000, maxContextWindow: 272000 }).success, false);
});

test('channel save and unrelated settings patches preserve context values and explicit clearing', () => {
  const saved = channelSettingsInputSchema.parse({ modelContexts: [{ modelId: 'gpt-6.1-sol', maxContextWindow: 872000 }] });
  const updated = mergeChannelSettingsForUpdate(saved, { disableSessionReuse: true });
  assert.deepEqual(updated.modelContexts, saved.modelContexts);
  const cleared = channelSettingsInputSchema.parse(mergeChannelSettingsForUpdate(updated, { modelContexts: [] }));
  assert.deepEqual(cleared.modelContexts, []);
  assert.equal(cleared.disableSessionReuse, true);
});

test('image tool permission defaults to enabled and survives unrelated channel patches', () => {
  assert.equal(mergeChannelSettingsForUpdate(undefined, {}).allowImageGenerationTool, true);
  const disabled = channelSettingsInputSchema.parse(mergeChannelSettingsForUpdate(null, { allowImageGenerationTool: false }));
  assert.equal(disabled.allowImageGenerationTool, false);
  const unrelated = channelSettingsInputSchema.parse(mergeChannelSettingsForUpdate(disabled, { disableSessionReuse: true }));
  assert.equal(unrelated.allowImageGenerationTool, false);
  const enabled = channelSettingsInputSchema.parse(mergeChannelSettingsForUpdate(unrelated, { allowImageGenerationTool: true }));
  assert.equal(enabled.allowImageGenerationTool, true);
  assert.equal(enabled.disableSessionReuse, true);
});
