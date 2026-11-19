// Structural guard: every `settings { ... }` selection block in channels.ts must
// request `quotaRoutingMode`, so a new or edited query/mutation cannot silently
// drop the field and starve the zod schema / merge whitelist downstream.
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { mergeChannelSettingsForUpdate } from '../utils/merge.ts';

const source = ['channels.ts', 'channel-create-operations.ts'].map((file) => readFileSync(join(import.meta.dirname, file), 'utf8')).join('\n');
const lines = source.split('\n');

function enclosingDeclaration(lineIndex) {
  for (let i = lineIndex; i >= 0; i--) {
    const m = lines[i].match(/const (\w+)\s*=/);
    if (m) return m[1];
  }
  return `line ${lineIndex + 1}`;
}

function extractSettingsBlocks() {
  const blocks = [];
  for (let i = 0; i < lines.length; i++) {
    const open = lines[i].indexOf('settings {');
    if (open === -1) continue;
    let depth = 0;
    let body = '';
    for (let j = i; j < lines.length; j++) {
      const line = j === i ? lines[j].slice(open) : lines[j];
      for (const ch of line) {
        if (ch === '{') depth++;
        else if (ch === '}') depth--;
      }
      body += `${line}\n`;
      if (depth === 0) break;
    }
    blocks.push({ name: enclosingDeclaration(i), body });
  }
  return blocks;
}

test('every settings selection block in channels.ts requests quotaRoutingMode', () => {
  const blocks = extractSettingsBlocks();
  assert.ok(blocks.length >= 1, 'no `settings {` blocks found — selection extraction is broken');

  // Pricing reads only the multiplier and never feed a channel settings write.
  const missing = blocks.filter((b) => b.name !== 'GET_CHANNEL_MODEL_PRICES_QUERY' && !/\bquotaRoutingMode\b/.test(b.body));
  assert.deepEqual(
    missing.map((b) => b.name),
    [],
    `settings selection block(s) missing quotaRoutingMode: ${missing.map((b) => b.name).join(', ')}`
  );
});

test('渠道请求设置的读写返回都包含模式覆写', () => {
  const blocks = extractSettingsBlocks().filter((block) => /\ballowImageGenerationTool\b/.test(block.body));
  assert.ok(blocks.length > 0);
  for (const block of blocks) {
    assert.match(block.body, /\bresponsesServiceTierOverride\b/, block.name);
    assert.match(block.body, /serviceTierBilling\s*\{\s*enabled basis\s*\}/, block.name);
    assert.match(block.body, /responsesTierSupport\s*\{\s*fast ultrafast allowDowngrade\s*\}/, block.name);
  }
  const selections = readFileSync(join(import.meta.dirname, 'channel-query-fields.ts'), 'utf8');
  assert.equal((selections.match(/\ballowImageGenerationTool\b/g) ?? []).length,
    (selections.match(/\bresponsesServiceTierOverride\b/g) ?? []).length);
});

test('清空超时和保存其他设置保留模式，恢复默认保留通用覆盖规则', () => {
  const rules = [{ op: 'set', path: 'service_tier', value: 'ultrafast' }];
  const existing = { responsesServiceTierOverride: 'FAST', bodyOverrideOperations: rules, streamFirstEventTimeoutSeconds: 120 };
  const clearTimeout = mergeChannelSettingsForUpdate(existing, { streamFirstEventTimeoutSeconds: null });
  assert.equal(clearTimeout.responsesServiceTierOverride, 'FAST');
  assert.equal(clearTimeout.streamFirstEventTimeoutSeconds, null);
  const reset = mergeChannelSettingsForUpdate(existing, { responsesServiceTierOverride: 'INHERIT' });
  assert.equal(reset.responsesServiceTierOverride, 'INHERIT');
  assert.deepEqual(reset.bodyOverrideOperations, rules);
  assert.equal(reset.streamFirstEventTimeoutSeconds, 120);
  assert.equal(mergeChannelSettingsForUpdate(undefined, {}).responsesServiceTierOverride, 'INHERIT');
  const standardOnly = { fast: false, ultrafast: false, allowDowngrade: false };
  const configured = mergeChannelSettingsForUpdate(existing, { responsesServiceTierOverride: 'SUPPORTED', responsesTierSupport: standardOnly });
  assert.deepEqual(mergeChannelSettingsForUpdate(configured, { streamFirstEventTimeoutSeconds: null }).responsesTierSupport, standardOnly);
});


test('协议策略在其他设置保存时保留，显式恢复默认可清除限制', () => {
  const policy = { allowedProtocols: ['anthropic/messages'], sameProtocolOnly: true };
  const updated = mergeChannelSettingsForUpdate({ protocolPolicy: policy, responsesServiceTierOverride: 'FAST' }, { disableSessionReuse: true });
  assert.deepEqual(updated.protocolPolicy, policy);
  const reset = mergeChannelSettingsForUpdate(updated, { protocolPolicy: { allowedProtocols: [], sameProtocolOnly: false } });
  assert.equal(reset.responsesServiceTierOverride, 'FAST');
  assert.deepEqual(reset.protocolPolicy, { allowedProtocols: [], sameProtocolOnly: false });
});
