import { useLayoutEffect, useMemo, useRef } from 'react';
import { flexRender, getCoreRowModel, useReactTable, type Table as ReactTable } from '@tanstack/react-table';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { TableBody, TableCell, TableRow } from '@/components/ui/table';
import { useRequestContinuations } from '../data/requests';
import type { Request } from '../data/schema';
import type { RequestDiagnosticTab } from './request-log-cells';
import { useRequestsColumns } from './requests-columns';

export type ContinuationHistoryPosition = { cursors: string[]; scrollTop: number };

export function RequestContinuationHistory({
  request,
  parentTable,
  widths,
  position,
  onPageChange,
  onBodyClick,
  onViewDetail,
}: {
  request: Request;
  parentTable: ReactTable<Request>;
  widths: Record<string, number>;
  position: ContinuationHistoryPosition;
  onPageChange: (cursors: string[]) => void;
  onBodyClick: (requests: Request[], id: string, index: number, tab: RequestDiagnosticTab) => void;
  onViewDetail: (id: string) => void;
}) {
  const { t } = useTranslation();
  const after = position.cursors.at(-1) ?? null;
  const query = useRequestContinuations(request.id, request.continuationCount ?? 0, after);
  const requests = useMemo(() => query.data?.edges.map((edge) => edge.node) ?? [], [query.data]);
  const columns = useRequestsColumns({
    continuationHistory: true,
    onBodyClick: (id, index, tab = 'request') => onBodyClick(requests, id, index, tab),
    onViewDetail,
  });
  const { columnVisibility, columnOrder } = parentTable.getState();
  const table = useReactTable({
    data: requests,
    columns,
    state: { columnVisibility, columnOrder },
    getRowId: (row) => row.id,
    getCoreRowModel: getCoreRowModel(),
  });
  const container = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (container.current && query.data) container.current.scrollTop = position.scrollTop;
  }, [after, query.data, position]);

  return (
    <div data-testid='request-continuation-history' className='bg-muted/30 border-b border-[var(--table-border)]'>
      <p className='text-muted-foreground px-4 py-2 text-xs'>{t('requests.continuation.canceled')}</p>
      {query.isPending ? (
        <p className='text-muted-foreground px-4 py-3 text-xs'>{t('common.loading')}</p>
      ) : query.isError ? (
        <div role='alert' className='flex items-center gap-2 px-4 py-2 text-xs'>
          <span>{t('requests.continuation.loadFailed')}</span>
          <Button variant='ghost' size='sm' onClick={() => query.refetch()}>{t('requests.diagnostics.retry')}</Button>
        </div>
      ) : (
        <>
          <div ref={container} className='max-h-80 overflow-y-auto' onScroll={(event) => { position.scrollTop = event.currentTarget.scrollTop; }}>
            <table className='w-full table-fixed border-separate border-spacing-0 text-sm'>
              <colgroup>
                {table.getVisibleLeafColumns().map((column) => <col key={column.id} style={{ width: widths[column.id] ?? 150 }} />)}
              </colgroup>
              <TableBody>
                {table.getRowModel().rows.map((row) => (
                  <TableRow key={row.id} data-history-request-id={row.id} className='group/row'>
                    {row.getVisibleCells().map((cell) => (
                      <TableCell key={cell.id} className={`${cell.column.columnDef.meta?.className ?? ''} border-b border-[var(--table-border)] py-3`}>
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </table>
          </div>
          {(position.cursors.length > 0 || query.data.pageInfo.hasNextPage) && (
            <div className='flex items-center gap-2 px-4 py-2 text-xs'>
              <Button variant='ghost' size='sm' disabled={!position.cursors.length} onClick={() => onPageChange(position.cursors.slice(0, -1))}>
                {t('requests.continuation.previous')}
              </Button>
              <span className='text-muted-foreground'>{t('requests.continuation.page', { page: position.cursors.length + 1 })}</span>
              <Button variant='ghost' size='sm' disabled={!query.data.pageInfo.hasNextPage} onClick={() => {
                const cursor = query.data.pageInfo.endCursor;
                if (cursor) onPageChange([...position.cursors, cursor]);
              }}>
                {t('requests.continuation.next')}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
