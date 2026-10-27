import { z } from 'zod';
import Decimal from 'decimal.js-light';
import { modelPriceInputSchema, type ModelPrice, type ModelPriceItem, type Pricing } from '@/features/channels/data/schema';
import type { ProviderModel } from './providers.schema';

const costFields = [
  ['input', 'prompt_tokens'],
  ['output', 'completion_tokens'],
  ['cache_read', 'prompt_cached_tokens'],
  ['cache_write', 'prompt_write_cached_tokens'],
] as const;

// Zero is an intentional price. Preserve even partially edited items rather
// than replacing the user's draft when a different model ID is selected.
export function hasPriceConfiguration(price: ModelPrice | null | undefined): boolean {
  return Boolean(price?.items?.length || price?.volumeTiers?.length || price?.schedule);
}

export function priceFromCatalog(model: ProviderModel | undefined, providerId: string): ModelPrice | undefined {
  const cost = model?.cost;
  if (!cost) return undefined;
  // The catalog carries only the 5m write price; Anthropic bills 1h writes at 2x input.
  const addOneHourWrite = providerId === 'anthropic' && cost.input != null && cost.cache_write != null;
  const itemsFor = (values: NonNullable<ProviderModel['cost']>): ModelPriceItem[] =>
    costFields.flatMap(([field, itemCode]) => {
      if (values[field] == null) return [];
      const item: ModelPriceItem = { itemCode, pricing: { mode: 'usage_per_unit', usagePerUnit: String(values[field]) } };
      if (addOneHourWrite && field === 'cache_write') {
        const usagePerUnit = new Decimal(String(values.input)).mul(2).toFixed();
        item.promptWriteCacheVariants = [{ variantCode: 'one_hour', pricing: { mode: 'usage_per_unit', usagePerUnit } }];
      }
      return [item];
    });
  const volumeTiers = (cost.tiers ?? [])
    .filter((tier) => tier.tier.type === 'context' && Number.isSafeInteger(tier.tier.size) && tier.tier.size! >= 0)
    .map((tier) => ({ above: tier.tier.size!, items: itemsFor({ ...cost, ...tier }) }))
    .sort((a, b) => a.above - b.above);
  if (!volumeTiers.length && cost.context_over_200k) {
    volumeTiers.push({ above: 200000, items: itemsFor({ ...cost, ...cost.context_over_200k }) });
  }
  const items = itemsFor(cost);
  return items.length ? { items, ...(volumeTiers.length ? { volumeTiers } : {}) } : undefined;
}

function validAmount(value: string | number | null | undefined): boolean {
  const raw = String(value ?? '');
  return (
    value != null && raw !== '' && /^[0-9]+(?:\.[0-9]*)?(?:[eE][+-]?[0-9]+)?$/.test(raw) && Number.isFinite(Number(raw)) && Number(raw) >= 0
  );
}

