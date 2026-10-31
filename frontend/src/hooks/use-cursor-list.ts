import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { hashKey, useInfiniteQuery, useQueryClient, type InfiniteData, type QueryKey } from '@tanstack/react-query';
import { trimCursorList } from './cursor-list-cache';

type CursorPage = {
  edges: { node: { id: string }; cursor: string }[];
  pageInfo: { hasNextPage: boolean; hasPreviousPage: boolean; startCursor?: string | null; endCursor?: string | null };
  totalCount?: number;
};

export function useCursorList<Page extends CursorPage>({
  queryKey,
  fetchPage,
  enabled = true,
  interval = false,
  structuralSharing,
  gcTime,
}: {
  queryKey: QueryKey;
  fetchPage: (after: string | null, signal: AbortSignal) => Promise<Page>;
  enabled?: boolean;
  interval?: number | false;
  structuralSharing?: (previous: unknown, next: unknown) => unknown;
  gcTime?: number;
}) {
  const client = useQueryClient();
  const trimming = useRef(false);
  const resetKey = hashKey(queryKey);
  const keyRef = useRef(queryKey);
  keyRef.current = queryKey;
  const [readyKey, setReadyKey] = useState<string>();
  const ready = enabled && readyKey === resetKey;

  useEffect(() => {
    setReadyKey(undefined);
    if (!enabled) return;
    let active = true;
    // Returning to a list also resets its scroll position. Prepare its cache
    // before enabling Query, otherwise mount/refetch replays every old cursor.
    void trimCursorList<Page>(client, keyRef.current, 0).then(() => {
      if (active) setReadyKey(resetKey);
    });
    return () => {
      active = false;
    };
  }, [client, enabled, resetKey]);

  const query = useInfiniteQuery<Page, Error, InfiniteData<Page, string | null>, QueryKey, string | null>({
    queryKey,
    enabled: ready,
    initialPageParam: null,
    queryFn: ({ pageParam, signal }) => fetchPage(pageParam, signal),
    getNextPageParam: (page) => (page.pageInfo.hasNextPage ? page.pageInfo.endCursor : undefined),
    refetchInterval: interval,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
    staleTime: 0,
    structuralSharing,
    gcTime,
  });
  const data = useMemo(() => {
    const pages = ready ? query.data?.pages : query.data?.pages.slice(0, 1);
    if (!pages?.length) return undefined;
    const seen = new Set<string>();
    const edges = pages
      .flatMap((page) => page.edges)
      .filter(({ node }) => {
        if (seen.has(node.id)) return false;
        seen.add(node.id);
        return true;
      });
    return { ...pages[0], edges, pageInfo: { ...pages.at(-1)!.pageInfo, startCursor: pages[0].pageInfo.startCursor } } as Page;
  }, [query.data, ready]);

  const trimAfter = useCallback(
    async (rowCount: number) => {
      if (trimming.current) return;
      trimming.current = true;
      try {
        await trimCursorList<Page>(client, queryKey, rowCount);
      } finally {
        trimming.current = false;
      }
    },
    [client, queryKey]
  );

  const resetToTop = useCallback(async () => {
    await client.cancelQueries({ queryKey, exact: true });
    await trimAfter(0);
    await client.refetchQueries({ queryKey, exact: true });
  }, [client, queryKey, trimAfter]);

  const loadMore = useCallback(() => {
    if (!ready || query.isFetching || trimming.current) return;
    if (query.isError && !query.isFetchNextPageError) void query.refetch();
    else if (query.hasNextPage) void query.fetchNextPage({ cancelRefetch: false });
  }, [ready, query.isFetching, query.isError, query.isFetchNextPageError, query.hasNextPage, query.fetchNextPage, query.refetch]);

  const pauseRefresh = useCallback(() => {
    if (!query.isFetchingNextPage) void client.cancelQueries({ queryKey, exact: true });
  }, [client, queryKey, query.isFetchingNextPage]);

  return { ...query, isLoading: enabled && query.isPending, data, loadMore, trimAfter, resetToTop, pauseRefresh, resetKey };
}
