import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { channelTodayUsageSchema } from './schema';

export function useChannelTodayUsage(channelIds: string[]) {
  return useQuery({
    queryKey: ['channel-today-usage', channelIds],
    enabled: channelIds.length > 0,
    refetchInterval: 30_000,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ channelTodayUsage: unknown }>(
        `query ChannelTodayUsage($channelIds: [ID!]!) {
          channelTodayUsage(channelIds: $channelIds) { channelId requestCount unpricedCount totalCost timezone currencyCode }
        }`,
        { channelIds },
        undefined,
        { signal }
      );
      return z.array(channelTodayUsageSchema).parse(data.channelTodayUsage);
    },
  });
}
