import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { pageInfoSchema } from '@/gql/pagination';

const configuredModelSchema = z.object({
  id: z.string(),
  modelID: z.string(),
  name: z.string(),
  matchType: z.enum(['exact', 'prefix', 'suffix', 'regex']),
  publicModelIds: z.array(z.string()).nullish(),
});
type ConfiguredModel = z.infer<typeof configuredModelSchema>;
export type ConfiguredModelOption = { id: string; names: string[]; fromRule: boolean };

export function configuredModelOptions(models: ConfiguredModel[]) {
  const options = new Map<string, ConfiguredModelOption>();
  const rulesWithoutIDs: string[] = [];
  for (const model of models) {
    const ids = model.matchType === 'exact' ? [model.modelID] : (model.publicModelIds ?? []);
    if (ids.length === 0) rulesWithoutIDs.push(model.name);
    for (const id of ids) {
      const existing = options.get(id);
      if (existing) {
        if (!existing.names.includes(model.name)) existing.names.push(model.name);
      } else options.set(id, { id, names: [model.name], fromRule: model.matchType !== 'exact' });
    }
  }
  return { options: [...options.values()], rulesWithoutIDs };
}

export function useConfiguredModelOptions(enabled: boolean) {
  return useQuery({
    queryKey: ['models', 'channel-options'],
    enabled,
    queryFn: async () => {
      const models: ConfiguredModel[] = [];
      let after: string | undefined;
      do {
        const data = await graphqlRequest<{ models: unknown }>(
          `
          query ConfiguredChannelModelOptions($after: Cursor) {
            models(first: 200, after: $after, orderBy: {field: NAME, direction: ASC}) {
              edges { node { id modelID name matchType publicModelIds } }
              pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
            }
          }
        `,
          { after }
        );
        const page = z.object({ edges: z.array(z.object({ node: configuredModelSchema })), pageInfo: pageInfoSchema }).parse(data.models);
        models.push(...page.edges.map(({ node }) => node));
        after = page.pageInfo.hasNextPage ? (page.pageInfo.endCursor ?? undefined) : undefined;
      } while (after);
      // Public IDs belong to rule models, so filtering is done after expanding
      // them. A modelID filter would incorrectly hide matching public names.
      return configuredModelOptions(models);
    },
  });
}
