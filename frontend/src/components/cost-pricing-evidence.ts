import { z } from 'zod';

export const costPricingEvidenceSchema = z.object({
  itemMethod: z.enum(['multiplier', 'unit_price', 'unpriced']).nullable().optional(),
  ruleSource: z.string().optional(),
  baseUnitPrice: z.number().nullable().optional(),
  finalUnitPrice: z.number().nullable().optional(),
  modeSubtotal: z.number().nullable().optional(),
  modeTierSubtotals: z.array(z.number()).nullable().optional(),
  reference: z.object({ source: z.enum(['official','custom','model']), referenceModel: z.string(), version: z.string(), sourceURL: z.string(), verifiedAt: z.string(), application: z.string(), currency: z.string(), unit: z.string() }).nullable().optional(),
  serviceTier: z.object({
    mode: z.string(),
    basis: z.string(),
    client: z.string(),
    outbound: z.string(),
    upstream: z.string(),
    confirmed: z.boolean(),
    reason: z.string(),
    source: z.string(),
    referenceModel: z.string(),
    version: z.string(),
  }),
  context: z.object({
    kind: z.string(),
    inputTokens: z.number(),
    threshold: z.number().nullable(),
    longContext: z.boolean(),
    name: z.string(),
  }),
  baseSubtotal: z.number(),
  baseTierSubtotals: z.array(z.number()).nullable().optional(),
  modeMultiplier: z.number().nullable(),
  costMultiplier: z.number(),
});
export type CostPricingEvidence = z.infer<typeof costPricingEvidenceSchema>;
export const COST_PRICING_EVIDENCE_FIELDS = `pricingEvidence {
  serviceTier { mode basis client outbound upstream confirmed reason source referenceModel version }
  context { kind inputTokens threshold longContext name }
  baseSubtotal baseTierSubtotals modeMultiplier costMultiplier itemMethod ruleSource baseUnitPrice finalUnitPrice modeSubtotal modeTierSubtotals reference { source referenceModel version sourceURL verifiedAt application currency unit }
}`;

export function commonPricingEvidence(items: { pricingEvidence?: CostPricingEvidence | null }[]) {
  if (!items.length || items.some((item) => !item.pricingEvidence)) return null;
  const first = items[0].pricingEvidence!;
  const identity = (value: CostPricingEvidence) => JSON.stringify([value.serviceTier, value.context]);
  return items.every((item) => identity(item.pricingEvidence!) === identity(first)) ? first : ('mixed' as const);
}
