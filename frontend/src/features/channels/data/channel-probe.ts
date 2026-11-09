import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';
import { graphqlRequest } from '@/gql/graphql';
import { channelProbeDataSchema } from './schema';
import { CHANNEL_PROBE_INTERVAL } from './channel-control-query';

export function channelProbeQueryOptions(channelIDs: string[]) {
  return {
    queryKey: ['channelProbeData', channelIDs],
    enabled: channelIDs.length > 0,
    staleTime: CHANNEL_PROBE_INTERVAL,
    gcTime: 5 * 60_000,
    refetchInterval: CHANNEL_PROBE_INTERVAL,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const data = await graphqlRequest<{ channelProbeData: unknown }>(
        `query GetChannelProbeData($input: GetChannelProbeDataInput!) {
          channelProbeData(input: $input) {
            channelID points { timestamp totalRequestCount successRequestCount avgTokensPerSecond avgTimeToFirstTokenMs }
          }
        }`, { input: { channelIDs } }, undefined, { signal }
      );
      return z.array(channelProbeDataSchema).parse(data.channelProbeData);
    },
  };
}

export function useChannelProbeData(channelIDs: string[], options?: { enabled?: boolean }) {
  return useQuery({ ...channelProbeQueryOptions(channelIDs), enabled: channelIDs.length > 0 && options?.enabled !== false });
}
