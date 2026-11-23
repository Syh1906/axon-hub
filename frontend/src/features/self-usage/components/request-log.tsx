import { useEffect, useMemo, useState } from 'react';
import { hashKey, useInfiniteQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useAutoRefreshInterval } from '@/hooks/use-auto-refresh-interval';
import { useIncrementalScroll } from '@/hooks/use-incremental-scroll';
import { useAnimatedList } from '@/hooks/useAnimatedList';
import useInterval from '@/hooks/useInterval';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { AutoRefreshControl } from '@/components/auto-refresh-control';
import { IncrementalListFooter } from '@/components/incremental-list-footer';
import { RequestListModeControls, useRequestListPreference } from '@/features/requests/components/request-list-mode-controls';
import { RequestSessionFilter } from '@/features/requests/components/request-session-filter';
import {
  SelfUsageError,
  fetchRequests,
  type RequestCursor,
  type RequestPage,
  type RequestStatus,
  type SelfUsageIdentity,
  type SelfUsageMeta,
} from '../api';
import { sameUsageScope } from '../scope';
import type { DateRange } from '../utils/date-range';
import { PageError } from './page-error';
import { UsageRequestTable } from './request-table';

export function RequestLog({
  group = false,
  keyValue,
  range,
  meta,
  models,
  onUnauthorized,
  expectedScope,
  refreshStats,
  scopeRevision,
  onScopeMismatch,
  loadRequests,
  queryScope,
  sessionID,
  onSessionFilter,
}: {
  sessionID: string;
  onSessionFilter: (id: string) => void;
  loadRequests?: (
    model: string | null,
    status: RequestStatus | null,
    page: number,
    signal: AbortSignal,
    after: RequestCursor | null
  ) => ReturnType<typeof fetchRequests>;
  queryScope?: readonly unknown[];
  group?: boolean;
  keyValue: string;
  range: DateRange;
  meta: SelfUsageMeta;
  models: string[];
  onUnauthorized: () => void;
  refreshStats: (options: { cancelRefetch: boolean }) => Promise<unknown>;
  expectedScope?: SelfUsageIdentity;
  scopeRevision: number;
  onScopeMismatch: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [model, setModel] = useState<string | null>(null);
  const [status, setStatus] = useState<RequestStatus | null>(null);
  const [page, setPage] = useState(1);
  const [listMode, setListMode] = useRequestListPreference('self-usage-list-mode', 'infinite', 'pages');
  const [refreshMode, setRefreshMode] = useRequestListPreference('self-usage-refresh-mode', 'steady', 'sequential');
  const [interval, setInterval] = useAutoRefreshInterval('self-usage-auto-refresh-interval-ms');
  const [atTop, setAtTop] = useState(true);
  const infinite = listMode === 'infinite';
  const active = atTop && (infinite || page === 1);
  const listKey = [...(queryScope ?? ['selfUsage']), 'requests', scopeRevision, group, range, model, status, sessionID, listMode];
  const queryKey = [...listKey, page];
  const resetKey = hashKey(queryKey);
  const result = useInfiniteQuery({
    queryKey,
    initialPageParam: null as RequestCursor | null,
    queryFn: ({ pageParam, signal }) =>
      loadRequests
        ? loadRequests(model, status, page, signal, pageParam)
        : fetchRequests(keyValue, range.start, range.end, model, status, page, signal, group, sessionID || undefined, pageParam),
    getNextPageParam: (last) => {
      const tail = last.items.at(-1);
      return infinite && last.hasMore && tail ? { createdAt: tail.createdAt, id: tail.id } : undefined;
    },
    // Keep the previous numbered page on screen only within the same filtered scope.
    placeholderData: (previous, query) =>
      !infinite && query && hashKey(query.queryKey.slice(0, -1)) === hashKey(listKey) ? previous : undefined,
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  useEffect(() => {
    if (result.error instanceof SelfUsageError && result.error.status === 401) onUnauthorized();
  }, [result.error, onUnauthorized]);
  const batches = result.data?.pages;
  const head = batches?.[0];
  const scopeMismatch =
    !!expectedScope &&
    batches?.some((batch) => !sameUsageScope(batch, expectedScope) || batch.channelVisible !== head?.channelVisible);
  useEffect(() => {
    if (scopeMismatch) onScopeMismatch();
  }, [scopeMismatch, onScopeMismatch]);
  const forbidden = result.error instanceof SelfUsageError && (result.error.status === 401 || result.error.status === 403);
  const data = expectedScope && !scopeMismatch && !forbidden ? head : undefined;
  const items = useMemo(() => (data && batches ? batches.flatMap((batch) => batch.items) : []), [data, batches]);
  const changeSession = (id: string) => {
    setPage(1);
    onSessionFilter(id);
  };
  const pages = Math.max(1, Math.ceil((data?.total ?? 0) / (data?.pageSize ?? 20)));

  async function refresh() {
    if (infinite) {
      await client.cancelQueries({ queryKey, exact: true });
      client.setQueryData<InfiniteData<RequestPage, RequestCursor | null>>(queryKey, (cached) =>
        cached && cached.pages.length > 1 ? { pages: cached.pages.slice(0, 1), pageParams: [null] } : cached
      );
    }
    await Promise.all([client.refetchQueries({ queryKey, exact: true, type: 'active' }), refreshStats({ cancelRefetch: false })]);
  }

  const loadMore = () => {
    if (data && result.hasNextPage && !result.isFetching) void result.fetchNextPage({ cancelRefetch: false });
  };
  const scrolling = useIncrementalScroll({
    resetKey,
    loadingMore: result.isFetchingNextPage,
    fetching: result.isFetching,
    hasMore: !!data && result.hasNextPage,
    error: result.isError,
    onLoadMore: loadMore,
    onReset: refresh,
    onAtTopChange: (value, reason) => {
      setAtTop(value);
      if (reason !== 'scroll') return;
      if (!value && result.isRefetching) void client.cancelQueries({ queryKey, exact: true });
      if (value && infinite) void refresh();
    },
  });
  const resumeKey = useInterval(
    () => {
      if (!result.isFetching) void refresh();
    },
    active && !forbidden && !scopeMismatch ? interval : null,
    { refreshOnResume: true }
  );
  const animate = refreshMode === 'sequential' && active && interval !== null;
  const displayed = useAnimatedList(items, animate && !!data, data?.pageSize, hashKey([resetKey, data?.scopeID, data?.channelVisible, resumeKey]));
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('selfUsage.requests.title')}</CardTitle>
        <div className='flex flex-wrap items-center gap-2 pt-2'>
          <Select
            value={model ?? '__all__'}
            onValueChange={(value) => {
              setModel(value === '__all__' ? null : value);
              setPage(1);
            }}
          >
            <SelectTrigger className='w-48' aria-label={t('selfUsage.requests.model')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='__all__'>{t('selfUsage.requests.allModels')}</SelectItem>
              {models.map((value) => (
                <SelectItem key={value} value={value}>
                  {value}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={status ?? '__all__'}
            onValueChange={(value) => {
              setStatus(value === '__all__' ? null : (value as RequestStatus));
              setPage(1);
            }}
          >
            <SelectTrigger className='w-44' aria-label={t('selfUsage.requests.status')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='__all__'>{t('selfUsage.requests.allStatuses')}</SelectItem>
              {(['SUCCESS', 'FAILED', 'PROCESSING'] as const).map((value) => (
                <SelectItem key={value} value={value}>
                  {t(`selfUsage.requests.${value.toLowerCase()}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <RequestSessionFilter key={sessionID} value={sessionID} onChange={changeSession} />
          <div className='ml-auto flex flex-wrap items-center gap-2'>
            <RequestListModeControls
              listMode={listMode}
              onListModeChange={(mode) => {
                setPage(1);
                setListMode(mode);
              }}
              refreshMode={refreshMode}
              onRefreshModeChange={setRefreshMode}
              paused={!active && interval !== null}
            />
            <AutoRefreshControl
              interval={interval}
              onIntervalChange={(value) => {
                setInterval(value);
                if (value !== null && active && !result.isFetching) void refresh();
              }}
              active={active}
              refreshing={result.isFetching && !result.isFetchingNextPage}
              onRefresh={scrolling.backToTop}
            />
          </div>
        </div>
        {sessionID && <p className='text-muted-foreground text-sm'>{t('requests.session.retention')}</p>}
      </CardHeader>
      <CardContent className='space-y-4'>
        {result.error && (
          <PageError
            error={result.error}
            retry={() => {
              if (result.isFetchNextPageError) loadMore();
              else void refresh();
            }}
          />
        )}
        {(result.isPending || scopeMismatch || !expectedScope) && <p role='status'>{t('selfUsage.loading')}</p>}
        <div
          ref={scrolling.containerRef}
          onScroll={scrolling.onScroll}
          className='relative max-h-[70vh] overflow-auto [overflow-anchor:none]'
          role='region'
          tabIndex={0}
          aria-label={t('selfUsage.requests.title')}
        >
          {data && (
            <UsageRequestTable
              onSessionFilter={changeSession}
              items={displayed}
              meta={meta}
              channelVisible={data.channelVisible}
              containerRef={scrolling.containerRef}
              animate={animate}
            />
          )}
          <div ref={scrolling.sentinelRef} className='h-px' />
        </div>
        {data &&
          (infinite ? (
            <IncrementalListFooter
              loaded={items.length}
              total={batches?.at(-1)?.total}
              loading={result.isFetchingNextPage}
              hasMore={result.hasNextPage}
              error={result.isFetchNextPageError}
              atTop={scrolling.atTop}
              onLoadMore={loadMore}
              onBackToTop={scrolling.backToTop}
            />
          ) : (
            <div className='flex items-center justify-between gap-2 text-sm'>
              <Button variant='outline' size='sm' disabled={result.isPlaceholderData || page <= 1} onClick={() => setPage(page - 1)}>
                {t('selfUsage.requests.previous')}
              </Button>
              <span>{t('selfUsage.requests.page', { page: data.page, pages, total: data.total })}</span>
              <Button variant='outline' size='sm' disabled={result.isPlaceholderData || page >= pages} onClick={() => setPage(page + 1)}>
                {t('selfUsage.requests.next')}
              </Button>
            </div>
          ))}
      </CardContent>
    </Card>
  );
}
