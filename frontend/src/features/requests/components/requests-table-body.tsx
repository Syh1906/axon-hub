import { memo, useCallback, useEffect, type RefObject } from 'react';
import { flexRender, type Row, type Table } from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import type { IncrementalScrollOptions } from '@/hooks/use-incremental-scroll';
import { TableBody, TableCell, TableRow } from '@/components/ui/table';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import type { Request } from '../data/schema';

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
}: {
  table: Table<Request>;
  containerRef: RefObject<HTMLDivElement | null>;
  scroll: IncrementalScrollOptions;
  loading?: boolean;
  animate: boolean;
}) {
  const { t } = useTranslation();
  const rows = table.getRowModel().rows;
  const getItemKey = useCallback((index: number) => rows[index].id, [rows]);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => containerRef.current,
    getItemKey,
    estimateSize: () => 80,
    overscan: 10,
    scrollMargin: 40,
    useFlushSync: false,
  });
  const items = virtualizer.getVirtualItems();
  const lastIndex = items.at(-1)?.index ?? -1;
  useEffect(() => {
    if (rows.length > 0 && lastIndex >= rows.length - 5 && scroll.hasMore && !scroll.fetching && !scroll.error) scroll.onLoadMore();
  }, [lastIndex, rows.length, scroll.hasMore, scroll.fetching, scroll.error, scroll.onLoadMore]);

  const columnCount = table.getVisibleLeafColumns().length;
  const state = table.getState();
  const columnState = JSON.stringify([state.columnVisibility, state.columnOrder]);
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
            const row = rows[item.index];
            return (
              <TableRow
                key={row.id}
                ref={virtualizer.measureElement}
                data-index={item.index}
                data-scroll-index={item.index}
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
