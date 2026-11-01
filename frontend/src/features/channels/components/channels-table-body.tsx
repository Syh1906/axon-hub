import { memo, useCallback, useEffect, useLayoutEffect, useRef, type RefObject } from 'react';
import { flexRender, type Row, type Table } from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useTranslation } from 'react-i18next';
import type { IncrementalScrollOptions } from '@/hooks/use-incremental-scroll';
import { TableBody, TableCell, TableRow } from '@/components/ui/table';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import type { Channel } from '../data/schema';
import { ChannelExpandedRow } from './channel-expanded-row';

const ChannelRows = memo(function ChannelRows({
  row,
  columnCount,
  getApiFormatLabel,
}: {
  row: Row<Channel>;
  columnCount: number;
  getApiFormatLabel: (format?: string) => string;
  stateKey: string;
  columns: Table<Channel>['options']['columns'];
}) {
  return (
    <>
      <TableRow
        data-state={row.getIsSelected() && 'selected'}
        className='group/row table-row-stable h-[88px] border-0 !bg-[var(--table-background)]'
      >
        {row.getVisibleCells().map((cell) => (
          <TableCell key={cell.id} className={`${cell.column.columnDef.meta?.className ?? ''} border-0 bg-inherit px-2 py-3`}>
            <div className='flex h-16 max-h-16 items-center justify-center overflow-hidden'>
              {flexRender(cell.column.columnDef.cell, cell.getContext())}
            </div>
          </TableCell>
        ))}
      </TableRow>
      {row.getIsExpanded() && (
        <TableRow className='border-0'>
          <TableCell colSpan={columnCount} className='border-0 p-0'>
            <ChannelExpandedRow channel={row.original} columnsLength={columnCount} getApiFormatLabel={getApiFormatLabel} />
          </TableCell>
        </TableRow>
      )}
    </>
  );
});

export function ChannelsTableBody({
  table,
  containerRef,
  loading,
  pageSize,
  getApiFormatLabel,
  scroll,
}: {
  table: Table<Channel>;
  containerRef: RefObject<HTMLDivElement | null>;
  loading?: boolean;
  pageSize: number;
  getApiFormatLabel: (format?: string) => string;
  scroll: IncrementalScrollOptions;
}) {
  const { t } = useTranslation();
  const rows = table.getRowModel().rows;
  const getItemKey = useCallback((index: number) => rows[index].id, [rows]);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => containerRef.current,
    getItemKey,
    estimateSize: () => 88,
    overscan: 6,
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
    const starts = new Map(rows.map((row, index) => [row.id, virtualizer.measurementsCache[index]?.start ?? 40]));
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
  const lastIndex = items.at(-1)?.index ?? -1;
  useEffect(() => {
    // A cache window change can leave one render with the previous virtual range.
    // Read the actual viewport before starting another page request.
    const container = containerRef.current;
    const nearBottom = !!container && container.scrollHeight - container.scrollTop - container.clientHeight < 400;
    if (rows.length > 0 && lastIndex >= rows.length - 3 && nearBottom && scroll.hasMore && !scroll.fetching && !scroll.error)
      scroll.onLoadMore();
  }, [lastIndex, rows.length, scroll.hasMore, scroll.fetching, scroll.error, scroll.onLoadMore]);
  const firstIndex = items[0]?.index ?? -1;
  useEffect(() => {
    if (
      rows.length > 0 &&
      firstIndex >= 0 &&
      firstIndex < 3 &&
      (containerRef.current?.scrollTop ?? Infinity) < 180 &&
      scroll.hasPrevious &&
      !scroll.fetching &&
      !scroll.error
    ) {
      scroll.onLoadPrevious?.();
    }
  }, [firstIndex, rows.length, scroll.hasPrevious, scroll.fetching, scroll.error, scroll.onLoadPrevious]);
  const columnCount = table.getVisibleLeafColumns().length;
  const top = Math.max(0, (items[0]?.start ?? 40) - 40);
  const bottom = Math.max(0, virtualizer.getTotalSize() - ((items.at(-1)?.end ?? 40) - 40));
  if (loading)
    return (
      <TableBody>
        <TableSkeleton rows={Math.min(pageSize, 8)} columns={columnCount} />
      </TableBody>
    );
  if (!rows.length)
    return (
      <TableBody>
        <TableRow>
          <TableCell colSpan={columnCount} className='h-24 text-center'>
            {t('common.noData')}
          </TableCell>
        </TableRow>
      </TableBody>
    );
  return (
    <>
      {top > 0 && (
        <tbody aria-hidden='true'>
          <tr>
            <td colSpan={columnCount} style={{ height: top, padding: 0, border: 0 }} />
          </tr>
        </tbody>
      )}
      {items.map((item) => {
        const row = rows[item.index];
        return (
          <tbody
            key={row.id}
            ref={virtualizer.measureElement}
            data-index={item.index}
            data-scroll-index={item.index}
            className='!bg-[var(--table-background)]'
          >
            <ChannelRows
              row={row}
              columns={table.options.columns}
              columnCount={columnCount}
              getApiFormatLabel={getApiFormatLabel}
              stateKey={JSON.stringify([row.getIsExpanded(), row.getIsSelected(), table.getState().columnVisibility])}
            />
          </tbody>
        );
      })}
      {bottom > 0 && (
        <tbody aria-hidden='true'>
          <tr>
            <td colSpan={columnCount} style={{ height: bottom, padding: 0, border: 0 }} />
          </tr>
        </tbody>
      )}
    </>
  );
}
