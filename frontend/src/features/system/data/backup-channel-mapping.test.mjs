import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('./backup-channel-mapping.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2023 },
}).outputText.replaceAll("'zod'", JSON.stringify(import.meta.resolve('zod')));
const { readBackupChannelReferences, backupChannelSourceKey, channelMappingSourceKey } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

test('新备份只读取渠道索引，保留同名渠道且不读取历史用量', async () => {
  let reads = 0;
  let cancelled = false;
  const refs = [{ id: 1, name: 'same', type: 'codex', tags: ['小为'], has_config: false }, { id: 2, name: 'same', type: 'codex', tags: ['裂魂'], has_config: false }];
  const file = {
    stream: () => new ReadableStream({
      pull(controller) {
        reads++;
        assert.equal(reads, 1, 'must not read the usage history');
        controller.enqueue(new TextEncoder().encode(`{"channel_references":${JSON.stringify(refs)}\n,"usage_logs":[`));
      },
      cancel() { cancelled = true; },
    }, { highWaterMark: 0 }),
    text: () => { throw new Error('must not load the whole backup'); },
  };
  assert.deepEqual(await readBackupChannelReferences(file), refs);
  assert.equal(reads, 1);
  assert.equal(cancelled, true);
});

test('旧备份仍能读取渠道，凭据不会进入选项数据', async () => {
  const file = new Blob([JSON.stringify({ version: '1.6', channels: [{ id: 7, name: 'legacy', type: 'openai', credentials: { api_key: 'fixture' } }] }, null, 2)]);
  assert.deepEqual(await readBackupChannelReferences(file), [{ id: 7, name: 'legacy', type: 'openai', tags: [], has_config: true }]);
});

test('无效备份报告解析失败', async () => {
  await assert.rejects(readBackupChannelReferences(new Blob(['{invalid}'])));
});

test('旧版纯价格备份提取名称来源，去重且不能新建渠道', async () => {
  const file = new Blob([JSON.stringify({ version: '1.6', channels: [], channel_model_prices: [
    { channel_name: 'same', model_id: 'one' }, { channel_name: 'same', model_id: 'two' }, { channel_name: 'other', model_id: 'one' },
  ] })]);
  const refs = await readBackupChannelReferences(file);
  assert.deepEqual(refs.map((r) => [r.name, r.id, r.has_config]), [['same', 0, false], ['other', 0, false]]);
  assert.notEqual(backupChannelSourceKey(refs[0]), backupChannelSourceKey(refs[1]));
  assert.equal(backupChannelSourceKey(refs[0]), channelMappingSourceKey({ sourceID: 0, sourceName: 'same', targetID: 2 }));
});

test('旧完整备份使用渠道 ID，不再为价格重复生成名称来源', async () => {
  const refs = await readBackupChannelReferences(new Blob([JSON.stringify({ version: '1.6', channels: [{ id: 7, name: 'same' }], channel_model_prices: [{ channel_name: 'same' }] })]));
  assert.equal(refs.length, 1);
  assert.equal(refs[0].id, 7);
  assert.equal(refs[0].has_config, true);
});
