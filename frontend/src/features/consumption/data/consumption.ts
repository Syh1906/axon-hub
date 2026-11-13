import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useAuthStore } from '@/stores/authStore';
import { extractNumberID } from '@/lib/utils';
import { loadConsumptionSlices } from './consumption-slices';

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
  pricedCost: z.number().default(0), tokenPricedCost: z.number().default(0), pricedTokens: z.number().default(0),
  successCount: z.number().default(0), outcomeCount: z.number().default(0), firstTokenSum: z.number().default(0),
  firstTokenCount: z.number().default(0), speedSum: z.number().default(0),
  coefficientSamples: z.number().default(0), coefficientEligible: z.number().default(0),
  coefficientTheoretical: z.number().default(0), coefficientObserved: z.number().default(0),
  requestCount: z.number(),
  totalTokens: z.number(),
  totalCost: z.number(),
  unpricedRequestCount: z.number(),
  pricedRequestCount: z.number(),
  tokenPricedRequestCount: z.number(),
  speedSampleCount: z.number().default(0),
  cacheRequests: z.number(),
  cacheReadTokens: z.number(),
  cacheInputTokens: z.number(),
  cacheCreationCost: z.number(),
  cacheHitRate: z.number().nullable(),
  successRate: z.number().nullable().default(null),
  avgFirstTokenMs: z.number().nullable().default(null),
  avgTokensPerSecond: z.number().nullable().default(null),
  avgCostPerRequest: z.number().nullable(),
  avgCostPerMillionTokens: z.number().nullable(),
  cacheCoefficient: z.number().nullable(),
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

const resultSchema = z.object({
  revision: z.string().default(''),
  cacheSamples: z.array(z.object({ channelID:z.string(),model:z.string(),samples:z.number(),eligible:z.number(),theoretical:z.number(),observed:z.number() })).default([]),
  performanceSamples: z.array(z.object({ channelID:z.string(),name:z.string(),model:z.string(),metrics:metricsSchema })).default([]),
  entries: z.array(entrySchema),
  generatedAt: z.string(),
  resolvedAt: z.string().optional(),
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

export function consumptionFields(input: ConsumptionFilter) {
  const performance = input.dimension === 'MODEL' || (input.dimension === 'CHANNEL' && input.metric === 'COST');
  const metrics = `requestCount totalTokens totalCost unpricedRequestCount pricedRequestCount tokenPricedRequestCount
    cacheRequests cacheReadTokens cacheInputTokens cacheCreationCost cacheHitRate avgCostPerRequest avgCostPerMillionTokens cacheCoefficient
    pricedCost tokenPricedCost pricedTokens
    ${performance ? 'successRate avgFirstTokenMs avgTokensPerSecond speedSampleCount successCount outcomeCount firstTokenSum firstTokenCount speedSum' : ''}
    ${input.dimension === 'CHANNEL' ? 'coefficientSamples coefficientEligible coefficientTheoretical coefficientObserved' : ''}`;
  return `${input.dimension === 'MODEL'
    ? `entries: modelEntries { id: model name: model metrics { ${metrics} } }`
    : `entries { id name channelRemark channelProvider channelTags duplicateName metrics { ${metrics} } models { model metrics { ${metrics} } } }`}
    ${input.dimension === 'CHANNEL' ? `cacheSamples { channelID model samples eligible theoretical observed }
      ${performance ? `performanceSamples { channelID name model metrics { ${metrics} } }` : ''}` : ''}
    revision generatedAt resolvedAt rangeStart rangeEnd timezone currencyCode`;
}

// Relative ranges change at midnight in the reporting timezone, which need
// not match the browser's local day. Search only within the refresh interval;
// this also handles days whose UTC offset changes at the boundary.
function timeUntilReportingDayChanges(result: ConsumptionResult, from: number, maximum: number) {
  const day = new Intl.DateTimeFormat('en-CA', {
    timeZone: result.timezone, year: 'numeric', month: '2-digit', day: '2-digit',
  });
  const expected = day.format(new Date(result.resolvedAt ?? result.generatedAt));
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
  const client = useQueryClient();
  const { t } = useTranslation();
  const userID = useAuthStore((state) => state.auth.user?.id);
  const accessScope = useAuthStore((state) => JSON.stringify([state.auth.user?.isOwner, state.auth.user?.scopes, state.auth.user?.projects]));
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
        return Boolean(providerNames.get(provider)?.includes(search)) ||
          (entry.channelTags ?? []).some((tag) => tag.toLowerCase().includes(search));
      }),
    };
  }, [search, groupRef, input.dimension, t]);
  const refreshInterval = input.period === 'YESTERDAY' || input.period === 'CUSTOM' ? 5 * 60_000 : 60_000;
  const relativeRange = input.period !== 'CUSTOM' && input.period !== 'ALL';
  return useQuery({
    queryKey: ['consumption', view, projectID, userID, accessScope, queryInput],
    queryFn: async ({ signal }) => {
      const field = view === 'usage' ? 'usageSlices' : 'consumptionSlices';
      const query = `query Consumption($input: ConsumptionFilter!, $after: Time, $known:[ConsumptionSliceRevisionInput!], $asOf:Time, $verify:Boolean!) { ${field}(input: $input, after: $after, known:$known, asOf:$asOf, verify:$verify) {
        slices { ${consumptionFields(input)} } days { start revision } pending resolvedAt next rangeStart rangeEnd timezone currencyCode
      } }`;
      const headers = projectID ? { 'X-Project-ID': projectID } : undefined;
      const { period: _period, startDate: _start, endDate: _end, ...scope } = queryInput;
      return loadConsumptionSlices(client, ['consumptionDays', view, projectID, userID, scope, accessScope], input, async (after, known, asOf, verify = false) => {
        const data = await graphqlRequest<Record<string, unknown>>(query, { input: queryInput, after, known, asOf, verify }, headers, { signal });
        return z.object({ slices: z.array(input.dimension === 'MODEL' ? modelResultSchema : resultSchema), next: z.string().nullable(),
          days:z.array(z.object({start:z.string(),revision:z.string()})),pending:z.boolean(),resolvedAt:z.string(),
          rangeStart: z.string().nullable(), rangeEnd: z.string(), timezone: z.string(), currencyCode: z.string() }).parse(data[field]);
      }, signal, !client.getQueryData(['consumption', view, projectID, userID, accessScope, queryInput]));
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

export function useConsumptionGroupOptions(input: ConsumptionFilter, view: 'leaderboard' | 'usage', currentProjectID?: string | null, enabled = true) {
  const userID = useAuthStore((state) => state.auth.user?.id);
  const projectID = view === 'usage' ? currentProjectID : undefined;
  const queryInput = { ...input, dimension: 'API_KEY_GROUP', groupRef: undefined, search: undefined };
  return useQuery({
    queryKey: ['consumptionGroupOptions', view, projectID, userID, queryInput],
    queryFn: async ({ signal }) => {
      const field = view === 'usage' ? 'usageGroupOptions' : 'consumptionGroupOptions';
      const data = await graphqlRequest<Record<string, unknown>>(
        `query ConsumptionGroupOptions($input: ConsumptionFilter!) { ${field}(input: $input) { id name } }`,
        { input: queryInput }, projectID ? { 'X-Project-ID': projectID } : undefined, { signal }
      );
      return z.array(z.object({ id: z.string(), name: z.string() })).parse(data[field]);
    },
    enabled: enabled && !!userID && (view === 'leaderboard' || !!projectID),
    staleTime: 30_000,
    gcTime: 5 * 60_000,
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
  });
}
