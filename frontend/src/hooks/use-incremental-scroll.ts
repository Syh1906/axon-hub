import { useCallback, useEffect, useRef, useState } from 'react';

export type IncrementalScrollOptions = {
  resetKey: string;
  loadingMore: boolean;
  fetching: boolean;
  hasMore: boolean;
  error: boolean;
  onLoadMore: () => void;
  hasPrevious?: boolean;
  loadingPrevious?: boolean;
  onLoadPrevious?: () => void;
  onTrim?: (rowCount: number) => void;
  onReset: () => void | Promise<unknown>;
  onAtTopChange: (atTop: boolean, reason?: 'scroll' | 'reset') => void;
};

export function useIncrementalScroll(options: IncrementalScrollOptions) {
  const containerRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const lastTop = useRef(0);
  const direction = useRef<'up' | 'down'>('down');
  const [atTop, setAtTop] = useState(true);
  const atTopRef = useRef(true);
  const trimTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const returningToTop = useRef(false);
  const clearTrim = useCallback(() => {
    if (trimTimer.current !== undefined) clearTimeout(trimTimer.current);
    trimTimer.current = undefined;
  }, []);
  useEffect(() => clearTrim, [clearTrim]);

  const backToTop = useCallback(async () => {
    if (returningToTop.current) return;
    returningToTop.current = true;
    clearTrim();
    optionsRef.current.onAtTopChange(false, 'reset');
    // Begin cancellation first, then move the viewport before the first page
    // arrives. Otherwise the old bottom position immediately fetches page two.
    try {
      const reset = optionsRef.current.onReset();
      containerRef.current?.scrollTo({ top: 0, behavior: 'instant' });
      await reset;
      containerRef.current?.scrollTo({ top: 0, behavior: 'instant' });
      lastTop.current = 0;
      direction.current = 'up';
      setAtTop(true);
      atTopRef.current = true;
      optionsRef.current.onAtTopChange(true, 'reset');
    } finally {
      returningToTop.current = false;
    }
  }, [clearTrim]);

  useEffect(() => {
    clearTrim();
    const container = containerRef.current;
    if (container) container.scrollTop = 0;
    lastTop.current = 0;
    direction.current = 'down';
    setAtTop(true);
    atTopRef.current = true;
    optionsRef.current.onAtTopChange(true, 'reset');
  }, [options.resetKey, clearTrim]);

  const onScroll = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;
    const current = optionsRef.current;
    const top = container.scrollTop;
    direction.current = top < lastTop.current ? 'up' : top > lastTop.current ? 'down' : direction.current;
    lastTop.current = top;
    // 回顶产生的滚动不是用户再次进入顶部，不能据此取消或重启首批刷新。
    if (returningToTop.current) return;
    const isTop = top <= 8 && !current.hasPrevious;
    if (atTopRef.current !== isTop) {
      atTopRef.current = isTop;
      setAtTop(isTop);
      current.onAtTopChange(isTop, 'scroll');
    }
    clearTrim();
    if (top < 180 && direction.current === 'up' && current.hasPrevious && !current.fetching && !current.error) {
      current.onLoadPrevious?.();
    }
    if (current.onTrim && direction.current === 'up') {
      // 连续滚动只记位置。停止后才回收尾部批次，避免每个滚动事件
      // 都强制布局并取消查询，造成卡顿和上下往返时的重复取数。
      trimTimer.current = setTimeout(() => {
        const latest = optionsRef.current;
        if (latest.fetching || !latest.onTrim) return;
        if (container.scrollTop <= 8) {
          latest.onTrim(0);
          return;
        }
        // Keep everything above the viewport and one viewport below it. Only
        // complete trailing batches are released; the reading anchor never moves.
        const boundary = container.getBoundingClientRect().bottom + container.clientHeight;
        let keepRows = 0;
        for (const row of container.querySelectorAll<HTMLElement>('[data-scroll-index]')) {
          if (row.getBoundingClientRect().top <= boundary) keepRows = Math.max(keepRows, Number(row.dataset.scrollIndex) + 1);
        }
        latest.onTrim(keepRows);
      }, 180);
    }
    if (
      direction.current === 'down' &&
      !current.fetching &&
      !current.error &&
      current.hasMore &&
      container.scrollHeight - top - container.clientHeight < 180
    ) {
      current.onLoadMore();
    }
  }, [clearTrim]);

  useEffect(() => {
    const container = containerRef.current,
      sentinel = sentinelRef.current;
    if (!container || !sentinel) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        const current = optionsRef.current;
        if (entry.isIntersecting && direction.current === 'down' && current.hasMore && !current.fetching && !current.error)
          current.onLoadMore();
      },
      { root: container, rootMargin: '180px' }
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [options.hasMore, options.fetching, options.error, options.resetKey]);

  return { containerRef, sentinelRef, onScroll, atTop, backToTop };
}
