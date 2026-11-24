import { useMemo } from 'react';
import { z } from 'zod';
import { hashKey, useInfiniteQuery, type InfiniteData } from '@tanstack/react-query';
import { graphqlRequest } from '@/gql/graphql';
import { useTranslation } from 'react-i18next';
import { useAuthStore } from '@/stores/authStore';
import { modelResultSchema, resultSchema, type ConsumptionFilter, type ConsumptionMetrics, type ConsumptionResult } from './consumption';
import { waitForSummary } from './consumption-slices';

export type RankSort = { key: keyof ConsumptionMetrics; direction: 'asc' | 'desc' } | null;
type RankPage = { result: ConsumptionResult; nextOffset: number | null };
type Cursor = { offset: number; revision?: string; asOf?: string };

export function useRankedPages(
  input: ConsumptionFilter,
  view: 'usage' | 'leaderboard',
  currentProject: string | null,
  sort: RankSort,
  enabled: boolean
) {
  const { i18n } = useTranslation();
  const project = view === 'usage' ? currentProject : undefined;
  const user = useAuthStore((state) => state.auth.user);
  const search = input.search?.trim().toLowerCase();
  const providers =
    search && input.dimension === 'CHANNEL'
      ? Object.entries(i18n.t('channels.providers', { returnObjects: true }) as Record<string, string>)
          .filter(([, label]) => String(label).toLowerCase().includes(search))
          .map(([key]) => key)
      : [];
  const scope = ['rankedConsumption', view, project, user?.id, user?.isOwner, user?.scopes, user?.projects, input, providers];
  const query = useInfiniteQuery<RankPage, Error, InfiniteData<RankPage>, readonly unknown[], Cursor>({
    queryKey: [...scope, sort],
    // 同一份榜单换排序时保留表格和展开状态。项目、权限或筛选变化须等待对应结果。
    placeholderData: (previous, query) => query && hashKey(query.queryKey.slice(0, -1)) === hashKey(scope) ? previous : undefined,
    enabled: enabled && !!user && (view === 'leaderboard' || !!project),
    initialPageParam: { offset: 0 } as Cursor,
    queryFn: async ({ pageParam, signal }) => {
      const perf = input.dimension === 'MODEL' || (input.dimension === 'CHANNEL' && input.metric === 'COST');
      const metrics = `requestCount totalTokens totalCost pricedRequestCount unpricedRequestCount cacheRequests cacheReadTokens cacheInputTokens cacheHitRate
        ${input.metric === 'COST' ? 'avgCostPerMillionTokens avgCostPerRequest' : ''}
        ${input.dimension === 'CHANNEL' ? 'cacheCoefficient' : ''}
        ${perf ? 'successRate avgFirstTokenMs avgTokensPerSecond speedSampleCount' : ''}`;
      const entries =
        input.dimension === 'MODEL'
          ? `entries:modelEntries { id:model name:model metrics { ${metrics} } }`
          : `entries { id name ${input.dimension === 'CHANNEL' ? 'channelRemark channelProvider channelTags duplicateName' : ''} metrics { ${metrics} } models { model metrics { ${metrics} } } }`;
      const document = `query RankedConsumption($input:ConsumptionFilter!, $usage:Boolean!, $offset:Int!, $revision:String, $sort:String, $desc:Boolean!, $providers:[String!], $asOf:Time) {
        consumptionRankedPage(input:$input,projectUsage:$usage,offset:$offset,revision:$revision,sort:$sort,descending:$desc,providerMatches:$providers,asOf:$asOf) {
          pending resolvedAt nextOffset result { revision resolvedAt generatedAt rangeStart rangeEnd timezone currencyCode ${entries} }
        }
      }`;
      const schema = z.object({
        pending: z.boolean(),
        resolvedAt: z.string(),
        nextOffset: z.number().nullable(),
        result: (input.dimension === 'MODEL' ? modelResultSchema : resultSchema).nullable(),
      });
      let asOf = pageParam.asOf;
      let delay = 300;
      const started = Date.now();
      for (;;) {
        const value = await graphqlRequest<{ consumptionRankedPage: unknown }>(
          document,
          {
            input,
            usage: view === 'usage',
            offset: pageParam.offset,
            revision: pageParam.revision,
            sort: sort?.key,
            desc: sort?.direction === 'desc',
            providers,
            asOf,
          },
          project ? { 'X-Project-ID': project } : undefined,
          { signal }
        );
        const page = schema.parse(value.consumptionRankedPage);
        if (!page.pending && page.result) return { result: page.result, nextOffset: page.nextOffset };
        if (!page.pending || Date.now() - started > 180000)
          throw Object.assign(new Error('统计汇总尚未完成，请重试'), { extensions: { code: 'STATISTICS_PENDING' } });
        asOf ??= page.resolvedAt;
        await waitForSummary(signal, delay);
        delay = Math.min(1200, delay * 2);
      }
    },
    getNextPageParam: ({ result, nextOffset }): Cursor | undefined =>
      nextOffset === null
        ? undefined
        : {
            offset: nextOffset,
            revision: result.revision,
            asOf: result.resolvedAt,
          },
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    gcTime: 5 * 60_000,
  });
  const data = useMemo(() => {
    const pages = query.data?.pages;
    return pages?.length ? { ...pages[0].result, entries: pages.flatMap((page) => page.result.entries) } : undefined;
  }, [query.data]);
  return { ...query, data };
}
