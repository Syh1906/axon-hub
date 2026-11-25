import { useCallback } from 'react';
import { z } from 'zod';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';
import { extractNumberID } from '@/lib/utils';
import { hasCachedConsumptionRange, loadConsumptionSlices } from './consumption-slices';

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
  pricedCost: z.number().default(0),
  tokenPricedCost: z.number().default(0),
  pricedTokens: z.number().default(0),
  successCount: z.number().default(0),
  outcomeCount: z.number().default(0),
  firstTokenSum: z.number().default(0),
  firstTokenCount: z.number().default(0),
  speedSum: z.number().default(0),
  coefficientSamples: z.number().default(0),
  coefficientEligible: z.number().default(0),
  coefficientTheoretical: z.number().default(0),
  coefficientObserved: z.number().default(0),
  requestCount: z.number(),
  totalTokens: z.number(),
  totalCost: z.number(),
  billedCost: z.number().nullable().default(null),
  channelCost: z.number().nullable().default(null),
  unpricedRequestCount: z.number(),
  pricedRequestCount: z.number(),
  tokenPricedRequestCount: z.number().default(0),
  speedSampleCount: z.number().default(0),
  cacheRequests: z.number(),
  cacheReadTokens: z.number(),
  cacheInputTokens: z.number(),
  cacheCreationCost: z.number().default(0),
  cacheHitRate: z.number().nullable().default(null),
  successRate: z.number().nullable().default(null),
  avgFirstTokenMs: z.number().nullable().default(null),
  avgTokensPerSecond: z.number().nullable().default(null),
  avgCostPerRequest: z.number().nullable().default(null),
  avgCostPerMillionTokens: z.number().nullable().default(null),
  cacheCoefficient: z.number().nullable().default(null),
});

const entrySchema = z.object({
  channelRemark: z.string().optional(),
  channelProvider: z.string().optional(),
  channelTags: z.array(z.string()).optional(),
  duplicateName: z.boolean().optional(),
  id: z.string(),
  name: z.string(),
  metrics: metricsSchema,
  models: z.array(z.object({ model: z.string(), metrics: metricsSchema })),
});

const performanceMetrics = metricsSchema.pick({
  successCount: true, outcomeCount: true, firstTokenSum: true, firstTokenCount: true, speedSum: true, speedSampleCount: true,
});

export const resultSchema = z.object({
  revision: z.string().default(''),
  cacheSamples: z
    .array(
      z.object({
        channelID: z.string(),
        model: z.string(),
        samples: z.number(),
        eligible: z.number(),
        theoretical: z.number(),
        observed: z.number(),
      })
    )
    .default([]),
  performanceSamples: z.array(z.object({ channelID: z.string(), model: z.string(), metrics: performanceMetrics })).default([]),
  entries: z.array(entrySchema),
  generatedAt: z.string(),
  resolvedAt: z.string().optional(),
  rangeStart: z.string().nullable(),
  rangeEnd: z.string(),
  timezone: z.string(),
  currencyCode: z.string(),
});

export const modelResultSchema = resultSchema.extend({
  entries: z.array(entrySchema.omit({ models: true })).transform((entries) => entries.map((entry) => ({ ...entry, models: [] }))),
});

export type ConsumptionMetrics = z.infer<typeof metricsSchema>;
export type ConsumptionEntry = z.infer<typeof entrySchema>;
export type ConsumptionResult = z.infer<typeof resultSchema> & { useRankedPage?: boolean };

export function consumptionFields(input: ConsumptionFilter, view: 'usage' | 'leaderboard' = 'leaderboard') {
  const performance = input.dimension === 'MODEL' || (input.dimension === 'CHANNEL' && input.metric === 'COST');
  const channel = input.dimension === 'CHANNEL';
  // 跨天比率由浏览器用累计量计算，避免重复传输每日平均值。
  const metrics = `requestCount totalTokens totalCost unpricedRequestCount pricedRequestCount
    ${(channel || view === 'usage') && input.metric === 'COST' ? 'billedCost channelCost' : ''}
    cacheRequests cacheReadTokens cacheInputTokens
    ${input.metric === 'COST' && input.dimension !== 'MODEL' ? 'tokenPricedCost pricedTokens' : ''}
    ${channel && input.metric === 'COST' ? 'pricedCost' : ''}
    ${performance && !channel ? 'speedSampleCount successCount outcomeCount firstTokenSum firstTokenCount speedSum' : ''}`;
  return `${
    input.dimension === 'MODEL'
      ? `entries: modelEntries { id: model name: model metrics { ${metrics} } }`
      : `entries { id name ${channel ? 'channelRemark channelProvider channelTags duplicateName' : ''} metrics { ${metrics} } models { model metrics { ${metrics} } } }`
  }
    ${
      input.dimension === 'CHANNEL'
        ? `cacheSamples { channelID model samples eligible theoretical observed }
      ${performance ? `performanceSamples { channelID model metrics { successCount outcomeCount firstTokenSum firstTokenCount speedSum speedSampleCount } }` : ''}`
        : ''
    }
    revision generatedAt resolvedAt rangeStart rangeEnd timezone currencyCode`;
}

export function consumptionQueryInput(input: ConsumptionFilter) {
  const localSearch = input.dimension === 'API_KEY_GROUP' || input.dimension === 'MODEL' || input.dimension === 'CHANNEL';
  return { ...input, search: localSearch ? undefined : input.search, groupRef: input.dimension === 'API_KEY_GROUP' ? undefined : input.groupRef };
}

