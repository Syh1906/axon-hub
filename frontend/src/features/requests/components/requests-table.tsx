import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import {
  ColumnFiltersState,
  ColumnOrderState,
  RowData,
  SortingState,
  VisibilityState,
  Updater,
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getSortedRowModel,
  useReactTable,
} from '@tanstack/react-table';
import { motion, AnimatePresence } from 'framer-motion';
import { useTranslation } from 'react-i18next';
import { readColumnOrder, writeColumnOrder, reconcileColumnOrder, isReorderableColumn } from '@/lib/column-order';
import type { DateTimeRangeValue } from '@/utils/date-range';
import type { AutoRefreshInterval } from '@/hooks/use-auto-refresh-interval';
import { useIncrementalScroll, type IncrementalScrollOptions } from '@/hooks/use-incremental-scroll';
import { useIsMobile, MOBILE_BREAKPOINT } from '@/hooks/use-mobile';
import { useAnimatedList } from '@/hooks/useAnimatedList';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { TableSkeleton } from '@/components/ui/table-skeleton';
import { IncrementalListFooter } from '@/components/incremental-list-footer';
import { Request, RequestConnection } from '../data/schema';
import { DataTableToolbar } from './data-table-toolbar';
import { RequestBodyDrawer } from './request-body-drawer';
import type { RequestDiagnosticTab } from './request-log-cells';
import { DEFAULT_HIDDEN_COLUMN_IDS, DEFAULT_MOBILE_HIDDEN_COLUMN_IDS, useRequestsColumns } from './requests-columns';

const COLUMN_VISIBILITY_STORAGE_KEY = 'requests-table-column-visibility';
const COLUMN_VISIBILITY_STORAGE_VERSION = 7;

const COLUMN_ORDER_STORAGE_KEY = 'requests-table-column-order';
const COLUMN_ORDER_STORAGE_VERSION = 2;

const MotionTableRow = motion.create(TableRow);

declare module '@tanstack/react-table' {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  interface ColumnMeta<TData extends RowData, TValue> {
    className: string;
  }
}

interface RequestsTableProps {
  data: Request[];
  loading?: boolean;
  pageInfo?: RequestConnection['pageInfo'];
  pageSize: number;
  totalCount?: number;
  statusFilter: string[];
  sourceFilter: string[];
  channelFilter: string[];
  channelTagFilter: string[];
  apiKeyGroupFilter: string[];
  apiKeyFilter: string[];
  modelIDFilter: string;
  dateRange?: DateTimeRangeValue;
  queryWhere?: Record<string, any>;
  scroll: IncrementalScrollOptions;
  onFiltersChange: (filters: RequestTableFilters) => void;
  onDateRangeChange: (range: DateTimeRangeValue | undefined) => void;
  onResetFilters: () => void;
  onViewDetail: (requestId: string) => void;
  showRefresh: boolean;
  autoRefreshInterval?: AutoRefreshInterval;
  autoRefreshResumeKey?: number;
  onAutoRefreshIntervalChange?: (interval: AutoRefreshInterval) => void;
}

export interface RequestTableFilters {
  statusFilter: string[];
  sourceFilter: string[];
  channelFilter: string[];
  channelTagFilter: string[];
  apiKeyGroupFilter: string[];
  apiKeyFilter: string[];
  modelIDFilter: string;
}

function getFilterArrayValue(filters: ColumnFiltersState, id: string) {
  const value = filters.find((filter) => filter.id === id)?.value;
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
}

function getFilterStringValue(filters: ColumnFiltersState, id: string) {
  const value = filters.find((filter) => filter.id === id)?.value;
  return typeof value === 'string' ? value : '';
}

