import { z } from 'zod';
import { useQuery, type QueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { channelTodayUsageSchema } from './schema';

export function channelTodayUsageQueryOptions(channelIds: string[]) {
  return {
    queryKey: ['channel-today-usage', channelIds],
    enabled: channelIds.length > 0,
    refetchInterval: false as const,
    refetchOnMount: 'always' as const,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: Infinity,
    gcTime: 5 * 60_000,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const data = await graphqlRequest<{ channelTodayUsage: unknown }>(
        `query ChannelTodayUsage($channelIds: [ID!]!) {
          channelTodayUsage(channelIds: $channelIds) { channelId requestCount unpricedCount pricedCount totalCost timezone currencyCode
            models { model requestCount unpricedCount pricedCount totalTokens totalCost cacheHitRate successRate avgFirstTokenMs avgTokensPerSecond standardCost standardUnpricedCount avgCostPerMillionTokens tokenPricedRequestCount }
          }
        }`,
        { channelIds },
        undefined,
        { signal }
      );
      return z.array(channelTodayUsageSchema).parse(data.channelTodayUsage);
    },
  };
}

export function useChannelTodayUsage(channelIds: string[]) {
  return useQuery(channelTodayUsageQueryOptions(channelIds));
}

export async function refreshChannelTodayUsage(client: QueryClient, channelIds: string[]) {
  const requested = new Set(channelIds);
  const overlapping = client
    .getQueryCache()
    .findAll({ queryKey: ['channel-today-usage'] })
    .filter((query) => (query.queryKey[1] as string[]).some((id) => requested.has(id)));
  // Keep first loads on their observed query keys, including their error/retry
  // lifecycle. Batches which already have data only need the requested IDs.
  const batches = overlapping
    .filter((query) => query.state.data === undefined)
    .map((query) => query.queryKey[1] as string[]);
  const covered = new Set(batches.flat());
  const remaining = channelIds.filter((id) => !covered.has(id));
  if (remaining.length > 0) batches.push(remaining);
  await client.cancelQueries({ predicate: (query) => overlapping.includes(query) });
  const rows = (await Promise.all(batches.map((ids) => client.fetchQuery({ ...channelTodayUsageQueryOptions(ids), staleTime: 0 })))).flat();
  const updated = new Map(rows.map((row) => [row.channelId, row]));
  for (const query of client.getQueryCache().findAll({ queryKey: ['channel-today-usage'] })) {
    const ids = query.queryKey[1] as string[];
    client.setQueryData<typeof rows>(query.queryKey, (previous) =>
      previous
        ? previous.map((row) => updated.get(row.channelId) ?? row)
        : ids.every((id) => updated.has(id))
          ? ids.map((id) => updated.get(id)!)
          : undefined
    );
  }
}
