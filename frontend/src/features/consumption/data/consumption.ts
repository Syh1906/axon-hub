import { useCallback } from 'react';
import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useAuthStore } from '@/stores/authStore';
import { extractNumberID } from '@/lib/utils';

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

// Relative ranges change at midnight in the reporting timezone, which need
// not match the browser's local day. Search only within the refresh interval;
// this also handles days whose UTC offset changes at the boundary.
function timeUntilReportingDayChanges(result: ConsumptionResult, from: number, maximum: number) {
  const day = new Intl.DateTimeFormat('en-CA', {
    timeZone: result.timezone, year: 'numeric', month: '2-digit', day: '2-digit',
  });
  const expected = day.format(new Date(result.generatedAt));
  if (day.format(from) !== expected) return 0;
  if (day.format(from + maximum) === expected) return maximum;
  let low = 0;
  let high = maximum;
  while (high - low > 1) {
    const middle = Math.floor((low + high) / 2);
    if (day.format(from + middle) === expected) low = middle;
    else high = middle;
  }
  return high;
}

export function useConsumption(input: ConsumptionFilter, view: 'leaderboard' | 'usage', currentProjectID?: string | null, enabled = true) {
  const userID = useAuthStore((state) => state.auth.user?.id);
  const projectID = view === 'usage' ? currentProjectID : undefined;
  // These summaries already contain every matching entity. Keep the complete
  // result in the query cache and derive visible rows without another SQL scan.
  // Key/user searches still run on the server: they also match undisplayed names/email.
  const localSearch = input.dimension === 'API_KEY_GROUP' || input.dimension === 'MODEL' || input.dimension === 'CHANNEL';
  const localGroup = input.dimension === 'API_KEY_GROUP';
  const queryInput = {
    ...input,
    search: localSearch ? undefined : input.search,
    groupRef: localGroup ? undefined : input.groupRef,
  };
  const search = localSearch ? input.search?.trim().toLowerCase() : undefined;
  const groupRef = localGroup ? input.groupRef : undefined;
  const select = useCallback((result: ConsumptionResult) => {
    if (!search && groupRef == null) return result;
    return {
      ...result,
      entries: result.entries.filter((entry) =>
        (!search || entry.name.toLowerCase().includes(search)) &&
        (groupRef == null || Number(extractNumberID(entry.id)) === groupRef)
      ),
    };
  }, [search, groupRef]);
  const refreshInterval = input.period === 'YESTERDAY' || input.period === 'CUSTOM' ? 5 * 60_000 : 60_000;
  const relativeRange = input.period !== 'CUSTOM' && input.period !== 'ALL';
  return useQuery({
    queryKey: ['consumption', view, projectID, userID, queryInput],
    queryFn: async ({ signal }) => {
      const field = view === 'usage' ? 'usageStatistics' : 'consumptionLeaderboard';
      const fields = input.dimension === 'MODEL' ? MODEL_RESULT_FIELDS : RESULT_FIELDS;
      const query = `query Consumption($input: ConsumptionFilter!) { ${field}(input: $input) { ${fields} } }`;
      const headers = projectID ? { 'X-Project-ID': projectID } : undefined;
      const data = await graphqlRequest<Record<string, unknown>>(query, { input: queryInput }, headers, { signal });
      return (input.dimension === 'MODEL' ? modelResultSchema : resultSchema).parse(data[field]);
    },
    enabled: enabled && !!userID && (view === 'leaderboard' || !!projectID),
    select,
    refetchInterval: (query) => {
      if (!relativeRange || !query.state.data) return refreshInterval;
      const untilMidnight = timeUntilReportingDayChanges(query.state.data, Date.now(), refreshInterval);
      // A read started before midnight may finish after it with yesterday's
      // range. Refresh that successful result promptly; failed reads keep the
      // normal retry period instead of starting a rapid retry loop.
      return untilMidnight || (query.state.error ? refreshInterval : 1_000);
    },
    staleTime: (query) => relativeRange && query.state.data
      ? timeUntilReportingDayChanges(query.state.data, query.state.dataUpdatedAt, refreshInterval)
      : refreshInterval,
    gcTime: queryInput.search || queryInput.groupRef != null ? 5 * 60_000 : 30 * 60_000,
    refetchOnWindowFocus: (query) => relativeRange && !!query.state.data &&
      timeUntilReportingDayChanges(query.state.data, Date.now(), refreshInterval) === 0,
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