export function RequestsTable({
  data,
  loading,
  pageInfo,
  totalCount,
  pageSize,
  statusFilter,
  sourceFilter,
  channelFilter,
  channelTagFilter,
  apiKeyGroupFilter,
  apiKeyFilter,
  modelIDFilter,
  dateRange,
  queryWhere,
  scroll,
  onFiltersChange,
  onDateRangeChange,
  onResetFilters,
  onViewDetail,
  showRefresh,
  autoRefreshInterval = null,
  autoRefreshResumeKey = 0,
  onAutoRefreshIntervalChange,
}: RequestsTableProps) {
  const { t } = useTranslation();
  const scrolling = useIncrementalScroll(scroll);
  const [refreshMode, setRefreshMode] = useState(() => {
    try {
      return localStorage.getItem('requests-refresh-mode') === 'sequential' ? 'sequential' : 'steady';
    } catch {
      return 'steady';
    }
  });
  const animate = refreshMode === 'sequential' && showRefresh && autoRefreshInterval !== null;

  const [drawerInitialTab, setDrawerInitialTab] = useState<RequestDiagnosticTab>('request');
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerInitialRequestId, setDrawerInitialRequestId] = useState<string | null>(null);
  const [drawerInitialIndex, setDrawerInitialIndex] = useState(0);

  const handleBodyClick = useCallback((requestId: string, index: number, tab: RequestDiagnosticTab = 'request') => {
    setDrawerInitialTab(tab);
    setDrawerInitialRequestId(requestId);
    setDrawerInitialIndex(index);
    setDrawerOpen(true);
  }, []);

  const requestsColumns = useRequestsColumns({
    onBodyClick: handleBodyClick,
    onViewDetail,
    onCallerFilter: (kind, value) =>
      onFiltersChange({
        statusFilter,
        sourceFilter,
        channelFilter,
        channelTagFilter,
        modelIDFilter,
        apiKeyFilter: kind === 'key' ? [value] : [],
        apiKeyGroupFilter: kind === 'group' ? [value] : [],
      }),
  });
  const [sorting, setSorting] = useState<SortingState>([]);
  const isMobile = useIsMobile();

  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({});
  const userOverridesRef = useRef<VisibilityState>({});
  const columnVisibilityRef = useRef<VisibilityState>({});
  const [visibilityReady, setVisibilityReady] = useState(false);

  // Hydrate column visibility from localStorage once viewport is known
  useEffect(() => {
    const isMobileInit = window.innerWidth < MOBILE_BREAKPOINT;
    const desktopDefaults: VisibilityState = Object.fromEntries(DEFAULT_HIDDEN_COLUMN_IDS.map((id) => [id, false]));
    const mobileDefaults: VisibilityState = isMobileInit
      ? { ...desktopDefaults, ...Object.fromEntries(DEFAULT_MOBILE_HIDDEN_COLUMN_IDS.map((id) => [id, false])) }
      : desktopDefaults;

    let overrides: VisibilityState = {};
    try {
      const raw = localStorage.getItem(COLUMN_VISIBILITY_STORAGE_KEY);
      if (raw) {
        const parsed: unknown = JSON.parse(raw);
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          if ((parsed as { v?: number }).v === COLUMN_VISIBILITY_STORAGE_VERSION) {
            const stored = (parsed as { overrides?: VisibilityState }).overrides;
            if (stored && typeof stored === 'object') overrides = stored;
          }
        }
      }
    } catch {
      // localStorage unavailable or corrupt — use defaults
    }

    userOverridesRef.current = overrides;
    setColumnVisibility({ ...mobileDefaults, ...overrides });
    setVisibilityReady(true);
  }, []); // Run once on mount

  // Mirror columnVisibility into a ref so the visibility-change handler can read prev without a closure
  useEffect(() => {
    columnVisibilityRef.current = columnVisibility;
  }, [columnVisibility]);

  const [rowSelection, setRowSelection] = useState({});

  // Persist only user-driven overrides (never responsive defaults)
  useEffect(() => {
    if (!visibilityReady) return;
    try {
      localStorage.setItem(
        COLUMN_VISIBILITY_STORAGE_KEY,
        JSON.stringify({ v: COLUMN_VISIBILITY_STORAGE_VERSION, overrides: userOverridesRef.current })
      );
    } catch {
      // localStorage unavailable or quota exceeded — skip persistence
    }
  }, [columnVisibility, visibilityReady]);

  useEffect(() => {
    if (!visibilityReady) return;
    setColumnVisibility((prev) => {
      const hidden = DEFAULT_MOBILE_HIDDEN_COLUMN_IDS.filter((id) => !DEFAULT_HIDDEN_COLUMN_IDS.includes(id));
      const overrides = userOverridesRef.current;
      const next = { ...prev };

      if (isMobile) {
        // Hide responsive defaults, preserve user overrides
        hidden.forEach((id) => {
          if (next[id] === undefined && overrides[id] === undefined) {
            next[id] = false;
          }
        });
      } else {
        // On desktop, remove responsive defaults but keep user overrides
        hidden.forEach((id) => {
          if (overrides[id] === undefined) {
            delete next[id];
          }
        });
      }
      return next;
    });
  }, [isMobile, visibilityReady]);

  const [columnOrder, setColumnOrder] = useState<ColumnOrderState>([]);
  const columnOrderRef = useRef<ColumnOrderState>([]);

  // Hydrate column order from localStorage once table is mounted
  useEffect(() => {
    const allIds = table.getAllLeafColumns().map((c) => c.id);
    const reorderable = allIds.filter((id) => {
      const col = table.getColumn(id);
      return col ? isReorderableColumn(col) : false;
    });
    let stored = readColumnOrder(COLUMN_ORDER_STORAGE_KEY, COLUMN_ORDER_STORAGE_VERSION);
    if (!stored) {
      const previous = readColumnOrder(COLUMN_ORDER_STORAGE_KEY, 1);
      if (previous) {
        stored = [...previous];
        const modelIndex = stored.indexOf('modelID');
        const channelIndex = stored.indexOf('channel');
        if (modelIndex >= 0 && channelIndex > modelIndex) {
          [stored[modelIndex], stored[channelIndex]] = [stored[channelIndex], stored[modelIndex]];
        }
        writeColumnOrder(COLUMN_ORDER_STORAGE_KEY, COLUMN_ORDER_STORAGE_VERSION, stored);
      }
    }
    setColumnOrder(
      reconcileColumnOrder({
        allColumnIds: allIds,
        reorderableColumnIds: reorderable,
        storedOrder: stored,
        pinnedLast: ['details'],
      })
    );
  }, []);

  useEffect(() => {
    columnOrderRef.current = columnOrder;
  }, [columnOrder]);

  const handleColumnOrderChange = useCallback((updater: Updater<ColumnOrderState>) => {
    const next = typeof updater === 'function' ? updater(columnOrderRef.current) : updater;
    setColumnOrder(next);
    writeColumnOrder(COLUMN_ORDER_STORAGE_KEY, COLUMN_ORDER_STORAGE_VERSION, next);
  }, []);

  const animationResetKey = useMemo(
    () => JSON.stringify({ queryWhere: queryWhere ?? null, autoRefreshResumeKey }),
    [queryWhere, autoRefreshResumeKey]
  );
  const displayedData = useAnimatedList(data, animate, pageSize, animationResetKey);

  const columnFilters = useMemo<ColumnFiltersState>(() => {
    const filters: ColumnFiltersState = [];
    if (statusFilter.length > 0) {
      filters.push({ id: 'status', value: statusFilter });
    }
    if (sourceFilter.length > 0) {
      filters.push({ id: 'source', value: sourceFilter });
    }
    if (channelFilter.length > 0) {
      filters.push({ id: 'channel', value: channelFilter });
    }
    if (channelTagFilter.length > 0) filters.push({ id: 'channelTag', value: channelTagFilter });
    if (apiKeyGroupFilter.length > 0) filters.push({ id: 'apiKeyGroup', value: apiKeyGroupFilter });
    if (apiKeyFilter.length > 0) {
      filters.push({ id: 'caller', value: apiKeyFilter });
    }
    if (modelIDFilter) {
      filters.push({ id: 'modelID', value: modelIDFilter });
    }
    return filters;
  }, [statusFilter, sourceFilter, channelFilter, channelTagFilter, apiKeyGroupFilter, apiKeyFilter, modelIDFilter]);

  const handleColumnFiltersChange = useCallback(
    (updater: any) => {
      const newFilters = typeof updater === 'function' ? updater(columnFilters) : updater;

      onFiltersChange({
        statusFilter: getFilterArrayValue(newFilters, 'status'),
        sourceFilter: getFilterArrayValue(newFilters, 'source'),
        channelFilter: getFilterArrayValue(newFilters, 'channel'),
        channelTagFilter: getFilterArrayValue(newFilters, 'channelTag'),
        apiKeyGroupFilter: getFilterArrayValue(newFilters, 'apiKeyGroup'),
        apiKeyFilter: getFilterArrayValue(newFilters, 'caller'),
        modelIDFilter: getFilterStringValue(newFilters, 'modelID'),
      });
    },
    [columnFilters, onFiltersChange]
  );

  const handleColumnVisibilityChange = useCallback((updater: Updater<VisibilityState>) => {
    const prev = columnVisibilityRef.current;
    const next = typeof updater === 'function' ? updater(prev) : updater;
    // Record only user-driven changes as explicit overrides
    Object.entries(next).forEach(([id, visible]) => {
      if (prev[id] !== visible) {
        userOverridesRef.current = { ...userOverridesRef.current, [id]: visible };
      }
    });
    setColumnVisibility(next);
  }, []);

  const table = useReactTable({
    data: displayedData,
    getRowId: (row) => row.id,
    columns: requestsColumns,
    state: {
      sorting,
      columnVisibility,
      columnOrder,
      rowSelection,
      columnFilters,
    },
    enableRowSelection: true,
    onRowSelectionChange: setRowSelection,
    onSortingChange: setSorting,
    onColumnFiltersChange: handleColumnFiltersChange,
    onColumnVisibilityChange: handleColumnVisibilityChange,
    onColumnOrderChange: handleColumnOrderChange,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    // Disable client-side pagination since we're using server-side
    manualPagination: true,
    manualFiltering: true, // Enable manual filtering for server-side filtering
  });

  return (
    <div className='flex flex-1 flex-col overflow-hidden'>
      <DataTableToolbar
        table={table}
        dateRange={dateRange}
        onDateRangeChange={onDateRangeChange}
        onResetFilters={onResetFilters}
        onRefresh={scrolling.backToTop}
        showRefresh
        refreshControls={
          <>
            {!showRefresh && autoRefreshInterval !== null && (
              <span className='text-muted-foreground text-xs'>{t('requests.refresh.paused')}</span>
            )}
            <Select
              value={refreshMode}
              onValueChange={(value) => {
                setRefreshMode(value);
                try {
                  localStorage.setItem('requests-refresh-mode', value);
                } catch {
                  // Keep the in-memory choice when browser storage is unavailable.
                }
              }}
            >
              <SelectTrigger className='h-8 w-28' aria-label={t('requests.refresh.mode')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value='steady'>{t('requests.refresh.steady')}</SelectItem>
                  <SelectItem value='sequential'>{t('requests.refresh.sequential')}</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </>
        }
        autoRefreshInterval={autoRefreshInterval}
        onAutoRefreshIntervalChange={onAutoRefreshIntervalChange}
        enableColumnOrdering
        getColumnLabel={(id) => t(`requests.columns.${id}`, { defaultValue: id })}
      />
      <div
        ref={scrolling.containerRef}
        onScroll={scrolling.onScroll}
        data-testid='requests-scroll'
        className='shadow-soft relative mt-2 flex-1 overflow-auto rounded-2xl border border-[var(--table-border)] sm:mt-4'
      >
        <div className='min-w-max'>
          <Table data-testid='requests-table' className='border-separate border-spacing-0 rounded-2xl bg-[var(--table-background)]'>
            <TableHeader className='sticky top-0 z-20 bg-[var(--table-header)] shadow-sm'>
              {table.getHeaderGroups().map((headerGroup) => (
                <TableRow key={headerGroup.id} className='group/row border-0'>
                  {headerGroup.headers.map((header) => {
                    return (
                      <TableHead
                        key={header.id}
                        colSpan={header.colSpan}
                        className={`${header.column.columnDef.meta?.className ?? ''} text-muted-foreground border-0 text-xs font-semibold tracking-wider uppercase`}
                      >
                        {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                      </TableHead>
                    );
                  })}
                </TableRow>
              ))}
            </TableHeader>
            <TableBody className='space-y-1 !bg-[var(--table-background)] p-2'>
              {loading ? (
                <TableSkeleton rows={pageSize} columns={table.getVisibleLeafColumns().length} />
              ) : table.getRowModel().rows?.length ? (
                <AnimatePresence initial={false} mode='popLayout'>
                  {table.getRowModel().rows.map((row) => (
                    <MotionTableRow
                      key={row.id}
                      data-scroll-index={row.index}
                      data-state={row.getIsSelected() && 'selected'}
                      initial={animate ? { opacity: 0, y: -20, height: 0 } : false}
                      animate={{ opacity: 1, y: 0, height: 'auto' }}
                      exit={animate ? { opacity: 0, height: 0 } : undefined}
                      transition={{
                        type: 'spring',
                        stiffness: 500,
                        damping: 30,
                        mass: 1,
                        opacity: { duration: 0.2 },
                      }}
                      layout={animate}
                      className='group/row hover:bg-muted/50 data-[state=selected]:bg-muted'
                    >
                      {row.getVisibleCells().map((cell) => (
                        <TableCell
                          key={cell.id}
                          className={`${cell.column.columnDef.meta?.className ?? ''} border-b border-[var(--table-border)] py-3 group-last/row:border-0`}
                        >
                          {flexRender(cell.column.columnDef.cell, cell.getContext())}
                        </TableCell>
                      ))}
                    </MotionTableRow>
                  ))}
                </AnimatePresence>
              ) : (
                <TableRow className='!bg-[var(--table-background)]'>
                  <TableCell colSpan={requestsColumns.length} className='h-24 !bg-[var(--table-background)] text-center'>
                    {t('common.noData')}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div ref={scrolling.sentinelRef} className='h-px' />
      </div>
      <IncrementalListFooter
        loaded={data.length}
        total={totalCount}
        loading={scroll.loadingMore}
        hasMore={scroll.hasMore}
        error={scroll.error}
        atTop={scrolling.atTop}
        onLoadMore={scroll.onLoadMore}
        onBackToTop={scrolling.backToTop}
      />

      <RequestBodyDrawer
        initialTab={drawerInitialTab}
        open={drawerOpen}
        onOpenChange={setDrawerOpen}
        initialRequestId={drawerInitialRequestId}
        initialIndex={drawerInitialIndex}
        initialRequests={data}
        pageInfo={pageInfo}
        queryWhere={queryWhere}
        onViewDetail={onViewDetail}
      />
    </div>
  );
}
