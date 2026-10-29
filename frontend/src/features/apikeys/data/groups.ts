import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useSelectedProjectId } from '@/stores/projectStore';
import { usePermissions } from '@/hooks/usePermissions';

export type KeyGroup = { id: number; name: string; projectID: number };
export function useKeyGroups() {
  const project = useSelectedProjectId();
  const { user } = usePermissions();
  return useQuery({
    queryKey: ['apiKeyGroups', user?.id, project],
    enabled: !!project && !!user,
    staleTime: 5 * 60_000,
    queryFn: async ({ signal }) =>
      (
        await graphqlRequest<{ apiKeyGroupOptions: KeyGroup[] }>(
          `query KeyGroups { apiKeyGroupOptions { id name projectID } }`,
          undefined,
          { 'X-Project-ID': project! },
          { signal }
        )
      ).apiKeyGroupOptions,
  });
}
export function useSaveKeyGroup() {
  const project = useSelectedProjectId();
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: { id?: number; name: string }) =>
      graphqlRequest<{ saveAPIKeyGroup: KeyGroup }>(
        `mutation SaveKeyGroup($id: Int, $name: String!) { saveAPIKeyGroup(id: $id, name: $name) { id name projectID } }`,
        input,
        { 'X-Project-ID': project! }
      ),
    onSuccess: async () => {
      await Promise.all(
        [
          'apiKeyGroups',
          'apiKeys',
          'apiKey',
          'consumption',
          'requests',
          'request',
          'analyticsMetadata',
          'analyticsDimensionStats',
          'analyticsAPIKeyStats',
          'requestStatsByAPIKey',
          'tokenStatsByAPIKey',
          'costStatsByAPIKey',
        ].map((key) => client.invalidateQueries({ queryKey: [key] }))
      );
    },
  });
}
export function useDeleteKeyGroup() {
  const project = useSelectedProjectId();
  const client = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      graphqlRequest(`mutation DeleteKeyGroup($id: Int!) { deleteAPIKeyGroup(id: $id) }`, { id }, { 'X-Project-ID': project! }),
    onSuccess: async () => {
      await Promise.all(
        [
          'apiKeyGroups',
          'apiKeys',
          'apiKey',
          'consumption',
          'requests',
          'request',
          'analyticsMetadata',
          'analyticsDimensionStats',
          'analyticsAPIKeyStats',
          'requestStatsByAPIKey',
          'tokenStatsByAPIKey',
          'costStatsByAPIKey',
        ].map((key) => client.invalidateQueries({ queryKey: [key] }))
      );
    },
  });
}
