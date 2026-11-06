import { z } from 'zod';
import { useQuery } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useSelectedProjectId } from '@/stores/projectStore';

// --- Zod Schemas ---

export const analyticsFilterSchema = z.object({
  startTime: z.string().nullable().optional(), // 'YYYY-MM-DD' 或 ISO timestamp
  endTime: z.string().nullable().optional(),
  projectIDs: z.array(z.string()).optional(),
  channelIDs: z.array(z.string()).optional(),
  modelIDs: z.array(z.string()).optional(),
  apiKeyIDs: z.array(z.string()).optional(),
  templateIDs: z.array(z.string()).optional(),
  userIDs: z.array(z.string()).optional(),
});

export type AnalyticsFilter = z.infer<typeof analyticsFilterSchema>;

export const analyticsModelFilterSchema = z.object({
  startTime: z.string().nullable().optional(),
  endTime: z.string().nullable().optional(),
  projectIDs: z.array(z.string()).optional(),
  channelIDs: z.array(z.string()).optional(),
  channelTags: z.array(z.string()).optional(),
  modelIDs: z.array(z.string()).optional(),
});

export type AnalyticsModelFilter = z.infer<typeof analyticsModelFilterSchema>;

export const analyticsOverviewSchema = z.object({
  totalTokens: z.number(),
  totalInputTokens: z.number(),
  totalCachedInputTokens: z.number(),
  totalUncachedInputTokens: z.number(),
  totalOutputTokens: z.number(),
  totalRequests: z.number(),
  totalCost: z.number(),
});

export type AnalyticsOverview = z.infer<typeof analyticsOverviewSchema>;

export const analyticsDailyStatSchema = z.object({
  date: z.string(),
  inputTokens: z.number(),
  cachedInputTokens: z.number(),
  uncachedInputTokens: z.number(),
  outputTokens: z.number(),
  totalTokens: z.number(),
  requestCount: z.number(),
  cost: z.number(),
});

export type AnalyticsDailyStat = z.infer<typeof analyticsDailyStatSchema>;

export const analyticsDimensionStatSchema = z.object({
  id: z.string(),
  name: z.string(),
  requestCount: z.number(),
  inputTokens: z.number(),
  cachedInputTokens: z.number(),
  outputTokens: z.number(),
  totalTokens: z.number(),
  cost: z.number(),
});

export type AnalyticsDimensionStat = z.infer<typeof analyticsDimensionStatSchema>;

export const analyticsAPIKeyModelStatSchema = z.object({
  id: z.string(),
  name: z.string(),
  requestCount: z.number(),
  totalTokens: z.number(),
  cost: z.number(),
});

export type AnalyticsAPIKeyModelStat = z.infer<typeof analyticsAPIKeyModelStatSchema>;

export const analyticsAPIKeyStatSchema = z.object({
  id: z.string(),
  name: z.string(),
  requestCount: z.number(),
  totalTokens: z.number(),
  cost: z.number(),
  models: z.array(analyticsAPIKeyModelStatSchema),
});

export type AnalyticsAPIKeyStat = z.infer<typeof analyticsAPIKeyStatSchema>;

export const analyticsAPIKeyTemplateSchema = z.object({
  id: z.string(),
  name: z.string(),
});

export type AnalyticsAPIKeyTemplate = z.infer<typeof analyticsAPIKeyTemplateSchema>;

export const analyticsModelChannelStatSchema = z.object({
  id: z.string(),
  name: z.string(),
  requestCount: z.number(),
  totalTokens: z.number(),
  cost: z.number(),
  costPerMillion: z.number(),
  successRate: z.number(),
  cacheHitRate: z.number(),
  avgFirstTokenLatencyMs: z.number().nullable(),
  avgOutputTokensPerSecond: z.number().nullable(),
  speedSampleCount: z.number(),
});

export type AnalyticsModelChannelStat = z.infer<
  typeof analyticsModelChannelStatSchema
>;

export const analyticsModelStatSchema = z.object({
  id: z.string(),
  name: z.string(),
  requestCount: z.number(),
  totalTokens: z.number(),
  cost: z.number(),
  costPerMillion: z.number(),
  successRate: z.number(),
  cacheHitRate: z.number(),
  avgFirstTokenLatencyMs: z.number().nullable(),
  avgOutputTokensPerSecond: z.number().nullable(),
  speedSampleCount: z.number(),
  channels: z.array(analyticsModelChannelStatSchema),
});

export type AnalyticsModelStat = z.infer<typeof analyticsModelStatSchema>;

export const analyticsMetadataSchema = z.object({
  earliestDate: z.string().nullable().optional(),
});

export type AnalyticsMetadata = z.infer<typeof analyticsMetadataSchema>;

// --- GraphQL Queries ---

const ANALYTICS_METADATA_QUERY = `
  query GetAnalyticsMetadata {
    analyticsMetadata {
      earliestDate
    }
  }
`;

