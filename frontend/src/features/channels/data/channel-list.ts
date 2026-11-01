import { hashKey } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useCursorList } from '@/hooks/use-cursor-list';
import { buildQueryChannelsQuery, type useQueryChannels } from './channels';
import { channelConnectionSchema, type ChannelConnection } from './schema';

export function useInfiniteChannels(variables: NonNullable<Parameters<typeof useQueryChannels>[0]>, enabled: boolean, atTop: boolean) {
  const { columnVisibility, ...input } = variables;
  const query = useCursorList({
    queryKey: ['channels', 'infinite', input, columnVisibility],
    gcTime: 60_000,
    maxPages: 8,
    enabled,
    interval: atTop ? 5000 : false,
    fetchPage: async (after, signal) => {
      const data = await graphqlRequest<{ queryChannels: ChannelConnection }>(
        buildQueryChannelsQuery(columnVisibility),
        {
          input: { ...input, after },
        },
        undefined,
        { signal }
      );
      return channelConnectionSchema.parse(data.queryChannels);
    },
    fetchPreviousPage: async (before, signal) => {
      const { first } = input;
      const data = await graphqlRequest<{ queryChannels: ChannelConnection }>(
        buildQueryChannelsQuery(columnVisibility),
        {
          input: { ...input, first: undefined, after: undefined, last: first, before },
        },
        undefined,
        { signal }
      );
      return channelConnectionSchema.parse(data.queryChannels);
    },
  });
  return { ...query, selectionScopeKey: hashKey(['channels', 'selection', input]) };
}
