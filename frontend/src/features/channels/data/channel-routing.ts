import { z } from 'zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useErrorHandler } from '@/hooks/use-error-handler';

export const channelRoutingSummarySchema = z.object({
  id: z.string(),
  name: z.string(),
  tags: z.array(z.string()).nullish(),
  orderingWeight: z.number(),
  settings: z.object({ tagPriorities: z.array(z.object({ tag: z.string(), priority: z.number().int() })) }).nullish(),
});

export type ChannelRoutingSummary = z.infer<typeof channelRoutingSummarySchema>;
export type ChannelTagPriorityUpdate = { channelId: string; priority: number | null; orderingWeight?: number };

export function useChannelRoutingSummaries(enabled: boolean) {
  return useQuery({
    queryKey: ['channels', 'routing-summaries'],
    enabled,
    queryFn: async () => {
      const data = await graphqlRequest<{ allChannelSummarys: unknown }>(`
        query ChannelRoutingSummaries {
          allChannelSummarys {
            id name tags orderingWeight
            settings { tagPriorities { tag priority } }
          }
        }
      `);
      return z.array(channelRoutingSummarySchema).parse(data.allChannelSummarys);
    },
  });
}

export function useUpdateChannelTagPriorities() {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: (variables: { tag: string; input: ChannelTagPriorityUpdate[] }) =>
      graphqlRequest(
        `
        mutation UpdateChannelTagPriorities($tag: String!, $input: [ChannelTagPriorityUpdateInput!]!) {
          updateChannelTagPriorities(tag: $tag, input: $input) {
            id orderingWeight settings { tagPriorities { tag priority } }
          }
        }
      `,
        variables
      ),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['channels'] });
      queryClient.invalidateQueries({ queryKey: ['allChannelSummarys'] });
      variables.input.forEach(({ channelId }) => queryClient.invalidateQueries({ queryKey: ['channel', channelId] }));
      toast.success(t('channels.routing.saved'));
    },
    onError: (error) => handleError(error, t('channels.routing.saveFailed')),
  });
}

export function useUpdateChannelCostMultiplier() {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  const { handleError } = useErrorHandler();
  return useMutation({
    mutationFn: (variables: { channelId: string; multiplier: string }) =>
      graphqlRequest(
        `
        mutation UpdateChannelCostMultiplier($channelId: ID!, $multiplier: Decimal!) {
          updateChannelCostMultiplier(channelId: $channelId, multiplier: $multiplier) { id }
        }
      `,
        variables
      ),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['channels'] });
      queryClient.invalidateQueries({ queryKey: ['channel', variables.channelId] });
      queryClient.invalidateQueries({ queryKey: ['channelModelPrices'] });
      toast.success(t('channels.routing.saved'));
    },
    onError: (error) => handleError(error, t('channels.routing.saveFailed')),
  });
}
