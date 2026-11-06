import { commonPricingEvidence, type CostPricingEvidence } from './cost-pricing-evidence.ts';
export type CostBreakdownItem = {
  pricingEvidence?: CostPricingEvidence | null;
  image?: { requestedWidth?: number; requestedHeight?: number; inspectionError?: string; width: number; height: number; tier: string; tierSource?: string; source: string } | null;
  imageRule?: string;
  unpricedReason?: string;
  unpriced?: boolean;
  itemCode: string;
  variant: string | null;
  quantity: number;
  subtotal: number;
  tiers: { upTo: number | null; units: number; subtotal: number }[];
};

export type CostBreakdown = {
  items: CostBreakdownItem[];
  cacheWriteTokens5m?: number | null;
  cacheWriteTokens1h?: number | null;
  multiplier: number | 'mixed';
  unpricedRecords?: number;
};

type TierRow = CostBreakdownItem['tiers'][number] & { unitPrice: number | null };
type ItemRow = Omit<CostBreakdownItem, 'tiers'> & { unitPrice: number | null; tiers: TierRow[] };

export function inputCostSubtotal(
  items: Array<{ itemCode: string; subtotal: number; unpriced?: boolean; image?: unknown }> | null | undefined,
  cached = false
): number | null {
  const codes = cached
    ? ['prompt_cached_tokens', 'prompt_text_cached_tokens', 'prompt_image_cached_tokens']
    : ['prompt_tokens', 'prompt_text_tokens', 'prompt_image_tokens'];
  const selected = items?.filter((item) => !item.image && codes.includes(item.itemCode)) ?? [];
  if (!selected.length || selected.some((item) => item.unpriced)) return null;
  return selected.reduce((sum, item) => sum + item.subtotal, 0);
}

const itemOrder: Record<string, number> = {
  prompt_text_tokens: 0,
  prompt_image_tokens: 0.5,
  prompt_text_cached_tokens: 2,
  prompt_image_cached_tokens: 2.5,
  prompt_tokens: 0,
  completion_tokens: 1,
  prompt_cached_tokens: 2,
  prompt_write_cached_tokens: 3,
};

export function buildCostBreakdown(total: number, breakdown: CostBreakdown) {
  const items: ItemRow[] = breakdown.items
    .filter((item) => item.unpriced || item.quantity !== 0 || item.subtotal !== 0)
    .map((item) => ({
      ...item,
      unitPrice: !item.unpriced && item.quantity ? ((item.pricingEvidence?.modeSubtotal ?? item.pricingEvidence?.baseSubtotal ?? item.subtotal) * (item.image || item.pricingEvidence?.ruleSource === 'non_token' ? 1 : 1_000_000)) / item.quantity : null,
      tiers:
        item.tiers.length > 1
          ? item.tiers.map((tier, index) => {
            const subtotal = item.pricingEvidence?.modeTierSubtotals?.[index] ?? item.pricingEvidence?.baseTierSubtotals?.[index] ?? tier.subtotal;
            return { ...tier, subtotal, unitPrice: tier.units ? (subtotal * 1_000_000) / tier.units : null };
          })
          : [],
    }))
    .sort((a, b) => (itemOrder[a.itemCode] ?? 4) - (itemOrder[b.itemCode] ?? 4));
  const { multiplier } = breakdown;
  const cacheWrites = items.filter((item) => item.itemCode === 'prompt_write_cached_tokens' && item.quantity > 0);
  const hasHourWrite = (breakdown.cacheWriteTokens1h ?? (cacheWrites.some((item) => item.variant === 'one_hour') ? 1 : 0)) > 0;
  const hasFiveMinuteWrite = (breakdown.cacheWriteTokens5m ?? (cacheWrites.some((item) => item.variant === 'five_min') ? 1 : 0)) > 0;
  const cacheWriteLabel = hasHourWrite
    ? '1h'
    : hasFiveMinuteWrite ? '5m' : '';
  const cacheWriteVariant = hasHourWrite ? 'one_hour' : hasFiveMinuteWrite ? 'five_min' : null;
  const cacheWriteAdjusted = cacheWrites.some(
    (item) => ((item.variant === 'five_min' && hasFiveMinuteWrite) || (item.variant === 'one_hour' && hasHourWrite)) &&
      item.pricingEvidence?.cacheWritePricingVariant != null && item.pricingEvidence.cacheWritePricingVariant !== item.variant
  );
  const evidence = commonPricingEvidence(items);
  const hasEvidence = items.length > 0 && items.every((item) => item.pricingEvidence);
  const modes = new Set(items.map((item) => item.pricingEvidence?.modeMultiplier));
  const costs = new Set(items.map((item) => item.pricingEvidence?.costMultiplier));
  return {
    cacheWriteLabel,
    cacheWriteAdjusted,
    cacheWriteWithoutCost: cacheWriteVariant && !cacheWrites.some((item) => item.variant === cacheWriteVariant) ? cacheWriteVariant : null,
    evidence,
    itemized: hasEvidence && items.some((item) => item.pricingEvidence?.itemMethod === 'unit_price' || item.pricingEvidence?.itemMethod === 'unpriced') || modes.size > 1,
    hasModeSubtotals: hasEvidence && items.every((item) => item.unpriced || item.pricingEvidence?.modeSubtotal != null),
    modeTotal: hasEvidence ? items.reduce((sum, item) => sum + (item.pricingEvidence!.modeSubtotal ?? 0), 0) : null,
    modeMultiplier: hasEvidence ? (modes.size === 1 ? items[0].pricingEvidence!.modeMultiplier : 'mixed') : undefined,
    costMultiplier: hasEvidence ? (costs.size === 1 ? items[0].pricingEvidence!.costMultiplier : 'mixed') : multiplier,
    items,
    multiplier,
    baseTotal: hasEvidence ? items.reduce((sum, item) => sum + item.pricingEvidence!.baseSubtotal, 0) : typeof multiplier === 'number' && multiplier !== 0 ? total / multiplier : null,
    total,
  };
}