const ANALYTICS_OVERVIEW_QUERY = `
  query GetAnalyticsOverview($filter: AnalyticsFilter) {
    analyticsOverview(filter: $filter) {
      totalTokens
      totalInputTokens
      totalCachedInputTokens
      totalUncachedInputTokens
      totalOutputTokens
      totalRequests
      totalCost
    }
  }
`;

const ANALYTICS_DAILY_STATS_QUERY = `
  query GetAnalyticsDailyStats($filter: AnalyticsFilter) {
    analyticsDailyStats(filter: $filter) {
      date
      inputTokens
      cachedInputTokens
      uncachedInputTokens
      outputTokens
      totalTokens
      requestCount
      cost
    }
  }
`;

const ANALYTICS_DIMENSION_STATS_QUERY = `
  query GetAnalyticsDimensionStats($filter: AnalyticsFilter, $dimension: String!) {
    analyticsDimensionStats(filter: $filter, dimension: $dimension) {
      id
      name
      requestCount
      inputTokens
      cachedInputTokens
      outputTokens
      totalTokens
      cost
    }
  }
`;

const ANALYTICS_API_KEY_TEMPLATES_QUERY = `
  query GetAnalyticsAPIKeyTemplates {
    analyticsAPIKeyTemplates {
      id
      name
    }
  }
`;

const ANALYTICS_API_KEY_STATS_QUERY = `
  query GetAnalyticsAPIKeyStats($filter: AnalyticsFilter) {
    analyticsAPIKeyStats(filter: $filter) {
      id
      name
      requestCount
      totalTokens
      cost
      models {
        id
        name
        requestCount
        totalTokens
        cost
      }
    }
  }
`;

const ANALYTICS_CHANNEL_TAGS_QUERY = `
  query GetAnalyticsChannelTags {
    analyticsChannelTags
  }
`;

const ANALYTICS_MODEL_STATS_QUERY = `
  query GetAnalyticsModelStats($filter: AnalyticsModelFilter) {
    analyticsModelStats(filter: $filter) {
      id
      name
      requestCount
      totalTokens
      cost
      costPerMillion
      successRate
      cacheHitRate
      avgFirstTokenLatencyMs
      avgOutputTokensPerSecond
      speedSampleCount
      channels {
        id
        name
        requestCount
        totalTokens
        cost
        costPerMillion
        successRate
        cacheHitRate
        avgFirstTokenLatencyMs
        avgOutputTokensPerSecond
        speedSampleCount
      }
    }
  }
`;

// --- Helper: convert filter to GraphQL input ---

// 直接发 YYYY-MM-DD 字符串，后端用系统时区解析（同仪表盘模式）
export function toGraphQLFilter(
  filter: AnalyticsFilter | AnalyticsModelFilter | null
): Record<string, unknown> | null {
  if (!filter) return null;

  const result: Record<string, unknown> = {};

  if (filter.startTime) result.startTime = filter.startTime;
  if (filter.endTime) result.endTime = filter.endTime;
  if (filter.projectIDs && filter.projectIDs.length > 0) result.projectIDs = filter.projectIDs;
  if (filter.channelIDs && filter.channelIDs.length > 0) result.channelIDs = filter.channelIDs;
  if ('channelTags' in filter && filter.channelTags && filter.channelTags.length > 0) {
    result.channelTags = filter.channelTags;
  }
  if (filter.modelIDs && filter.modelIDs.length > 0) result.modelIDs = filter.modelIDs;
  if ('apiKeyIDs' in filter && filter.apiKeyIDs && filter.apiKeyIDs.length > 0) {
    result.apiKeyIDs = filter.apiKeyIDs;
  }
  if ('templateIDs' in filter && filter.templateIDs && filter.templateIDs.length > 0) {
    result.templateIDs = filter.templateIDs;
  }
  if ('userIDs' in filter && filter.userIDs && filter.userIDs.length > 0) {
    result.userIDs = filter.userIDs;
  }

  return Object.keys(result).length > 0 ? result : null;
}

// --- React Query Hooks ---

export function useAnalyticsMetadata() {
  return useQuery({
    queryKey: ['analyticsMetadata'],
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ analyticsMetadata: AnalyticsMetadata }>(
        ANALYTICS_METADATA_QUERY,
        undefined,
        undefined,
        { signal }
      );
      return analyticsMetadataSchema.parse(data.analyticsMetadata);
    },
    staleTime: 5 * 60 * 1000,
  });
}

export function useAnalyticsOverview(filter: AnalyticsFilter | null) {
  return useQuery({
    queryKey: ['analyticsOverview', filter],
    queryFn: async ({ signal }) => {
      const gqlFilter = toGraphQLFilter(filter);
      const data = await graphqlRequest<{ analyticsOverview: AnalyticsOverview }>(
        ANALYTICS_OVERVIEW_QUERY,
        { filter: gqlFilter },
        undefined,
        { signal }
      );
      return analyticsOverviewSchema.parse(data.analyticsOverview);
    },
    refetchInterval: 60000,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    placeholderData: (previousData) => previousData,
  });
}

