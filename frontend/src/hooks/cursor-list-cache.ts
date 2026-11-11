import type { InfiniteData, QueryClient, QueryKey } from '@tanstack/react-query';

export type CursorPageParam = string | null | { before: string };

// 获取时间只供缓存判断，不作为表格数据变化；未变的请求行仍可整体复用。
export function cursorListView<Page extends { edges: unknown[]; fetchedAt?: number }>(data: InfiniteData<Page, CursorPageParam>) {
  return {
    ...data,
    pages: data.pages.map((page) => {
      const { fetchedAt: _fetchedAt, ...visible } = page;
      return visible as Page;
    }),
  };
}

export async function prepareCursorListHead<Page extends { edges: unknown[]; fetchedAt?: number }>(client: QueryClient, queryKey: QueryKey, staleTime = 0) {
  await client.cancelQueries({ queryKey, exact: true });
  const state = client.getQueryState<InfiniteData<Page, CursorPageParam>>(queryKey);
  const current = state?.data;
  // 翻页成功不能延长首批有效期；失效、失败和已移出首页的窗口必须重新读首页。
  const updatedAt = current?.pageParams[0] === null && !state?.isInvalidated && !state?.error
    ? Math.min(state?.dataUpdatedAt ?? 0, current.pages[0]?.fetchedAt ?? state?.dataUpdatedAt ?? 0)
    : 0;
  const reusePages = updatedAt > 0 && Date.now() - updatedAt < staleTime;
  client.setQueryData<InfiniteData<Page, CursorPageParam>>(
    queryKey,
    (current) =>
      current?.pages.length && !reusePages
        ? { pages: [current.pages[0]], pageParams: [null] }
        : current,
    // 保留可见记录与真实获取时间，下一次需要读取时从首批开始。
    { updatedAt }
  );
}

export async function trimCursorList<Page extends { edges: unknown[] }>(client: QueryClient, queryKey: QueryKey, rowCount: number) {
  const cached = client.getQueryData<InfiniteData<Page, CursorPageParam>>(queryKey);
  if (!cached) return;
  let keep = 1,
    rows = cached.pages[0]?.edges.length ?? 0;
  while (keep < cached.pages.length && rows < rowCount) rows += cached.pages[keep++].edges.length;
  // A pending second page is not in cached.pages yet. Returning to the head
  // must cancel it even when the cache currently contains only one page.
  if (keep >= cached.pages.length && rowCount > 0) return;
  const updatedAt = client.getQueryState(queryKey)?.dataUpdatedAt;
  await client.cancelQueries({ queryKey, exact: true });
  client.setQueryData<InfiniteData<Page, CursorPageParam>>(
    queryKey,
    (current) =>
      current
        ? {
            pages: current.pages.slice(0, keep),
            pageParams: current.pageParams.slice(0, keep),
          }
        : current,
    // Trimming is not a fresh server response. A returning list must still refresh.
    { updatedAt }
  );
}
