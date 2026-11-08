import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { flexRender, type Row, type Table } from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import type { IncrementalScrollOptions } from '@/hooks/use-incremental-scroll';
import { TableBody, TableCell, TableRow } from '@/components/ui/table';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import type { Request } from '../data/schema';
import { RequestContinuationHistory, type ContinuationHistoryPosition } from './request-continuation-history';
import type { RequestDiagnosticTab } from './request-log-cells';

export const REQUEST_COLUMN_WIDTHS: Record<string, number> = {
  id: 145,
  channel: 210,
  modelID: 200,
  provider: 100,
  tokens: 215,
  cache: 160,
  cost: 185,
  duration: 160,
  caller: 185,
  createdAt: 160,
  details: 50,
  apiFormat: 160,
  clientIP: 160,
};

const RequestRow = memo(
  function RequestRow({
    row,
    entering,
  }: {
    row: Row<Request>;
    columns: Table<Request>['options']['columns'];
    columnState: string;
    entering: boolean;
  }) {
    // 列顺序和可见性变化必须更新单元格；滚动位置变化不应重建它们。
    return row.getVisibleCells().map((cell) => (
      <TableCell
        key={cell.id}
        className={cn(
          cell.column.columnDef.meta?.className,
          'border-b border-[var(--table-border)] py-3',
          entering && 'animate-in fade-in-0 duration-200 motion-reduce:animate-none'
        )}
      >
        {flexRender(cell.column.columnDef.cell, cell.getContext())}
      </TableCell>
    ));
  },
  (previous, next) =>
    previous.row.original === next.row.original &&
    previous.row.index === next.row.index &&
    previous.columns === next.columns &&
    previous.columnState === next.columnState &&
    previous.entering === next.entering
);

