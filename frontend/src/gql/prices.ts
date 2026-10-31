export const SERVICE_TIER_PRICE_FIELDS = `source multiplier referenceModel version unpriced application contextAbove contextUpTo sourceURL verifiedAt currency unit items { itemCode variantCode method value referenceBasePrice referenceModePrice }`;
export const SERVICE_TIER_PRICES_FIELDS = `serviceTiers { fast { ${SERVICE_TIER_PRICE_FIELDS} } ultrafast { ${SERVICE_TIER_PRICE_FIELDS} } }`;
const PRICING_FIELDS = `
  mode
  image { automatic rules { name fee match conditions { dimension operator value } } savedRules { name fee match conditions { dimension operator value } } }
  flatFee
  usagePerUnit
  usageTiered { tiers { upTo pricePerUnit } }
`;

const MODEL_PRICE_ITEM_FIELDS = `
  itemCode
  pricing { ${PRICING_FIELDS} }
  promptWriteCacheVariants {
    variantCode
    pricing { ${PRICING_FIELDS} }
  }
`;

export const MODEL_PRICE_FIELDS = `
 ${SERVICE_TIER_PRICES_FIELDS}
  items { ${MODEL_PRICE_ITEM_FIELDS} }
  volumeTiers {
    ${SERVICE_TIER_PRICES_FIELDS}
    longContext
    above
    items { ${MODEL_PRICE_ITEM_FIELDS} }
  }
  schedule {
    timezone
    overrides {
      ${SERVICE_TIER_PRICES_FIELDS}
      name
      priority
      when {
        dailyTime { start end }
        weekdays
        dateRange { start end }
      }
      items { ${MODEL_PRICE_ITEM_FIELDS} }
    }
  }
`;
