import { useMutation, useQuery } from '@tanstack/react-query';
import { graphqlRequest } from './graphql';
import { useSelectedProjectId } from '@/stores/projectStore';

export interface Model {
  id: string;
  status: 'enabled' | 'disabled' | 'archived';
}

export interface ModelsResponse {
  queryModels: Model[];
}

export interface QueryModelsInput {
  statusIn?: ('enabled' | 'disabled' | 'archived')[];
  includeMapping?: boolean;
  includePrefix?: boolean;
  includeAllChannelModels?: boolean;
}

const MODELS_QUERY = `
  query Models($input: QueryModelsInput!) {
    queryModels(input: $input) {
      id
      status
    }
  }
`;

export function useQueryModels() {
  const selectedProjectId = useSelectedProjectId();

  return useMutation({
    mutationFn: async (input: QueryModelsInput = {}) => {
      const headers = selectedProjectId ? { 'X-Project-ID': selectedProjectId } : undefined;
      const data = await graphqlRequest<{
        queryModels: Model[];
      }>(MODELS_QUERY, { input }, headers);
      return data.queryModels;
    },
  });
}

// Channel administration is not scoped to the currently selected project.
// Keep the identity-only options with channel queries so mutations invalidate them.
export function useChannelModelFilterOptions() {
  return useQuery({
    queryKey: ['channels', 'model-filter-options'],
    staleTime: 30_000,
    gcTime: 5 * 60_000,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<ModelsResponse>(MODELS_QUERY, {
        input: { statusIn: ['enabled', 'disabled'], includeMapping: true, includePrefix: true, includeAllChannelModels: true },
      }, undefined, { signal });
      return data.queryModels;
    },
  });
}
