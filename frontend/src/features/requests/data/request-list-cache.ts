type Page = { edges: { node: { id: string }; cursor?: string }[] };
type Pages = { pages: Page[]; pageParams: unknown[] };

// Zod 会把日期解析为新的 Date；默认缓存比较无法复用这些记录。
// 按 ID 保留未改变的行（包括插入新请求后移位的行），同时比较用量等嵌套字段。
export function shareRequestList(previous: unknown, next: unknown): unknown {
  if (!previous) return next;
  const oldPages = 'pages' in (previous as Pages) ? (previous as Pages).pages : [previous as Page];
  const nextPages = 'pages' in (next as Pages) ? (next as Pages).pages : [next as Page];
  const oldNodes = new Map(oldPages.flatMap((page) => page.edges.map(({ node }) => [node.id, node] as const)));
  const sharedPages = nextPages.map((page, index) => {
    const edges = page.edges.map((edge) => {
      const old = oldNodes.get(edge.node.id);
      if (old === edge.node) return edge;
      return old && JSON.stringify(old) === JSON.stringify(edge.node) ? { ...edge, node: old } : edge;
    });
    const oldPage = oldPages[index];
    if (
      oldPage &&
      edges.length === oldPage.edges.length &&
      edges.every((edge, i) => edge.node === oldPage.edges[i].node && edge.cursor === oldPage.edges[i].cursor) &&
      JSON.stringify({ ...page, edges: undefined }) === JSON.stringify({ ...oldPage, edges: undefined })
    )
      return oldPage;
    return { ...page, edges };
  });
  if (!('pages' in (next as Pages))) return sharedPages[0];
  if (
    sharedPages.length === oldPages.length &&
    sharedPages.every((page, i) => page === oldPages[i]) &&
    JSON.stringify((previous as Pages).pageParams) === JSON.stringify((next as Pages).pageParams)
  )
    return previous;
  return { ...(next as Pages), pages: sharedPages };
}
