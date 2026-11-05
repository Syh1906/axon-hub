import assert from 'node:assert/strict';
import test from 'node:test';
import { buildCostBreakdown, inputCostSubtotal } from './request-usage-cost-breakdown.ts';

const item = (itemCode, quantity, subtotal, variant = null, tiers = []) => ({ itemCode, variant, quantity, subtotal, tiers });
const breakdown = (items, multiplier = 1) => ({ items, multiplier });

test('input overview sums modality costs and keeps missing prices unknown', () => {
  const items = [item('prompt_text_tokens', 600, 0.003), item('prompt_image_tokens', 1200, 0.0096),
    item('prompt_text_cached_tokens', 400, 0.0005), item('prompt_image_cached_tokens', 800, 0.0016)];
  assert.ok(Math.abs(inputCostSubtotal(items) - 0.0126) < 1e-12);
  assert.ok(Math.abs(inputCostSubtotal(items, true) - 0.0021) < 1e-12);
  assert.equal(inputCostSubtotal([item('prompt_tokens', 1000, 0.005)]), 0.005);
  assert.equal(inputCostSubtotal([item('prompt_cached_tokens', 1000, 0.001)], true), 0.001);
  assert.equal(inputCostSubtotal([{ ...items[0], unpriced: true }, items[1]]), null);
  assert.equal(inputCostSubtotal([{ ...items[0], image: { width: 1024 } }]), null);
  assert.equal(inputCostSubtotal([]), null);
});

test('image unit fees stay per image and unknown images stay unpriced', () => {
  const image = { width: 2048, height: 2048, tier: '2K', source: 'file' };
  const result = buildCostBreakdown(0.04, breakdown([
    { ...item('prompt_tokens', 1, 0.04), image, imageRule: '2K' },
    { ...item('prompt_tokens', 0, 0), unpriced: true },
  ]));
  assert.equal(result.items[0].unitPrice, 0.04);
  assert.equal(result.items[1].unitPrice, null);
  assert.equal(result.items[1].unpriced, true);
});

test('orders known items and preserves other item order while omitting zero items', () => {
  const rows = buildCostBreakdown(4, breakdown([
    item('audio_tokens', 3, 1), item('prompt_write_cached_tokens', 1, 1, 'one_hour'),
    item('completion_tokens', 2, 1), item('zero', 0, 0), item('prompt_tokens', 4, 1),
    item('prompt_cached_tokens', 5, 0), item('image_tokens', 0, 1),
  ]));
  assert.deepEqual(rows.items.map(({ itemCode }) => itemCode), [
    'prompt_tokens', 'completion_tokens', 'prompt_cached_tokens', 'prompt_write_cached_tokens', 'audio_tokens', 'image_tokens',
  ]);
});

test('fixed request fee is not multiplied into a token unit price', () => {
  const result = buildCostBreakdown(0.09, breakdown([{ ...item('usage', 1, 0.09), pricingEvidence: {
    ...evidence('standard', 1, 0.09), ruleSource: 'non_token', modeSubtotal: 0.09,
  } }]));
  assert.equal(result.items[0].unitPrice, 0.09);
});

test('unit price uses the stored subtotal per million tokens and is absent when quantity is zero', () => {
  const rows = buildCostBreakdown(0.75, breakdown([item('prompt_tokens', 250_000, 0.5), item('completion_tokens', 0, 0.25)]));
  assert.equal(rows.items[0].unitPrice, 2);
  assert.equal(rows.items[1].unitPrice, null);
});

test('base total derives from every known nonzero multiplier, including 1', () => {
  const cases = [[1, 2], [2, 1], [0.5, 4]];
  for (const [multiplier, baseTotal] of cases) {
    const rows = buildCostBreakdown(2, breakdown([], multiplier));
    assert.equal(rows.multiplier, multiplier);
    assert.equal(rows.baseTotal, baseTotal);
    assert.equal(rows.total, 2);
  }
  for (const [total, multiplier] of [[0, 0], [2, 'mixed']]) {
    const rows = buildCostBreakdown(total, breakdown([], multiplier));
    assert.equal(rows.multiplier, multiplier);
    assert.equal(rows.baseTotal, null);
  }
});

