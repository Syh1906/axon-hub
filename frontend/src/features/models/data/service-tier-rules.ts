import Decimal from 'decimal.js-light';
import type { ModelPrice, Pricing, ServiceTierPrices, ServiceTierItemRule } from '@/features/channels/data/schema';

export type TierMode = 'fast' | 'ultrafast';
export type TierPrice = NonNullable<ServiceTierPrices['fast']>;
type OfficialItem = { itemCode: ServiceTierItemRule['itemCode']; variantCode: string; unitPrice: string | number };
export type OfficialTierContext = { above: number | null; upTo: number | null; standard: OfficialItem[]; fast: OfficialItem[]; ultrafast: OfficialItem[] };
export type OfficialTierPrice = {
  model: string; version: string; sourceURL: string; verifiedAt: string; currency: string; unit: string;
  fast: string | number | null; ultrafast: string | number | null; contexts: OfficialTierContext[];
};
export const tierItemKeys = [
  { itemCode: 'prompt_tokens', variantCode: '' },
  { itemCode: 'completion_tokens', variantCode: '' },
  { itemCode: 'prompt_cached_tokens', variantCode: '' },
  { itemCode: 'prompt_write_cached_tokens', variantCode: '' },
  { itemCode: 'prompt_write_cached_tokens', variantCode: 'five_min' },
  { itemCode: 'prompt_write_cached_tokens', variantCode: 'one_hour' },
] as const;

export function officialTierPrice(row: OfficialTierPrice, mode: TierMode, application = 'ratio', scope = row.contexts[0]): TierPrice {
  const result: TierPrice = {
    source: 'official', referenceModel: row.model, version: row.version, application,
    contextAbove: scope.above, contextUpTo: scope.upTo, sourceURL: row.sourceURL,
    verifiedAt: row.verifiedAt, currency: row.currency, unit: row.unit,
  };
  if (!scope[mode].length) return { ...result, unpriced: true };
  result.items = tierItemKeys.map((key) => {
    const quote = scope[mode].find((item) => item.itemCode === key.itemCode && item.variantCode === key.variantCode);
    if (!quote) return { ...key, method: 'unpriced' };
    const base = scope.standard.find((item) => item.itemCode === key.itemCode && item.variantCode === key.variantCode);
    const reference = { referenceBasePrice: base == null ? null : String(base.unitPrice), referenceModePrice: String(quote.unitPrice) };
    if (application === 'ratio' && base && !new Decimal(base.unitPrice).isZero()) {
      return { ...key, ...reference, method: 'multiplier', value: new Decimal(quote.unitPrice).div(base.unitPrice).toFixed() };
    }
    return { ...key, ...reference, method: 'unit_price', value: String(quote.unitPrice) };
  });
  return result;
}

export function applyOfficialTierPrices(price: ModelPrice, row: OfficialTierPrice, application: string): ModelPrice {
  const copy = structuredClone(price);
  const modesFor = (above: number | null) => {
    const scope = row.contexts.find((c) => c.above === above) ?? row.contexts.findLast((c) => c.above != null && above != null && c.above <= above) ?? row.contexts[0];
    return { fast: officialTierPrice(row, 'fast', application, scope), ultrafast: officialTierPrice(row, 'ultrafast', application, scope) };
  };
  copy.serviceTiers = modesFor(null);
  copy.volumeTiers = copy.volumeTiers?.map((tier) => ({ ...tier, serviceTiers: modesFor(tier.above) }));
  // Time schedules select their own base rates; never copy a long-context rule.
  copy.schedule?.overrides.forEach((override) => { override.serviceTiers = null; });
  return copy;
}

export function appliedModelPrice(price: ModelPrice, model: string): ModelPrice {
  const copy = structuredClone(price);
  for (const modes of [copy.serviceTiers, ...(copy.volumeTiers ?? []).map((tier) => tier.serviceTiers), ...(copy.schedule?.overrides ?? []).map((o) => o.serviceTiers)]) {
    for (const mode of ['fast', 'ultrafast'] as const) {
      const rate = modes?.[mode];
      if (rate) { rate.source = 'model'; rate.referenceModel ||= model; }
    }
  }
  return copy;
}

export function resolveTierItem(defaults: TierPrice | null | undefined, override: TierPrice | null | undefined, itemCode: string, variantCode = '', inputTokens = 0) {
  for (const rate of [override, defaults]) {
    if (!rate) continue;
    if (rate.unpriced || rate.contextAbove != null && inputTokens <= rate.contextAbove || rate.contextUpTo != null && inputTokens > rate.contextUpTo) return { method: 'unpriced' as const };
    const rule = rate.items?.find((item) => item.itemCode === itemCode && (item.variantCode ?? '') === variantCode)
      ?? (variantCode ? rate.items?.find((item) => item.itemCode === itemCode && !item.variantCode) : undefined);
    if (rule) return rule;
    if (rate.multiplier != null && rate.multiplier !== '') return { method: 'multiplier' as const, value: rate.multiplier };
  }
  return { method: 'unpriced' as const };
}

export function tierUnitPrice(base: Pricing, rule: ReturnType<typeof resolveTierItem>): string | null {
  if (base.mode === 'flat_fee' || base.mode === 'image_fixed' || rule.method === 'unpriced') return null;
  try {
    if (rule.value == null || rule.value === '') return null;
    if (rule.method === 'unit_price') return new Decimal(rule.value).toFixed();
    return base.usagePerUnit == null ? null : new Decimal(base.usagePerUnit).mul(rule.value).toFixed();
  } catch { return null; }
}