export function createPriceValidationSchema(t: (key: string) => string) {
  return modelPriceInputSchema
    .extend({
      volumeTiers: z
        .array(
          modelPriceInputSchema.shape.volumeTiers
            .unwrap()
            .unwrap()
            .element.extend({
              above: z
                .number({ error: t('price.validation.increasing') })
                .int(t('price.validation.increasing'))
                .nonnegative(t('price.validation.increasing')),
            })
        )
        .optional()
        .nullable(),
    })
    .superRefine((price, ctx) => {
      const issue = (path: Array<string | number>, key: string) => ctx.addIssue({ code: z.ZodIssueCode.custom, path, message: t(key) });
      const validatePricing = (pricing: Pricing, path: Array<string | number>) => {
        if (price.volumeTiers?.length && path[0] !== 'schedule' && pricing.mode !== 'usage_per_unit') {
          issue([...path, 'mode'], 'price.validation.perUnitOnly');
        }
        if (pricing.mode === 'flat_fee' || pricing.mode === 'usage_per_unit') {
          const field = pricing.mode === 'flat_fee' ? 'flatFee' : 'usagePerUnit';
          if (!validAmount(pricing[field])) issue([...path, field], 'price.validation.nonnegative');
          return;
        }
        const tiers = pricing.usageTiered?.tiers ?? [];
        if (!tiers.length) issue([...path, 'usageTiered'], 'price.validation.priceRequired');
        tiers.forEach((tier, i) => {
          if (!validAmount(tier.pricePerUnit)) issue([...path, 'usageTiered', 'tiers', i, 'pricePerUnit'], 'price.validation.nonnegative');
          const previous = i === 0 ? -1 : tiers[i - 1].upTo;
          const validBound =
            i === tiers.length - 1
              ? tier.upTo == null
              : tier.upTo != null && Number.isSafeInteger(tier.upTo) && previous != null && tier.upTo > previous;
          if (!validBound) issue([...path, 'usageTiered', 'tiers', i, 'upTo'], 'price.validation.increasing');
        });
      };
      const validateItems = (items: ModelPriceItem[], path: Array<string | number>) => {
        if (!items.length) issue(path, 'price.validation.itemsRequired');
        const seen = new Set<string>();
        items.forEach((item, i) => {
          if (seen.has(item.itemCode)) issue([...path, i, 'itemCode'], 'price.duplicateItemCode');
          seen.add(item.itemCode);
          validatePricing(item.pricing, [...path, i, 'pricing']);
          const variants = new Set<string>();
          item.promptWriteCacheVariants?.forEach((variant, j) => {
            if (variants.has(variant.variantCode))
              issue([...path, i, 'promptWriteCacheVariants', j, 'variantCode'], 'price.duplicateVariantCode');
            variants.add(variant.variantCode);
            validatePricing(variant.pricing, [...path, i, 'promptWriteCacheVariants', j, 'pricing']);
          });
        });
      };
      if (price.items.length || price.volumeTiers?.length) validateItems(price.items, ['items']);
      const itemKeys = (items: ModelPriceItem[]) =>
        items
          .flatMap((item) => [
            item.itemCode,
            ...(item.promptWriteCacheVariants ?? []).map((variant) => `${item.itemCode}:${variant.variantCode}`),
          ])
          .sort()
          .join('|');
      const baseKeys = itemKeys(price.items);
      price.volumeTiers?.forEach((tier, i, tiers) => {
        if (!Number.isSafeInteger(tier.above) || tier.above < 0 || (i > 0 && tier.above <= tiers[i - 1].above)) {
          issue(['volumeTiers', i, 'above'], 'price.validation.increasing');
        }
        validateItems(tier.items, ['volumeTiers', i, 'items']);
        if (itemKeys(tier.items) !== baseKeys) issue(['volumeTiers', i, 'items'], 'price.validation.completeTier');
      });
      if (price.schedule && price.schedule.overrides.length === 0) {
        issue(['schedule', 'overrides'], 'price.validation.itemsRequired');
      }
      price.schedule?.overrides.forEach((override, i) => {
        validateItems(override.items, ['schedule', 'overrides', i, 'items']);
        if (!override.when.dailyTime && !override.when.weekdays?.length && !override.when.dateRange) {
          issue(['schedule', 'overrides', i, 'when'], 'price.schedule.when.atLeastOne');
        }
      });
    })
    .transform((price) => (price.items.length === 0 && !price.volumeTiers?.length && !price.schedule ? null : price));
}

export function isValidPriceMultiplier(value: string): boolean {
  return /^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(value.trim()) && Number.isFinite(Number(value));
}

export function effectivePrice(value: unknown, multiplier: string): string | null {
  if ((typeof value !== 'string' && typeof value !== 'number') || !isValidPriceMultiplier(multiplier)) return null;
  const raw = String(value).trim();
  if (!/^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(raw)) return null;
  try {
    const ExactDecimal = Decimal.clone();
    const amount = new ExactDecimal(raw);
    const factor = new ExactDecimal(multiplier.trim());
    ExactDecimal.set({ precision: Math.max(1, amount.precision(false) + factor.precision(false)) });
    return amount.mul(factor).toFixed();
  } catch {
    return null;
  }
}

export function multiplyPriceItems(items: ModelPriceItem[], multiplier: string | number): ModelPriceItem[] {
  const ExactDecimal = Decimal.clone();
  const factor = new ExactDecimal(String(multiplier).trim().replace(/^\+/, ''));
  const factorDigits = factor.precision(false);
  const scale = (pricing: Pricing): Pricing => {
    const amount = (value: string | number | null | undefined) => {
      if (!validAmount(value)) return value;
      const decimal = new ExactDecimal(String(value).trim().replace(/^\+/, ''));
      // A product needs at most the sum of its operands' significant digits.
      ExactDecimal.set({ precision: decimal.precision(false) + factorDigits });
      return decimal.mul(factor).toFixed();
    };
    return {
      ...pricing,
      flatFee: amount(pricing.flatFee),
      usagePerUnit: amount(pricing.usagePerUnit),
      usageTiered: pricing.usageTiered
        ? {
            tiers: pricing.usageTiered.tiers.map((tier) => ({ ...tier, pricePerUnit: amount(tier.pricePerUnit)! })),
          }
        : pricing.usageTiered,
    };
  };
  return items.map((item) => ({
    ...item,
    pricing: scale(item.pricing),
    promptWriteCacheVariants: item.promptWriteCacheVariants?.map((variant) => ({ ...variant, pricing: scale(variant.pricing) })),
  }));
}