export function useAnalyticsDailyStats(filter: AnalyticsFilter | null) {
  return useQuery({
    queryKey: ['analyticsDailyStats', filter],
    queryFn: async ({ signal }) => {
      const gqlFilter = toGraphQLFilter(filter);
      const data = await graphqlRequest<{ analyticsDailyStats: AnalyticsDailyStat[] }>(
        ANALYTICS_DAILY_STATS_QUERY,
        { filter: gqlFilter },
        undefined,
        { signal }
      );
      return data.analyticsDailyStats.map((item) => analyticsDailyStatSchema.parse(item));
    },
    refetchInterval: 60000,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    placeholderData: (previousData) => previousData,
  });
}

export function useAnalyticsDimensionStats(filter: AnalyticsFilter | null, dimension: string) {
  return useQuery({
    queryKey: ['analyticsDimensionStats', filter, dimension],
    queryFn: async ({ signal }) => {
      const gqlFilter = toGraphQLFilter(filter);
      const data = await graphqlRequest<{
        analyticsDimensionStats: AnalyticsDimensionStat[];
      }>(
        ANALYTICS_DIMENSION_STATS_QUERY,
        { filter: gqlFilter, dimension },
        undefined,
        { signal }
      );
      return data.analyticsDimensionStats.map((item) => analyticsDimensionStatSchema.parse(item));
    },
    enabled: !!dimension,
    refetchInterval: 60000,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    placeholderData: (previousData) => previousData,
  });
}

export function useAnalyticsAPIKeyTemplates() {
  const selectedProjectId = useSelectedProjectId();

  return useQuery({
    queryKey: ['analyticsAPIKeyTemplates', selectedProjectId],
    queryFn: async ({ signal }) => {
      const headers = selectedProjectId
        ? { 'X-Project-ID': selectedProjectId }
        : undefined;
      const data = await graphqlRequest<{
        analyticsAPIKeyTemplates: AnalyticsAPIKeyTemplate[];
      }>(
        ANALYTICS_API_KEY_TEMPLATES_QUERY,
        undefined,
        headers,
        { signal }
      );
      return data.analyticsAPIKeyTemplates.map((item) =>
        analyticsAPIKeyTemplateSchema.parse(item)
      );
    },
    staleTime: 5 * 60 * 1000,
  });
}

export function useAnalyticsAPIKeyStats(filter: AnalyticsFilter | null) {
  const selectedProjectId = useSelectedProjectId();

  return useQuery({
    queryKey: ['analyticsAPIKeyStats', filter, selectedProjectId],
    queryFn: async ({ signal }) => {
      const projectIDs = filter?.projectIDs?.length
        ? filter.projectIDs
        : selectedProjectId
          ? [selectedProjectId]
          : undefined;
      const gqlFilter = toGraphQLFilter(
        filter ? { ...filter, projectIDs } : projectIDs ? { projectIDs } : null
      );
      const headers = selectedProjectId
        ? { 'X-Project-ID': selectedProjectId }
        : undefined;
      const data = await graphqlRequest<{
        analyticsAPIKeyStats: AnalyticsAPIKeyStat[];
      }>(
        ANALYTICS_API_KEY_STATS_QUERY,
        { filter: gqlFilter },
        headers,
        { signal }
      );
      return data.analyticsAPIKeyStats.map((item) =>
        analyticsAPIKeyStatSchema.parse(item)
      );
    },
    refetchInterval: 60000,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    placeholderData: (previousData) => previousData,
  });
}

export function useAnalyticsChannelTags() {
  const selectedProjectId = useSelectedProjectId();

  return useQuery({
    queryKey: ['analyticsChannelTags', selectedProjectId],
    queryFn: async ({ signal }) => {
      const headers = selectedProjectId
        ? { 'X-Project-ID': selectedProjectId }
        : undefined;
      const data = await graphqlRequest<{ analyticsChannelTags: string[] }>(
        ANALYTICS_CHANNEL_TAGS_QUERY,
        undefined,
        headers,
        { signal }
      );
      return z.array(z.string()).parse(data.analyticsChannelTags);
    },
    staleTime: 5 * 60 * 1000,
  });
}

export function useAnalyticsModelStats(filter: AnalyticsModelFilter | null) {
  const selectedProjectId = useSelectedProjectId();

  return useQuery({
    queryKey: ['analyticsModelStats', filter, selectedProjectId],
    queryFn: async ({ signal }) => {
      const projectIDs = filter?.projectIDs?.length
        ? filter.projectIDs
        : selectedProjectId
          ? [selectedProjectId]
          : undefined;
      const gqlFilter = toGraphQLFilter(
        filter ? { ...filter, projectIDs } : projectIDs ? { projectIDs } : null
      );
      const headers = selectedProjectId
        ? { 'X-Project-ID': selectedProjectId }
        : undefined;
      const data = await graphqlRequest<{
        analyticsModelStats: AnalyticsModelStat[];
      }>(ANALYTICS_MODEL_STATS_QUERY, { filter: gqlFilter }, headers, { signal });
      return data.analyticsModelStats.map((item) =>
        analyticsModelStatSchema.parse(item)
      );
    },
    refetchInterval: 60000,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    placeholderData: (previousData) => previousData,
  });
}
