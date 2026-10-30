import { useCallback, useEffect, useRef, useState } from 'react';

export type IncrementalScrollOptions = {
  resetKey: string;
  loadingMore: boolean;
  fetching: boolean;
  hasMore: boolean;
  error: boolean;
  onLoadMore: () => void;
  onTrim: (rowCount: number) => void;
  onReset: () => void;
  onAtTopChange: (atTop: boolean) => void;
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

  const backToTop = useCallback(() => {
    containerRef.current?.scrollTo({ top: 0, behavior: 'instant' });
    lastTop.current = 0;
    direction.current = 'up';
    setAtTop(true);
    atTopRef.current = true;
    optionsRef.current.onAtTopChange(true);
    optionsRef.current.onReset();
  }, []);

  useEffect(() => {
    const container = containerRef.current;
    if (container) container.scrollTop = 0;
    lastTop.current = 0;
    direction.current = 'down';
    setAtTop(true);
    atTopRef.current = true;
    optionsRef.current.onAtTopChange(true);
  }, [options.resetKey]);

  const onScroll = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;
    const current = optionsRef.current;
    const top = container.scrollTop;
    direction.current = top < lastTop.current ? 'up' : top > lastTop.current ? 'down' : direction.current;
    lastTop.current = top;
    const isTop = top <= 8;
    if (atTopRef.current !== isTop) {
      atTopRef.current = isTop;
      setAtTop(isTop);
      current.onAtTopChange(isTop);
    }
    if (direction.current === 'up' && !current.fetching) {
      if (isTop) {
        current.onTrim(0);
      } else {
        // Keep everything above the viewport and one viewport below it. Only
        // complete trailing batches are released; the reading anchor never moves.
        const boundary = container.getBoundingClientRect().bottom + container.clientHeight;
        let keepRows = 0;
        for (const row of container.querySelectorAll<HTMLElement>('[data-scroll-index]')) {
          if (row.getBoundingClientRect().top <= boundary) keepRows = Math.max(keepRows, Number(row.dataset.scrollIndex) + 1);
        }
        current.onTrim(keepRows);
      }
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
  }, []);

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
