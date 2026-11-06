import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';

export type ConsumptionDimension = 'USER' | 'API_KEY' | 'API_KEY_GROUP' | 'CHANNEL' | 'MODEL';
export type ConsumptionMetric = 'COST' | 'CACHE';
export type ConsumptionPeriod = 'TODAY' | 'YESTERDAY' | 'WEEK' | 'MONTH' | 'ALL' | 'CUSTOM';
export type ConsumptionDateRange = { period: ConsumptionPeriod; startDate?: string; endDate?: string };
export type ConsumptionFilter = ConsumptionDateRange & {
  dimension: ConsumptionDimension;
  metric: ConsumptionMetric;
  projectID?: string;
  groupRef?: number;
  search?: string;
};

const metricsSchema = z.object({
  requestCount: z.number(),
  totalTokens: z.number(),
  totalCost: z.number(),
  unpricedRequestCount: z.number(),
  pricedRequestCount: z.number(),
  tokenPricedRequestCount: z.number(),
  speedSampleCount: z.number(),
  cacheRequests: z.number(),
  cacheReadTokens: z.number(),
  cacheInputTokens: z.number(),
  cacheCreationCost: z.number(),
  cacheHitRate: z.number().nullable(),
  successRate: z.number().nullable(),
  avgFirstTokenMs: z.number().nullable(),
  avgTokensPerSecond: z.number().nullable(),
  avgCostPerRequest: z.number().nullable(),
  avgCostPerMillionTokens: z.number().nullable(),
  cacheCoefficient: z.number().nullable(),
});

const entrySchema = z.object({
  id: z.string(),
  name: z.string(),
  metrics: metricsSchema,
  models: z.array(z.object({ model: z.string(), metrics: metricsSchema })),
});

const resultSchema = z.object({
  entries: z.array(entrySchema),
  generatedAt: z.string(),
  rangeStart: z.string().nullable(),
  rangeEnd: z.string(),
  timezone: z.string(),
  currencyCode: z.string(),
});

const modelResultSchema = resultSchema.extend({
  entries: z.array(entrySchema.omit({ models: true })).transform((entries) => entries.map((entry) => ({ ...entry, models: [] }))),
});

export type ConsumptionMetrics = z.infer<typeof metricsSchema>;
export type ConsumptionEntry = z.infer<typeof entrySchema>;
export type ConsumptionResult = z.infer<typeof resultSchema>;

const METRIC_FIELDS = `
  requestCount totalTokens totalCost unpricedRequestCount pricedRequestCount tokenPricedRequestCount speedSampleCount cacheRequests cacheReadTokens cacheInputTokens cacheCreationCost
  cacheHitRate successRate avgFirstTokenMs avgTokensPerSecond avgCostPerRequest avgCostPerMillionTokens cacheCoefficient
`;
const RESULT_FIELDS = `
  entries { id name metrics { ${METRIC_FIELDS} } models { model metrics { ${METRIC_FIELDS} } } }
  generatedAt rangeStart rangeEnd timezone currencyCode
`;
const MODEL_RESULT_FIELDS = `
  entries: modelEntries { id: model name: model metrics { ${METRIC_FIELDS} } }
  generatedAt rangeStart rangeEnd timezone currencyCode
`;

export function useConsumption(input: ConsumptionFilter, view: 'leaderboard' | 'usage', currentProjectID?: string | null, enabled = true) {
  const projectID = view === 'usage' ? currentProjectID : undefined;
  return useQuery({
    queryKey: ['consumption', view, projectID, input],
    queryFn: async ({ signal }) => {
      const field = view === 'usage' ? 'usageStatistics' : 'consumptionLeaderboard';
      const fields = input.dimension === 'MODEL' ? MODEL_RESULT_FIELDS : RESULT_FIELDS;
      const query = `query Consumption($input: ConsumptionFilter!) { ${field}(input: $input) { ${fields} } }`;
      const headers = projectID ? { 'X-Project-ID': projectID } : undefined;
      const data = await graphqlRequest<Record<string, unknown>>(query, { input }, headers, { signal });
      return (input.dimension === 'MODEL' ? modelResultSchema : resultSchema).parse(data[field]);
    },
    enabled: enabled && (view === 'leaderboard' || !!projectID),
    refetchInterval: input.period === 'YESTERDAY' || input.period === 'CUSTOM' ? 5 * 60_000 : 60_000,
    staleTime: 30_000,
    gcTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });
}

export function useConsumptionProjects(enabled: boolean) {
  return useQuery({
    queryKey: ['consumptionProjects'],
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ consumptionProjects: unknown }>(
        `query ConsumptionProjects { consumptionProjects { id name } }`,
        undefined,
        undefined,
        { signal }
      );
      return z.array(z.object({ id: z.string(), name: z.string() })).parse(data.consumptionProjects);
    },
    enabled,
    staleTime: 60_000,
  });
}
