import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useSelectedProjectId } from '@/stores/projectStore';
import { usePermissions } from '@/hooks/usePermissions';
import { requestStatusCountVariables } from './request-log-variables';
import { requestStatusSchema } from './schema';

const statusCountsSchema = z.array(z.object({ status: requestStatusSchema, count: z.number().int().nonnegative() }));

export function useRequestLogStatusCounts(where: Record<string, any> | undefined, open: boolean, refreshInterval: number | false) {
  const projectId = useSelectedProjectId();
  const { hasScope } = usePermissions();
  const variables = requestStatusCountVariables(where, projectId);

  return useQuery({
    queryKey: ['request-log-status-counts', variables, projectId],
    enabled: open && hasScope('read_requests'),
    staleTime: 0,
    gcTime: 60_000,
    retry: false,
    refetchInterval: refreshInterval,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ requestLogStatusCounts: unknown }>(
        `query RequestLogStatusCounts($where: RequestWhereInput, $filter: RequestLogFilterInput) {
          requestLogStatusCounts(where: $where, filter: $filter) { status count }
        }`,
        variables,
        projectId ? { 'X-Project-ID': projectId } : undefined,
        { signal }
      );
      return statusCountsSchema.parse(data.requestLogStatusCounts);
    },
  });
}
