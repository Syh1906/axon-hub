import { graphqlRequest } from '@/gql/graphql';
import { channelSchema } from './schema';

const liveSchema = channelSchema.pick({
  id: true, status: true, errorMessage: true, disabledAPIKeys: true,
  healthGate: true, liveUsage: true, liveLimiterStats: true, providerQuotaStatus: true,
});

export const CHANNEL_LIVE_INTERVAL = 5_000;
export const CHANNEL_PROBE_INTERVAL = 60_000;

export function channelControlBatches(ids: string[], size = 100) {
  const unique = [...new Set(ids)];
  return Array.from({ length: Math.ceil(unique.length / size) }, (_, index) => unique.slice(index * size, (index + 1) * size));
}

export function channelLiveQueryOptions(ids: string[], load: boolean) {
  return {
    queryKey: ['channels', 'live', ids, load],
    enabled: ids.length > 0,
    staleTime: CHANNEL_LIVE_INTERVAL,
    gcTime: 5 * 60_000,
    refetchInterval: CHANNEL_LIVE_INTERVAL,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const data = await graphqlRequest<{ queryChannels: { edges: { node: unknown }[] } }>(
        `query ChannelLiveControls($ids: [ID!]!, $first: Int!) {
          queryChannels(input: { first: $first, where: { idIn: $ids } }) {
            edges { node {
              id status errorMessage disabledAPIKeys { key disabledAt errorCode reason expiresAt }
              healthGate { disabled openCount unstableCount }
              providerQuotaStatus { status nextResetAt ready quotaData providerType accountKey }
              ${load ? 'liveUsage { inFlight rpm } liveLimiterStats { inFlight waiting capacity queueSize }' : ''}
            } }
          }
        }`, { ids, first: ids.length }, undefined, { signal }
      );
      return data.queryChannels.edges.map(({ node }) => liveSchema.parse(node));
    },
  };
}