export function RequestsTableBody({
  table,
  containerRef,
  scroll,
  loading,
  animate,
  onHistoryBodyClick,
  onViewDetail,
}: {
  table: Table<Request>;
  containerRef: RefObject<HTMLDivElement | null>;
  scroll: IncrementalScrollOptions;
  loading?: boolean;
  animate: boolean;
  onHistoryBodyClick: (requests: Request[], id: string, index: number, tab: RequestDiagnosticTab) => void;
  onViewDetail: (id: string) => void;
}) {
  const { t } = useTranslation();
  const rows = table.getRowModel().rows;
  const expanded = table.getState().expanded;
  const entries = useMemo(() => rows.flatMap((row, index) => {
    const main = { row, index, history: false, key: row.id };
    return row.getIsExpanded() ? [main, { row, index, history: true, key: `${row.id}-history` }] : [main];
  }), [rows, expanded]);
  const positions = useMemo(() => new Map<string, ContinuationHistoryPosition>(), [scroll.resetKey]);
  const [, updateHistoryPage] = useState(0);
  const getPosition = (id: string) => {
    if (!positions.has(id)) positions.set(id, { cursors: [], scrollTop: 0 });
    return positions.get(id)!;
  };
  useEffect(() => {
    const visible = new Set(rows.map((row) => row.id));
    for (const id of positions.keys()) if (!visible.has(id)) positions.delete(id);
  }, [rows, positions]);
  const getItemKey = useCallback((index: number) => entries[index].key, [entries]);
  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => containerRef.current,
    getItemKey,
    estimateSize: (index) => entries[index].history ? 320 : 80,
    overscan: 10,
    scrollMargin: 40,
    useFlushSync: false,
  });
  const items = virtualizer.getVirtualItems();
  const windowPosition = useRef<{
    firstId?: string;
    starts: Map<string, number>;
    hasPrevious?: boolean;
    resetKey: string;
  } | null>(null);
  useLayoutEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const starts = new Map(entries.flatMap((entry, index) => entry.history ? [] :
      [[entry.row.id, virtualizer.measurementsCache[index]?.start ?? 40] as const]
    ));
    const previous = windowPosition.current;
    const firstId = rows[0]?.id;
    if (
      previous?.resetKey === scroll.resetKey &&
      previous.firstId &&
      firstId &&
      previous.firstId !== firstId &&
      (previous.hasPrevious || scroll.hasPrevious)
    ) {
      // Compensate for the inserted/removed prefix using the current physical
      // scroll offset. A fast response may arrive before a scroll render.
      const insertedStart = starts.get(previous.firstId);
      const removedStart = previous.starts.get(firstId);
      const shift =
        insertedStart !== undefined
          ? insertedStart - (previous.starts.get(previous.firstId) ?? 40)
          : removedStart !== undefined
            ? (starts.get(firstId) ?? 40) - removedStart
            : 0;
      if (shift) virtualizer.scrollToOffset(Math.max(0, container.scrollTop + shift));
    }
    windowPosition.current = { firstId, starts, hasPrevious: scroll.hasPrevious, resetKey: scroll.resetKey };
  });
  const lastIndex = entries[items.at(-1)?.index ?? -1]?.index ?? -1;
  const firstIndex = entries[items[0]?.index ?? -1]?.index ?? -1;
  useEffect(() => {
    // A cache window change can leave one render with the previous virtual range.
    // Read the actual viewport before starting another page request.
    const container = containerRef.current;
    const nearBottom = !!container && container.scrollHeight - container.scrollTop - container.clientHeight < 400;
    if (rows.length > 0 && lastIndex >= rows.length - 5 && nearBottom && scroll.hasMore && !scroll.fetching && !scroll.error)
      scroll.onLoadMore();
  }, [lastIndex, rows.length, scroll.hasMore, scroll.fetching, scroll.error, scroll.onLoadMore]);
  useEffect(() => {
    if (
      rows.length > 0 &&
      firstIndex >= 0 &&
      firstIndex < 5 &&
      (containerRef.current?.scrollTop ?? Infinity) < 180 &&
      scroll.hasPrevious &&
      !scroll.fetching &&
      !scroll.error
    ) {
      scroll.onLoadPrevious?.();
    }
  }, [firstIndex, rows.length, scroll.hasPrevious, scroll.fetching, scroll.error, scroll.onLoadPrevious]);

  const columnCount = table.getVisibleLeafColumns().length;
  const state = table.getState();
  const columnState = JSON.stringify([state.columnVisibility, state.columnOrder, expanded]);
  const top = Math.max(0, (items[0]?.start ?? 40) - 40);
  const bottom = Math.max(0, virtualizer.getTotalSize() - ((items.at(-1)?.end ?? 40) - 40));

  return (
    <TableBody className='!bg-[var(--table-background)]'>
      {loading ? (
        <TableSkeleton rows={8} columns={columnCount} />
      ) : rows.length === 0 ? (
        <TableRow>
          <TableCell colSpan={columnCount} className='h-24 text-center'>
            {t('common.noData')}
          </TableCell>
        </TableRow>
      ) : (
        <>
          {top > 0 && (
            <tr aria-hidden='true'>
              <td colSpan={columnCount} style={{ height: top, padding: 0, border: 0 }} />
            </tr>
          )}
          {items.map((item) => {
            const entry = entries[item.index];
            const row = entry.row;
            if (entry.history) return (
              <TableRow key={entry.key} ref={virtualizer.measureElement} data-index={item.index} data-scroll-index={entry.index}>
                <TableCell colSpan={columnCount} className='p-0'>
                  <RequestContinuationHistory
                    request={row.original} parentTable={table} widths={REQUEST_COLUMN_WIDTHS}
                    position={getPosition(row.id)}
                    onPageChange={(cursors) => {
                      positions.set(row.id, { cursors, scrollTop: 0 });
                      updateHistoryPage((value) => value + 1);
                    }}
                    onBodyClick={onHistoryBodyClick} onViewDetail={onViewDetail}
                  />
                </TableCell>
              </TableRow>
            );
            return (
              <TableRow
                key={row.id}
                ref={virtualizer.measureElement}
                data-index={item.index}
                data-scroll-index={entry.index}
                data-request-id={row.id}
                className='group/row'
              >
                <RequestRow row={row} columns={table.options.columns} columnState={columnState} entering={animate && item.index === 0} />
              </TableRow>
            );
          })}
          {bottom > 0 && (
            <tr aria-hidden='true'>
              <td colSpan={columnCount} style={{ height: bottom, padding: 0, border: 0 }} />
            </tr>
          )}
        </>
      )}
    </TableBody>
  );
}
