import type { InfiniteData, QueryClient, QueryKey } from '@tanstack/react-query';

export type CursorPageParam = string | null | { before: string };

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
