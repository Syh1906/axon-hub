import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { channelTodayUsageSchema } from './schema';

const channelTodayUsageDetailsSchema = z.object({
  channelId: z.string(),
  timezone: z.string(),
  currencyCode: z.string(),
  models: z.array(
    z.object({
      model: z.string(),
      requestCount: z.number(),
      unpricedCount: z.number(),
      totalTokens: z.number(),
      totalCost: z.number(),
      cacheHitRate: z.number().nullable(),
      successRate: z.number().nullable(),
      avgFirstTokenMs: z.number().nullable(),
      avgTokensPerSecond: z.number().nullable(),
      standardCost: z.number().nullable(),
      standardUnpricedCount: z.number(),
      avgCostPerMillionTokens: z.number().nullable(),
      tokenPricedRequestCount: z.number(),
    })
  ),
});

export function useChannelTodayUsageDetails(channelId: string) {
  return useQuery({
    queryKey: ['channel-today-usage-details', channelId],
    staleTime: 20_000,
    gcTime: 300_000,
    refetchInterval: 30_000,
    refetchOnWindowFocus: false,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ channelTodayUsageDetails: unknown }>(
        `query ChannelTodayUsageDetails($channelId: ID!) {
          channelTodayUsageDetails(channelId: $channelId) {
            channelId timezone currencyCode
            models { model requestCount unpricedCount totalTokens totalCost cacheHitRate successRate avgFirstTokenMs avgTokensPerSecond standardCost standardUnpricedCount avgCostPerMillionTokens tokenPricedRequestCount }
          }
        }`,
        { channelId },
        undefined,
        { signal }
      );
      return channelTodayUsageDetailsSchema.parse(data.channelTodayUsageDetails);
    },
  });
}

export function useChannelTodayUsage(channelIds: string[]) {
  return useQuery({
    queryKey: ['channel-today-usage', channelIds],
    enabled: channelIds.length > 0,
    refetchInterval: 30_000,
    refetchOnWindowFocus: false,
    gcTime: 0,
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
