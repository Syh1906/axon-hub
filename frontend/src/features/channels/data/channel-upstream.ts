import { useEffect, useRef } from 'react';
import { z } from 'zod';
import { useIsMutating, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useErrorHandler } from '@/hooks/use-error-handler';
import { publishUpstreamStates, refreshUpstreamChannels, type UpstreamRefreshOptions } from './channel-upstream-refresh';
import { channelUpstreamStateSchema, type ChannelUpstreamState, type UpstreamInput } from './schema';

export type { ChannelUpstreamState, UpstreamKey, UpstreamBalance, UpstreamInput } from './schema';

const balanceFields = 'kind amount used limit unlimited currency unit accountRef expiresAt';
export const upstreamStateFields = `channelId provider manualMultiplier keySessionReuse
  accounts { ref name provider accountId hasToken verifiedAt keyCount complete updatedAt balance { ${balanceFields} } }
  balanceSummary { accountCount knownAccountCount unassignedKeyCount complete balances { ${balanceFields} } }
  settings { enabled costMode refreshIntervalMinutes }
  keys { keyRef label remark accountRef disabled upstreamMultiplier applied { value source provider observedAt }
    snapshot { provider balanceState rateState balanceErrorCode rateErrorCode balanceUpdatedAt balanceCheckedAt rateCheckedAt nextBalanceCheckAt nextRateCheckAt
      balances { ${balanceFields} } account { accountId updatedAt balance { ${balanceFields} }
      } rate { multiplier group observedAt syncedAt validUntil }
    }
  }`;

export function useChannelUpstreamStates(channelIds: string[]) {
  return useQuery({
    queryKey: ['channel-upstream', channelIds],
    enabled: channelIds.length > 0,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ channelUpstreamStates: unknown }>(
        `query ChannelUpstreamStates($channelIds: [ID!]!) { channelUpstreamStates(channelIds: $channelIds) { ${upstreamStateFields} } }`,
        { channelIds },
        undefined,
        { signal }
      );
      return z.array(channelUpstreamStateSchema).parse(data.channelUpstreamStates);
    },
  });
}

export function useUpstreamRefreshPending(channelId?: string) {
  return (
    useIsMutating({
      mutationKey: ['channel-upstream-refresh'],
      predicate: (mutation) =>
        !channelId || (mutation.state.variables as { channelIds?: string[] })?.channelIds?.includes(channelId) === true,
    }) > 0
  );
}

export function useRefreshChannelUpstreams() {
  const queryClient = useQueryClient();
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  return useMutation({
    mutationKey: ['channel-upstream-refresh'],
    mutationFn: async (variables: UpstreamRefreshOptions) => {
      const ids = [...new Set(variables.channelIds)];
      const states: ChannelUpstreamState[] = [];
      for (let offset = 0; offset < ids.length; offset += 100) {
        const data = await graphqlRequest<{ channelUpstreamStates: unknown }>(
          `query ChannelUpstreamStates($channelIds: [ID!]!) { channelUpstreamStates(channelIds: $channelIds) { ${upstreamStateFields} } }`,
          { channelIds: ids.slice(offset, offset + 100) }
        );
        states.push(...z.array(channelUpstreamStateSchema).parse(data.channelUpstreamStates));
      }
      return refreshUpstreamChannels(states, variables, async (channelId, keyRef) => {
        const data = await graphqlRequest<{
          refreshChannelUpstreams: { channels: unknown; refreshed: number; skipped: number; failed: number };
        }>(
          `mutation RefreshChannelUpstreams($channelIds: [ID!]!, $force: Boolean!, $includeDisabled: Boolean!, $keyRef: String!) {
            refreshChannelUpstreams(channelIds: $channelIds, force: $force, includeDisabled: $includeDisabled, keyRef: $keyRef) {
              channels { ${upstreamStateFields} } refreshed skipped failed
            }
          }`,
          { channelIds: [channelId], keyRef, force: variables.force ?? false, includeDisabled: variables.includeDisabled ?? false }
        );
        return {
          ...data.refreshChannelUpstreams,
          channels: z.array(channelUpstreamStateSchema).parse(data.refreshChannelUpstreams.channels),
        };
      });
    },
    onSuccess: async ({ channels, failed }, variables) => {
      await publishUpstreamStates(queryClient, channels);
      await queryClient.invalidateQueries({ queryKey: ['channels'] });
      if (failed && !variables.silent) toast.warning(t('channels.upstream.refreshFailed'));
    },
    onError: (error, variables) => {
      if (!variables.silent) handleError(error, t('channels.upstream.refreshFailed'));
    },
  });
}

export function useRefreshUpstreamOnPage(channelIds: string[], canWrite: boolean) {
  const refresh = useRefreshChannelUpstreams();
  const lastPage = useRef('');
  const signature = channelIds.join('|');
  const mutate = refresh.mutate;
  useEffect(() => {
    if (!canWrite || !signature || lastPage.current === signature) return;
    lastPage.current = signature;
    // The server checks due times. Start after the list snapshot is available,
    // without waiting for each row's upstream request to finish rendering.
    mutate({ channelIds: signature.split('|'), silent: true });
  }, [canWrite, signature, mutate]);
}

export async function getUpstreamRefreshTargets(): Promise<string[]> {
  const data = await graphqlRequest<{ channelUpstreamRefreshTargets: string[] }>(
    'query ChannelUpstreamRefreshTargets { channelUpstreamRefreshTargets }'
  );
  return data.channelUpstreamRefreshTargets;
}

export function useSaveChannelUpstream(channelId: string) {
  const queryClient = useQueryClient();
  const { handleError } = useErrorHandler();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: async (input: UpstreamInput) => {
      const data = await graphqlRequest<{ updateChannelUpstream: unknown }>(
        `mutation UpdateChannelUpstream($channelId: ID!, $input: UpdateChannelUpstreamInput!) { updateChannelUpstream(channelId: $channelId, input: $input) { ${upstreamStateFields} } }`,
        { channelId, input }
      );
      return channelUpstreamStateSchema.parse(data.updateChannelUpstream);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['channel-upstream'] });
      queryClient.invalidateQueries({ queryKey: ['channels'] });
      queryClient.invalidateQueries({ queryKey: ['channel', channelId] });
      queryClient.invalidateQueries({ queryKey: ['channelModelPrices'] });
      toast.success(t('channels.upstream.saved'));
    },
    onError: (error) => handleError(error, t('channels.routing.saveFailed')),
  });
}

export async function verifyUpstreamAccount(channelId: string, accountRef: string) {
  const data = await graphqlRequest<{
    verifyChannelUpstreamAccount: { success: boolean; accountId: number | null; requiresAccountId: boolean; errorCode: string };
  }>(
    `mutation VerifyChannelUpstreamAccount($channelId: ID!, $accountRef: String!) {
      verifyChannelUpstreamAccount(channelId: $channelId, accountRef: $accountRef) { success accountId requiresAccountId errorCode }
    }`,
    { channelId, accountRef }
  );
  return data.verifyChannelUpstreamAccount;
}

export function multiplierRange(values: string[]): string {
  if (!values.length) return '—';
  const numbers = values.map(Number);
  const min = Math.min(...numbers),
    max = Math.max(...numbers);
  return min === max ? `${min}×` : `${min}–${max}×`;
}
