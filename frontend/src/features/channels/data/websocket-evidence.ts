import { z } from 'zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';

const evidenceSchema = z.object({
  state: z.enum(['unknown', 'handshake_only', 'verified', 'unavailable']),
  stage: z.string(),
  reason: z.string(),
  source: z.string(),
  model: z.string(),
  endpoint: z.string(),
  credential: z.string(),
  statusCode: z.number(),
  checkedAt: z.string().nullish(),
  expiresAt: z.string().nullish(),
});
export type WebSocketEvidence = z.infer<typeof evidenceSchema>;
const fields = 'state stage reason source model endpoint credential checkedAt expiresAt statusCode';
const evidenceKey = (channelID: string, modelID: string) => ['channelWebSocketEvidence', channelID, modelID] as const;

export function useWebSocketEvidence(channelID: string, modelID: string) {
  return useQuery({
    queryKey: evidenceKey(channelID, modelID),
    enabled: !!channelID && !!modelID,
    staleTime: 0,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ channelWebSocketEvidence: unknown }>(
        `query ChannelWebSocketEvidence($channelID: ID!, $modelID: String!) {
          channelWebSocketEvidence(channelID: $channelID, modelID: $modelID) { ${fields} }
        }`,
        { channelID, modelID },
        undefined,
        { signal }
      );
      return evidenceSchema.parse(data.channelWebSocketEvidence);
    },
  });
}

export function useVerifyChannelWebSocket() {
  const cache = useQueryClient();
  return useMutation({
    retry: false,
    mutationFn: async (input: { channelID: string; modelID: string; reuseVerified: boolean }) => {
      const data = await graphqlRequest<{ verifyChannelWebSocket: unknown }>(
        `mutation VerifyChannelWebSocket($channelID: ID!, $modelID: String!, $reuseVerified: Boolean!) {
          verifyChannelWebSocket(channelID: $channelID, modelID: $modelID, reuseVerified: $reuseVerified) { ${fields} }
        }`,
        input
      );
      return evidenceSchema.parse(data.verifyChannelWebSocket);
    },
    onSuccess(result, { channelID, modelID }) {
      cache.setQueryData(evidenceKey(channelID, modelID), result);
    },
  });
}
