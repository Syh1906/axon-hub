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

const paginationURL = moduleURL('../../gql/pagination.ts');
const schemaURL = moduleURL('../channels/data/schema.ts', { '@/gql/pagination': paginationURL });
const pricingURL = moduleURL('./data/pricing.ts', { '@/features/channels/data/schema': schemaURL });
const { multiplyPriceItems, createPriceValidationSchema, priceFromCatalog, hasPriceConfiguration } = await import(pricingURL);
const { modelCardFromCatalog } = await import(
  moduleURL('./data/catalog-card.ts', { './pricing': pricingURL, './providers.schema': moduleURL('./data/providers.schema.ts') })
);
const priceSchema = createPriceValidationSchema((key) => key);
const item = (itemCode, amount) => ({ itemCode, pricing: { mode: 'usage_per_unit', usagePerUnit: amount } });

test('catalog auto-fill preserves zero prices, edited items, tiers and schedules', () => {
  assert.equal(hasPriceConfiguration(undefined), false);
  assert.equal(hasPriceConfiguration({ items: [], volumeTiers: [], schedule: null }), false);
  assert.equal(hasPriceConfiguration({ items: [item('prompt_tokens', '0')] }), true);
  assert.equal(hasPriceConfiguration({ items: [item('prompt_tokens', '')] }), true);
  assert.equal(hasPriceConfiguration({ items: [], volumeTiers: [{ above: 200000, items: [] }] }), true);
  assert.equal(hasPriceConfiguration({ items: [], schedule: { timezone: 'UTC', overrides: [] } }), true);
});

test('bulk tier filling preserves decimal prices and cache variants without mutating the base', () => {
  const base = [
    item('prompt_tokens', '0.1'),
    item('completion_tokens', '1e-7'),
    {
      ...item('prompt_write_cached_tokens', '0'),
      promptWriteCacheVariants: [{ variantCode: 'one_hour', pricing: item('', '0.123456789012345678901').pricing }],
    },
  ];
  const original = structuredClone(base);
  const filled = multiplyPriceItems(base, '3');
  assert.equal(filled[0].pricing.usagePerUnit, '0.3');
  assert.equal(filled[1].pricing.usagePerUnit, '0.0000003');
  assert.equal(filled[2].pricing.usagePerUnit, '0');
  assert.equal(filled[2].promptWriteCacheVariants[0].pricing.usagePerUnit, '0.370370367037037036703');
  assert.deepEqual(base, original);
  assert.equal(
    multiplyPriceItems([item('prompt_tokens', '0.1')], '1.00000000000000000001')[0].pricing.usagePerUnit,
    '0.100000000000000000001'
  );
});

test('anthropic catalog imports bill one-hour cache writes at twice the input price', () => {
  const oneHour = (items) =>
    items
      .find((entry) => entry.itemCode === 'prompt_write_cached_tokens')
      .promptWriteCacheVariants?.map(({ variantCode, pricing }) => [variantCode, pricing.usagePerUnit]);
  const model = {
    cost: {
      input: 0.3,
      output: 15,
      cache_read: 0.03,
      cache_write: 0.375,
      tiers: [{ input: 6, cache_write: 7.5, tier: { type: 'context', size: 200000 } }],
    },
  };
  const price = priceFromCatalog(model, 'anthropic');
  assert.deepEqual(oneHour(price.items), [['one_hour', '0.6']]);
  assert.equal(price.items.find((entry) => entry.itemCode === 'prompt_write_cached_tokens').pricing.usagePerUnit, '0.375');
  assert.deepEqual(oneHour(price.volumeTiers[0].items), [['one_hour', '12']]);
  assert.equal(priceSchema.safeParse(price).success, true);

  assert.equal(oneHour(priceFromCatalog(model, 'amazon-bedrock').items), undefined);
  const noWrite = priceFromCatalog({ cost: { input: 3, output: 15 } }, 'anthropic');
  assert.equal(
    noWrite.items.some((entry) => entry.promptWriteCacheVariants),
    false
  );
});

test('an unconfigured price stays absent while an explicitly free price remains configured', () => {
  assert.equal(priceSchema.parse({ items: [], volumeTiers: [] }), null);
  const free = { items: [item('prompt_tokens', '0')], volumeTiers: [] };
  assert.equal(priceSchema.parse(free).items[0].pricing.usagePerUnit, '0');
});

test('unfinished items and volume tiers cannot be mistaken for an unconfigured price', () => {
  assert.equal(priceSchema.safeParse({ items: [item('prompt_tokens', '')] }).success, false);
  assert.equal(priceSchema.safeParse({ items: [], volumeTiers: [{ above: 272000, items: [] }] }).success, false);
});

test('schedule-only pricing can be edited but incomplete override prices are rejected', () => {
  const price = {
    items: [],
    schedule: {
      timezone: 'UTC',
      overrides: [
        { name: 'night', priority: 1, when: { dailyTime: { start: '01:00', end: '02:00' } }, items: [item('prompt_tokens', '0.5')] },
      ],
    },
  };
  price.schedule.overrides[0].items[0].pricing.usagePerUnit = '';
  assert.equal(priceSchema.safeParse(price).success, false);
  price.schedule.overrides = [];
  assert.equal(priceSchema.safeParse(price).success, false);
});

test('catalog card imports capabilities, modalities, limits, dates and zero prices together', () => {
  const card = modelCardFromCatalog(
    {
      id: 'catalog-id',
      tool_call: true,
      temperature: true,
      reasoning: { supported: true, default: false },
      modalities: { input: ['text', 'image'], output: ['text'] },
      limit: { context: 128000, output: 16384 },
      knowledge: '2025-01',
      release_date: '2026-01-01',
      last_updated: '2026-02-01',
      cost: { input: 0, output: 2 },
    },
    'openai'
  );
  assert.equal(card.toolCall, true);
  assert.equal(card.temperature, true);
  assert.equal(card.vision, true);
  assert.deepEqual(card.reasoning, { supported: true, default: false });
  assert.deepEqual(card.modalities, { input: ['text', 'image'], output: ['text'] });
  assert.deepEqual(card.limit, { context: 128000, output: 16384 });
  assert.equal(card.knowledge, '2025-01');
  assert.equal(card.releaseDate, '2026-01-01');
  assert.equal(card.lastUpdated, '2026-02-01');
  assert.equal(card.price.items[0].pricing.usagePerUnit, '0');
  assert.equal(card.price.schedule, null);
  assert.equal('modelID' in card, false);
  assert.equal('developer' in card, false);
});

test('catalog cards without pricing remain importable and clear missing card fields', () => {
  const card = modelCardFromCatalog({ id: 'no-prices' }, 'openai');
  assert.deepEqual(card.price.items, []);
  assert.deepEqual(card.price.volumeTiers, []);
  assert.equal(card.price.schedule, null);
  assert.equal(card.toolCall, false);
  assert.equal(card.vision, false);
  assert.deepEqual(card.modalities, { input: [], output: [] });
  assert.deepEqual(card.limit, { context: 0, output: 0 });
});
