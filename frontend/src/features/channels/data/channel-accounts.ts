import { useMutation, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { upstreamStateFields } from './channel-upstream';
import { publishUpstreamStates } from './channel-upstream-refresh';
import { channelUpstreamStateSchema } from './schema';

export type AccountInput = { ref?: string; name: string; provider: 'NEW_API' | 'SUB2_API'; accountToken?: string; accountId?: number };
export type KeySettingsInput = { keyRef: string; remark: string; accountRef: string; priority?: number };

function useStateMutation<T>(channelId: string, field: string, declaration: string, argument: string) {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: async (value: T) => {
      const data = await graphqlRequest<Record<string, unknown>>(
        `mutation ChannelAccountAction($channelId: ID!, $value: ${declaration}) { ${field}(channelId: $channelId, ${argument}: $value) { ${upstreamStateFields} } }`,
        { channelId, value }
      );
      return channelUpstreamStateSchema.parse(data[field]);
    },
    onSuccess: async (state) => {
      await publishUpstreamStates(queryClient, [state]);
      await queryClient.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: (error) => {
      const message = error instanceof Error ? error.message : '';
      const code = ['ACCOUNT_ID_REQUIRED', 'ACCOUNT_MISMATCH', 'ACCOUNT_ALREADY_EXISTS', 'UNAUTHORIZED', 'FORBIDDEN'].find((code) =>
        message.includes(code)
      );
      toast.error(code ? t(`channels.upstream.errors.${code}`) : t('channels.accounts.saveFailed'));
    },
  });
}

export function useSaveUpstreamAccount(channelId: string) {
  return useStateMutation<AccountInput>(channelId, 'saveChannelUpstreamAccount', 'SaveChannelUpstreamAccountInput!', 'input');
}
export function useDeleteUpstreamAccount(channelId: string) {
  return useStateMutation<string>(channelId, 'deleteChannelUpstreamAccount', 'String!', 'accountRef');
}
export function useUpdateKeySettings(channelId: string) {
  return useStateMutation<KeySettingsInput>(channelId, 'updateChannelKeySettings', 'ChannelKeySettingsInput!', 'input');
}
export function useKeySessionReuse(channelId: string) {
  return useStateMutation<boolean>(channelId, 'setChannelKeySessionReuse', 'Boolean!', 'enabled');
}

// 完整凭据仅由主动操作读取，不放入 TanStack Query、浏览器存储或普通列表。
export async function revealChannelCredential(channelId: string, reference: { keyRef?: string; accountRef?: string }) {
  const data = await graphqlRequest<{ revealChannelCredential: string }>(
    `mutation RevealChannelCredential($channelId: ID!, $keyRef: String, $accountRef: String) { revealChannelCredential(channelId: $channelId, keyRef: $keyRef, accountRef: $accountRef) }`,
    { channelId, ...reference }
  );
  return data.revealChannelCredential;
}

export async function upstreamKeyRef(key: string) {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(`axonhub:upstream:key:v1:${key}`));
  return Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, '0')).join('');
}

export async function probeChannelAddress(url: string, channelId?: string) {
  const data = await graphqlRequest<{
    probeChannelAddress: { reachable: boolean; latencyMs: number; statusCode: number; errorCode: string };
  }>(
    `mutation ProbeChannelAddress($url: String!, $channelId: ID) { probeChannelAddress(url: $url, channelId: $channelId) { reachable latencyMs statusCode errorCode } }`,
    { url, channelId }
  );
  return data.probeChannelAddress;
}
