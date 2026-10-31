import { commonPricingEvidence, type CostPricingEvidence } from './cost-pricing-evidence.ts';
export type CostBreakdownItem = {
  pricingEvidence?: CostPricingEvidence | null;
  image?: { width: number; height: number; tier: string; tierSource?: string; source: string } | null;
  imageRule?: string;
  unpriced?: boolean;
  itemCode: string;
  variant: string | null;
  quantity: number;
  subtotal: number;
  tiers: { upTo: number | null; units: number; subtotal: number }[];
};

export type CostBreakdown = {
  items: CostBreakdownItem[];
  multiplier: number | 'mixed';
  unpricedRecords?: number;
};

type TierRow = CostBreakdownItem['tiers'][number] & { unitPrice: number | null };
type ItemRow = Omit<CostBreakdownItem, 'tiers'> & { unitPrice: number | null; tiers: TierRow[] };

const itemOrder: Record<string, number> = {
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
      unitPrice: !item.unpriced && item.quantity ? ((item.pricingEvidence?.modeSubtotal ?? item.pricingEvidence?.baseSubtotal ?? item.subtotal) * (item.image ? 1 : 1_000_000)) / item.quantity : null,
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
  const evidence = commonPricingEvidence(items);
  const hasEvidence = items.length > 0 && items.every((item) => item.pricingEvidence);
  const modes = new Set(items.map((item) => item.pricingEvidence?.modeMultiplier));
  const costs = new Set(items.map((item) => item.pricingEvidence?.costMultiplier));
  return {
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
