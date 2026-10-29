import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';

export type ConsumptionDimension = 'USER' | 'API_KEY' | 'API_KEY_GROUP' | 'CHANNEL';
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

export type ConsumptionMetrics = z.infer<typeof metricsSchema>;
export type ConsumptionEntry = z.infer<typeof entrySchema>;
export type ConsumptionResult = z.infer<typeof resultSchema>;

const METRIC_FIELDS = `
  requestCount totalTokens totalCost cacheRequests cacheReadTokens cacheInputTokens cacheCreationCost
  cacheHitRate successRate avgFirstTokenMs avgTokensPerSecond avgCostPerRequest avgCostPerMillionTokens cacheCoefficient
`;
const RESULT_FIELDS = `
  entries { id name metrics { ${METRIC_FIELDS} } models { model metrics { ${METRIC_FIELDS} } } }
  generatedAt rangeStart rangeEnd timezone currencyCode
`;
const LEADERBOARD_QUERY = `query ConsumptionLeaderboard($input: ConsumptionFilter!) { consumptionLeaderboard(input: $input) { ${RESULT_FIELDS} } }`;
const USAGE_QUERY = `query UsageStatistics($input: ConsumptionFilter!) { usageStatistics(input: $input) { ${RESULT_FIELDS} } }`;

export function useConsumption(input: ConsumptionFilter, view: 'leaderboard' | 'usage', currentProjectID?: string | null, enabled = true) {
  const projectID = view === 'usage' ? currentProjectID : undefined;
  return useQuery({
    queryKey: ['consumption', view, projectID, input],
    queryFn: async ({ signal }) => {
      const field = view === 'usage' ? 'usageStatistics' : 'consumptionLeaderboard';
      const query = view === 'usage' ? USAGE_QUERY : LEADERBOARD_QUERY;
      const headers = projectID ? { 'X-Project-ID': projectID } : undefined;
      const data = await graphqlRequest<Record<string, unknown>>(query, { input }, headers, { signal });
      return resultSchema.parse(data[field]);
    },
    enabled: enabled && (view === 'leaderboard' || !!projectID),
    refetchInterval: 60_000,
    staleTime: 30_000,
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
