import { useCallback, useMemo, useRef } from 'react';
import { hashKey, useInfiniteQuery, useQueryClient, type InfiniteData, type QueryKey } from '@tanstack/react-query';

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
}: {
  queryKey: QueryKey;
  fetchPage: (after: string | null, signal: AbortSignal) => Promise<Page>;
  enabled?: boolean;
  interval?: number | false;
}) {
  const client = useQueryClient();
  const trimming = useRef(false);
  const query = useInfiniteQuery<Page, Error, InfiniteData<Page, string | null>, QueryKey, string | null>({
    queryKey,
    enabled,
    initialPageParam: null,
    queryFn: ({ pageParam, signal }) => fetchPage(pageParam, signal),
    getNextPageParam: (page) => (page.pageInfo.hasNextPage ? page.pageInfo.endCursor : undefined),
    refetchInterval: interval,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  const data = useMemo(() => {
    const pages = query.data?.pages;
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
  }, [query.data]);

  const trimAfter = useCallback(
    async (rowCount: number) => {
      const cached = client.getQueryData<InfiniteData<Page, string | null>>(queryKey);
      if (!cached || trimming.current) return;
      let keep = 1,
        rows = cached.pages[0]?.edges.length ?? 0;
      while (keep < cached.pages.length && rows < rowCount) rows += cached.pages[keep++].edges.length;
      if (keep >= cached.pages.length) return;
      trimming.current = true;
      try {
        await client.cancelQueries({ queryKey, exact: true });
        client.setQueryData<InfiniteData<Page, string | null>>(queryKey, (current) =>
          current
            ? {
                pages: current.pages.slice(0, keep),
                pageParams: current.pageParams.slice(0, keep),
              }
            : current
        );
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
    if (query.isFetching || trimming.current) return;
    if (query.isError && !query.isFetchNextPageError) void query.refetch();
    else if (query.hasNextPage) void query.fetchNextPage({ cancelRefetch: false });
  }, [query.isFetching, query.isError, query.isFetchNextPageError, query.hasNextPage, query.fetchNextPage, query.refetch]);

  const pauseRefresh = useCallback(() => {
    if (!query.isFetchingNextPage) void client.cancelQueries({ queryKey, exact: true });
  }, [client, queryKey, query.isFetchingNextPage]);

  return { ...query, data, loadMore, trimAfter, resetToTop, pauseRefresh, resetKey: hashKey(queryKey) };
}