test('only multi-segment tiers receive display rows and per-tier unit prices', () => {
  const single = item('prompt_tokens', 10, 1, null, [{ upTo: null, units: 10, subtotal: 1 }]);
  const tiered = item('completion_tokens', 30, 3, null, [
    { upTo: 10, units: 10, subtotal: 0.5 }, { upTo: null, units: 20, subtotal: 2.5 },
  ]);
  const rows = buildCostBreakdown(4, breakdown([single, tiered]));
  assert.deepEqual(rows.items[0].tiers, []);
  assert.deepEqual(rows.items[1].tiers, [
    { upTo: 10, units: 10, subtotal: 0.5, unitPrice: 50_000 },
    { upTo: null, units: 20, subtotal: 2.5, unitPrice: 125_000 },
  ]);
});

const evidence = (mode = 'fast', factor = 2, base = 1.002) => ({
  serviceTier: { mode, basis: 'upstream', client: 'priority', outbound: 'priority', upstream: 'priority', confirmed: true, reason: 'upstream', source: 'custom', referenceModel: '', version: '' },
  context: { kind: 'volume', inputTokens: 300000, threshold: 272000, longContext: true, name: '' },
  baseSubtotal: base, modeMultiplier: factor, costMultiplier: 0.8,
});
test('frozen base cost survives zero multipliers and context with Fast', () => {
  for (const factor of [0, 2]) {
    const rows = buildCostBreakdown(1.002 * factor * .8, breakdown([{ ...item('prompt_tokens', 240000, 1.002 * factor * .8), pricingEvidence: evidence('fast', factor) }], .8));
    assert.equal(rows.baseTotal, 1.002);
    assert.equal(rows.modeMultiplier, factor);
    assert.equal(rows.evidence.context.longContext, true);
    assert.equal(rows.items[0].unitPrice, 4.175);
  }
});
test('mixed records keep each frozen basis and unresolved rates stay unknown', () => {
  const rows = buildCostBreakdown(1, breakdown([
    { ...item('prompt_tokens', 100, 1), pricingEvidence: evidence() },
    { ...item('prompt_tokens', 100, 0), unpriced: true, pricingEvidence: evidence('ultrafast', null) },
  ]));
  assert.equal(rows.evidence, 'mixed');
  assert.equal(rows.modeMultiplier, 'mixed');
  assert.equal(rows.items[1].pricingEvidence.serviceTier.mode, 'ultrafast');
  assert.equal(rows.items[1].unitPrice, null);
});

test('zero mode retains tier base prices without dividing by zero', () => {
  const ev={...evidence('fast',0,3),baseTierSubtotals:[1,2]};
  const rows=buildCostBreakdown(0,breakdown([{...item('prompt_tokens',30,0,null,[{upTo:10,units:10,subtotal:0},{upTo:null,units:20,subtotal:0}]),pricingEvidence:ev}]));
  assert.deepEqual(rows.items[0].tiers.map(t=>t.subtotal),[1,2]);
  assert.deepEqual(rows.items[0].tiers.map(t=>t.unitPrice),[100000,100000]);
});

test('itemized evidence uses frozen mode subtotals and never labels an absolute price unconfigured', () => {
  const first = { ...evidence(), itemMethod: 'multiplier', modeMultiplier: 2, modeSubtotal: 1.696008, finalUnitPrice: 8 };
  const second = { ...evidence(), itemMethod: 'unit_price', modeMultiplier: null, modeSubtotal: 0.018, finalUnitPrice: 0.3 };
  const rows = buildCostBreakdown(1.3712064, breakdown([
    { ...item('prompt_tokens', 212001, 1.3568064), pricingEvidence: first },
    { ...item('prompt_cached_tokens', 60000, 0.0144), pricingEvidence: second },
  ], 0.8));
  assert.equal(rows.itemized, true);
  assert.equal(rows.hasModeSubtotals, true);
  assert.equal(rows.evidence.serviceTier.mode, 'fast');
  assert.equal(rows.items[1].unitPrice, 0.3);
  assert.equal(rows.modeTotal, 1.714008);
});


test('text and image inputs show independent token quantities and prices', () => {
  const result = buildCostBreakdown(0.051, breakdown([
    item('completion_tokens', 1000, 0.03),
    item('prompt_image_tokens', 2000, 0.016),
    item('prompt_text_tokens', 1000, 0.005),
    { ...item('prompt_image_cached_tokens', 0, 0), unpriced: true, unpricedReason: 'input_cache_modality_missing' },
  ]));
  assert.deepEqual(result.items.map(({ itemCode }) => itemCode), ['prompt_text_tokens', 'prompt_image_tokens', 'completion_tokens', 'prompt_image_cached_tokens']);
  assert.deepEqual(result.items.map(({ unitPrice }) => unitPrice), [5, 8, 30, null]);
});
