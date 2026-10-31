import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import type { OfficialTierPrice } from './service-tier-rules';
export { appliedModelPrice, officialTierPrice } from './service-tier-rules';
export type { OfficialTierPrice } from './service-tier-rules';

export function useOfficialTierPrices() {
  return useQuery({
    queryKey: ['official-service-tier-item-prices'],
    staleTime: 60 * 60 * 1000,
    queryFn: async () => (await graphqlRequest<{ officialServiceTierPrices: OfficialTierPrice[] }>(
      `query OfficialServiceTierPrices { officialServiceTierPrices { model version sourceURL verifiedAt currency unit fast ultrafast contexts { above upTo standard { itemCode variantCode unitPrice } fast { itemCode variantCode unitPrice } ultrafast { itemCode variantCode unitPrice } } } }`
    )).officialServiceTierPrices,
  });
}
