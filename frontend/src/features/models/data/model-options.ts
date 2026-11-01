import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';

const MODEL_OPTIONS_QUERY = `
  query GetModelGatewayOptions($after: Cursor) {
    models(first: 200, after: $after, orderBy: { field: NAME, direction: ASC }, where: { statusIn: [enabled], typeIn: [chat] }) {
      edges { node { id modelID name } }
      pageInfo { hasNextPage endCursor }
    }
  }
`;

const pageSchema = z.object({
  edges: z.array(z.object({ node: z.object({ id: z.string(), modelID: z.string(), name: z.string() }) })),
  pageInfo: z.object({ hasNextPage: z.boolean(), endCursor: z.string().nullable() }),
});

export function useModelGatewayOptions(enabled: boolean) {
  return useQuery({
    queryKey: ['models', 'gateway-options'],
    enabled,
    staleTime: 30_000,
    gcTime: 0,
    queryFn: async ({ signal }) => {
      const edges: z.infer<typeof pageSchema>['edges'] = [];
      let after: string | null = null;
      do {
        const data = await graphqlRequest<{ models: unknown }>(MODEL_OPTIONS_QUERY, { after }, undefined, { signal });
        const page = pageSchema.parse(data.models);
        edges.push(...page.edges);
        after = page.pageInfo.hasNextPage ? page.pageInfo.endCursor : null;
      } while (after);
      return { edges };
    },
  });
}
