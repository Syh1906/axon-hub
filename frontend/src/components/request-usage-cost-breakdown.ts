export type CostBreakdownItem = {
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
      unitPrice: !item.unpriced && item.quantity ? (item.subtotal * (item.image ? 1 : 1_000_000)) / item.quantity : null,
      tiers:
        item.tiers.length > 1
          ? item.tiers.map((tier) => ({ ...tier, unitPrice: tier.units ? (tier.subtotal * 1_000_000) / tier.units : null }))
          : [],
    }))
    .sort((a, b) => (itemOrder[a.itemCode] ?? 4) - (itemOrder[b.itemCode] ?? 4));
  const { multiplier } = breakdown;
  return {
    items,
    multiplier,
    baseTotal: typeof multiplier === 'number' && multiplier !== 0 ? total / multiplier : null,
    total,
  };
}
