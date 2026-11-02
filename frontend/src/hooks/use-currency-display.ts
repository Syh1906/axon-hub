import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';

export function useCurrencyDisplayDecimals() {
  const { data } = useQuery({
    queryKey: ['system-currency-display-decimals'],
    staleTime: 300_000,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ systemCurrencyDisplayDecimals: number | null }>(
        'query { systemCurrencyDisplayDecimals }',
        {},
        undefined,
        { signal }
      );
      return data.systemCurrencyDisplayDecimals;
    },
  });
  return data;
}