export function useConsumption(input: ConsumptionFilter, view: 'leaderboard' | 'usage', currentProjectID?: string | null, enabled = true) {
  const client = useQueryClient();
  const { t } = useTranslation();
  const userID = useAuthStore((state) => state.auth.user?.id);
  const accessScope = useAuthStore((state) =>
    JSON.stringify([state.auth.user?.isOwner, state.auth.user?.scopes, state.auth.user?.projects])
  );
  const projectID = view === 'usage' ? currentProjectID : undefined;
  // These summaries already contain every matching entity. Keep the complete
  // result in the query cache and derive visible rows without another SQL scan.
  // Key/user searches still run on the server: they also match undisplayed names/email.
  const queryInput = consumptionQueryInput(input);
  const search = queryInput.search === undefined ? input.search?.trim().toLowerCase() : undefined;
  const groupRef = input.dimension === 'API_KEY_GROUP' ? input.groupRef : undefined;
  const select = useCallback(
    (result: ConsumptionResult) => {
      if (!search && groupRef == null) return result;
      const providerNames = new Map<string, string>();
      return {
        ...result,
        entries: result.entries.filter((entry) => {
          if (groupRef != null && Number(extractNumberID(entry.id)) !== groupRef) return false;
          if (!search || entry.name.toLowerCase().includes(search)) return true;
          if (input.dimension !== 'CHANNEL') return false;
          const provider = entry.channelProvider ?? '';
          if (provider.toLowerCase().includes(search)) return true;
          if (provider && !providerNames.has(provider)) {
            providerNames.set(provider, t(`channels.providers.${provider}`, { defaultValue: provider }).toLowerCase());
          }
          return (
            Boolean(providerNames.get(provider)?.includes(search)) ||
            (entry.channelTags ?? []).some((tag) => tag.toLowerCase().includes(search))
          );
        }),
      };
    },
    [search, groupRef, input.dimension, t]
  );
  const scopeKey = ['consumption', view, projectID, userID, accessScope];
  const { period: _period, startDate: _start, endDate: _end, ...scope } = queryInput;
  const dayKey = ['consumptionDays', view, projectID, userID, scope, accessScope];
  const wide = input.period === 'ALL' || (input.period === 'CUSTOM' && !!input.startDate && !!input.endDate &&
    Date.parse(input.endDate) - Date.parse(input.startDate) >= 31 * 86400000);
  const preferRanked = wide && !(input.period === 'CUSTOM' && hasCachedConsumptionRange(client, dayKey, input));
  const query = useQuery({
    queryKey: [...scopeKey, queryInput],
    meta: { channelLeaderboard: view === 'leaderboard' && input.dimension === 'CHANNEL' },
    queryFn: async ({ signal }) => {
      const field = view === 'usage' ? 'usageSlices' : 'consumptionSlices';
      const query = `query Consumption($input: ConsumptionFilter!, $after: Time, $known:[ConsumptionSliceRevisionInput!], $asOf:Time, $verify:Boolean!, $pendingDates:[Time!]) { ${field}(input: $input, after: $after, known:$known, asOf:$asOf, verify:$verify, pendingDates:$pendingDates) {
        slices { ${consumptionFields(input, view)} } days { start revision } pending useRankedPage resolvedAt next rangeStart rangeEnd timezone currencyCode
      } }`;
      const headers = projectID ? { 'X-Project-ID': projectID } : undefined;
      return loadConsumptionSlices(
        client,
        dayKey,
        input,
        async (after, known, asOf, verify = false, pendingDates) => {
          const data = await graphqlRequest<Record<string, unknown>>(query, { input: { ...queryInput, allowRankedPage: true }, after, known, asOf, verify, pendingDates }, headers, {
            signal,
          });
          return z
            .object({
              slices: z.array(input.dimension === 'MODEL' ? modelResultSchema : resultSchema),
              next: z.string().nullable(),
              days: z.array(z.object({ start: z.string(), revision: z.string() })),
              pending: z.boolean(),
              useRankedPage: z.boolean(),
              resolvedAt: z.string(),
              rangeStart: z.string().nullable(),
              rangeEnd: z.string(),
              timezone: z.string(),
              currencyCode: z.string(),
            })
            .parse(data[field]);
        },
        signal,
        false
      );
    },
    enabled: enabled && !preferRanked && !!userID && (view === 'leaderboard' || !!projectID),
    select,
    refetchInterval: false,
    refetchOnMount: 'always',
    refetchOnReconnect: false,
    staleTime: 0,
    gcTime: queryInput.search || queryInput.groupRef != null ? 5 * 60_000 : 30 * 60_000,
    refetchOnWindowFocus: false,
  });
  return { ...query, preferRanked };
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

export function useConsumptionGroupOptions(
  input: ConsumptionFilter,
  view: 'leaderboard' | 'usage',
  currentProjectID?: string | null,
  enabled = true
) {
  const userID = useAuthStore((state) => state.auth.user?.id);
  const projectID = view === 'usage' ? currentProjectID : undefined;
  const queryInput = { ...input, dimension: 'API_KEY_GROUP', groupRef: undefined, search: undefined };
  return useQuery({
    queryKey: ['consumptionGroupOptions', view, projectID, userID, queryInput],
    queryFn: async ({ signal }) => {
      const field = view === 'usage' ? 'usageGroupOptions' : 'consumptionGroupOptions';
      const data = await graphqlRequest<Record<string, unknown>>(
        `query ConsumptionGroupOptions($input: ConsumptionFilter!) { ${field}(input: $input) { id name } }`,
        { input: queryInput },
        projectID ? { 'X-Project-ID': projectID } : undefined,
        { signal }
      );
      return z.array(z.object({ id: z.string(), name: z.string() })).parse(data[field]);
    },
    enabled: enabled && !!userID && (view === 'leaderboard' || !!projectID),
    staleTime: 30_000,
    gcTime: 5 * 60_000,
    refetchInterval: false,
    refetchIntervalInBackground: false,
  });
}
