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
  items { ${MODEL_PRICE_ITEM_FIELDS} }
  volumeTiers {
    above
    items { ${MODEL_PRICE_ITEM_FIELDS} }
  }
  schedule {
    timezone
    overrides {
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
