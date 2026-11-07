import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import { ColumnDef, ColumnFiltersState, ExpandedState, RowData, RowSelectionState, SortingState, flexRender, getCoreRowModel, getExpandedRowModel, getFilteredRowModel, getSortedRowModel, useReactTable } from '@tanstack/react-table';
import { IconArchive, IconBan, IconCheck, IconFlask, IconTag, IconTrash, IconTemplate, IconX, IconEraser, IconShieldOff } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useIncrementalScroll, type IncrementalScrollOptions } from '@/hooks/use-incremental-scroll';
import { Button } from '@/components/ui/button';
import { Table, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { IncrementalListFooter } from '@/components/incremental-list-footer';
import { useChannels } from '../context/channels-context';
import type { ChannelListColumnVisibility } from '../data/channels';
import { Channel } from '../data/schema';
import type { ChannelTypeGroup } from '../utils/group-channel-types';
import { ChannelsDefaultSort } from './channels-default-sort';
import { ChannelsTableBody } from './channels-table-body';
import { DataTableToolbar } from './data-table-toolbar';

declare module '@tanstack/react-table' {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  interface ColumnMeta<TData extends RowData, TValue> {
    className: string;
  }
}

interface DataTableProps {
  columns: ColumnDef<Channel>[];
  loading?: boolean;
  data: Channel[];
  pageSize: number;
  totalCount?: number;
  nameFilter: string;
  typeFilter: string[];
  statusFilter: string[];
  tagFilter: string[];
  modelFilter: string;
  selectedTypeTab?: string;
  providerGroups: ChannelTypeGroup[];
  onApplyProvider: (provider: string) => void;
  showErrorOnly?: boolean;
  showHealthGateAbnormal: boolean;
  onHealthGateAbnormalChange: (checked: boolean) => void;
  onExitErrorOnlyMode?: () => void;
  sorting: SortingState;
  onSortingChange: (updater: SortingState | ((prev: SortingState) => SortingState)) => void;
  scroll: IncrementalScrollOptions;
  selectionScopeKey: string;
  onNameFilterChange: (filter: string) => void;
  onTypeFilterChange: (filters: string[]) => void;
  onStatusFilterChange: (filters: string[]) => void;
  onTagFilterChange: (filter: string[]) => void;
  onModelFilterChange: (filter: string) => void;
  columnVisibility: ChannelListColumnVisibility;
  onColumnVisibilityChange: React.Dispatch<React.SetStateAction<ChannelListColumnVisibility>>;
  canWrite?: boolean;
}

export function ChannelsTable({
  columns,
  loading,
  data,
  pageSize,
  totalCount,
  nameFilter,
  typeFilter,
  statusFilter,
  tagFilter,
  modelFilter,
  selectedTypeTab = 'all',
  providerGroups,
  onApplyProvider,
  showErrorOnly,
  showHealthGateAbnormal,
  onHealthGateAbnormalChange,
  sorting,
  onSortingChange,
  onExitErrorOnlyMode,
  scroll,
  selectionScopeKey,
  onNameFilterChange,
  onTypeFilterChange,
  onStatusFilterChange,
  onTagFilterChange,
  onModelFilterChange,
  columnVisibility,
  onColumnVisibilityChange,
  canWrite = true,
}: DataTableProps) {
  const { t, i18n } = useTranslation();
  const loadColumnWidths = useMemo(() => {
    let current = 4;
    let limit = 4;
    for (const channel of data) {
      for (const value of [channel.liveUsage?.inFlight, channel.liveUsage?.rpm]) {
        current = Math.max(current, (value ?? 0).toLocaleString(i18n.language).length);
      }
      for (const value of [channel.settings?.rateLimit?.maxConcurrent, channel.settings?.rateLimit?.rpm]) {
        limit = Math.max(limit, (value ?? 0).toLocaleString(i18n.language).length);
      }
    }
    return {
      '--channel-load-value-width': `${current}ch`,
      '--channel-load-limit-width': `${limit + 2}ch`,
    } as React.CSSProperties;
  }, [data, i18n.language]);
  const scrolling = useIncrementalScroll(scroll);
  const { setSelectedChannels, setResetRowSelection, setOpen } = useChannels();
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
  const selectedObjects = useRef(new Map<string, Channel>());
  const [expanded, setExpanded] = useState<ExpandedState>({});
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);

  // Sync server state to local column filters using useMemo instead of useEffect
  useEffect(() => {
    const newColumnFilters: ColumnFiltersState = [];

    if (nameFilter) {
      newColumnFilters.push({ id: 'name', value: nameFilter });
    }
    if (typeFilter.length > 0) {
      newColumnFilters.push({ id: 'provider', value: typeFilter });
    }
    if (statusFilter.length > 0) {
      newColumnFilters.push({ id: 'status', value: statusFilter });
    }
    if (tagFilter.length) {
      newColumnFilters.push({ id: 'tags', value: tagFilter });
    }
    if (modelFilter) {
      newColumnFilters.push({ id: 'model', value: modelFilter });
    }

    setColumnFilters(newColumnFilters);
  }, [nameFilter, typeFilter, statusFilter, tagFilter, modelFilter]);

  // Save column visibility to localStorage whenever it changes
  useEffect(() => {
    localStorage.setItem('channels-table-column-visibility', JSON.stringify(columnVisibility));
  }, [columnVisibility]);

  // Handle column filter changes and sync with server
  const handleColumnFiltersChange = useCallback(
    (updater: ColumnFiltersState | ((prev: ColumnFiltersState) => ColumnFiltersState)) => {
      const newFilters = typeof updater === 'function' ? updater(columnFilters) : updater;
      setColumnFilters(newFilters);

      // Extract filter values
      const nameFilterValue = newFilters.find((filter) => filter.id === 'name')?.value as string;
      const typeFilterValue = newFilters.find((filter) => filter.id === 'provider')?.value as string[];
      const statusFilterValue = newFilters.find((filter) => filter.id === 'status')?.value as string[];
      const tagFilterValue = newFilters.find((filter) => filter.id === 'tags')?.value as string[];
      const modelFilterValue = newFilters.find((filter) => filter.id === 'model')?.value as string;

      // Update server filters only if changed
      const newNameFilter = nameFilterValue || '';
      const newTypeFilter = Array.isArray(typeFilterValue) ? typeFilterValue : [];
      const newStatusFilter = Array.isArray(statusFilterValue) ? statusFilterValue : [];
      const newTagFilter = Array.isArray(tagFilterValue) ? tagFilterValue : [];
      const newModelFilter = modelFilterValue || '';

      if (newNameFilter !== nameFilter) {
        onNameFilterChange(newNameFilter);
      }

      if (JSON.stringify(newTypeFilter.sort()) !== JSON.stringify(typeFilter.sort())) {
        onTypeFilterChange(newTypeFilter);
      }

      if (JSON.stringify(newStatusFilter.sort()) !== JSON.stringify(statusFilter.sort())) {
        onStatusFilterChange(newStatusFilter);
      }

      if (JSON.stringify(newTagFilter) !== JSON.stringify(tagFilter)) {
        onTagFilterChange(newTagFilter);
      }

      if (newModelFilter !== modelFilter) {
        onModelFilterChange(newModelFilter);
      }
    },
    [
      columnFilters,
      nameFilter,
      typeFilter,
      statusFilter,
      tagFilter,
      modelFilter,
      onNameFilterChange,
      onTypeFilterChange,
      onStatusFilterChange,
      onTagFilterChange,
      onModelFilterChange,
    ]
  );

  const table = useReactTable({
    data,
    columns,
    state: {
      sorting,
      columnVisibility,
      rowSelection,
      columnFilters,
      expanded,
    },
    enableRowSelection: true,
    getRowId: (row) => row.id,
    onRowSelectionChange: setRowSelection,
    onExpandedChange: setExpanded,
    onSortingChange,
    onColumnFiltersChange: handleColumnFiltersChange,
    onColumnVisibilityChange,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getExpandedRowModel: getExpandedRowModel(),
    // Enable server-side pagination and filtering
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true, // Enable manual filtering for server-side filtering
  });

  // Only explicit selections survive page eviction. Ordinary rows remain bounded.
  const selectedRows = useMemo(() => {
    const visible = new Map(data.map((channel) => [channel.id, channel]));
    return Object.entries(rowSelection).flatMap(([id, selected]) => {
      const channel = visible.get(id) ?? selectedObjects.current.get(id);
      return selected && channel ? [channel] : [];
    });
  }, [data, rowSelection]);

  const getApiFormatLabel = useCallback(
    (apiFormat?: string) => {
      if (!apiFormat) return '-';

      const key = `channels.dialogs.fields.apiFormat.formats.${apiFormat}`;
      const label = t(key);
      return label === key ? apiFormat : label;
    },
    [t]
  );

  const selectedCount = selectedRows.length;
  const isFiltered = useMemo(() => columnFilters.length > 0, [columnFilters.length]);

  useEffect(() => {
    const resetFn = () => {
      setRowSelection({});
    };
    setResetRowSelection(resetFn);
  }, [setResetRowSelection]);

  useEffect(() => {
    selectedObjects.current = new Map(selectedRows.map((channel) => [channel.id, channel]));
    setSelectedChannels(selectedRows);
  }, [selectedRows, setSelectedChannels]);

  // A new filter/order starts a different selection scope; page changes do not.
  useEffect(() => {
    setRowSelection({});
    setExpanded({});
  }, [selectionScopeKey]);

  return (
    <div className='flex min-w-0 flex-1 flex-col overflow-hidden'>
      <DataTableToolbar
        table={table}
        isFiltered={isFiltered}
        selectedCount={selectedCount}
        selectedTypeTab={selectedTypeTab}
        showErrorOnly={showErrorOnly}
        showHealthGateAbnormal={showHealthGateAbnormal}
        onHealthGateAbnormalChange={onHealthGateAbnormalChange}
        onExitErrorOnlyMode={onExitErrorOnlyMode}
      />
      <ChannelsDefaultSort
        channelIds={loading ? [] : table.getRowModel().rows.map((row) => row.original.id)}
        sorting={sorting}
        onApply={onSortingChange}
        onApplyTags={onTagFilterChange}
        providerGroups={providerGroups}
        onApplyProvider={onApplyProvider}
      />
      <p className='text-muted-foreground mt-2 text-xs'>{t('channels.routing.clickHint')}</p>
      <div
        ref={scrolling.containerRef}
        onScroll={scrolling.onScroll}
        data-testid='channels-scroll'
        style={{ overflowAnchor: 'none' }}
        className='shadow-soft relative mt-2 min-h-0 flex-1 overflow-auto rounded-2xl border border-[var(--table-border)]'
      >
        <div className='min-w-[1100px]'>
          <Table
            data-testid='channels-table'
            style={loadColumnWidths}
            className='border-separate border-spacing-0 rounded-2xl bg-[var(--table-background)]'
          >
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
            <ChannelsTableBody
              table={table}
              containerRef={scrolling.containerRef}
              loading={loading}
              pageSize={pageSize}
              getApiFormatLabel={getApiFormatLabel}
              scroll={scroll}
            />
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
      {/* Floating Bulk Actions Bar */}
      {selectedCount > 0 && canWrite && (
        <div className='fixed bottom-6 left-1/2 z-50 -translate-x-1/2'>
          <div className='bg-background flex items-center gap-2 rounded-lg border px-4 py-2 shadow-lg'>
            <Button variant='ghost' size='icon' className='h-8 w-8' onClick={() => setRowSelection({})}>
              <IconX className='h-4 w-4' />
            </Button>
            <div className='flex items-center gap-1.5 px-2'>
              <span className='bg-primary text-primary-foreground flex h-6 min-w-6 items-center justify-center rounded px-1.5 text-xs font-medium'>
                {selectedCount}
              </span>
              <span className='text-muted-foreground text-sm'>{t('common.selected')}</span>
            </div>
            <div className='bg-border mx-2 h-6 w-px' />
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-violet-600 hover:bg-violet-100 hover:text-violet-700'
              onClick={() => setOpen('bulkManageTags')}
              title={t('channels.actions.bulkManageTags')}
              aria-label={t('channels.actions.bulkManageTags')}
            >
              <IconTag className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-blue-600 hover:bg-blue-100 hover:text-blue-700'
              onClick={() => setOpen('bulkApplyTemplate')}
              title={t('channels.templates.bulk.applyButton')}
            >
              <IconTemplate className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-violet-600 hover:bg-violet-100 hover:text-violet-700'
              onClick={() => setOpen('bulkAutoDisable')}
              title={t('channels.bulkAutoDisable.button')}
            >
              <IconShieldOff className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-red-600 hover:bg-red-100 hover:text-red-700'
              onClick={() => setOpen('bulkClearTemplate')}
              title={t('channels.templates.bulkClear.button')}
            >
              <IconEraser className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-sky-600 hover:bg-sky-100 hover:text-sky-700'
              onClick={() => setOpen('bulkTest')}
              title={t('channels.actions.bulkTest')}
            >
              <IconFlask className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-green-600 hover:bg-green-100 hover:text-green-700'
              onClick={() => setOpen('bulkEnable')}
              title={t('common.buttons.enable')}
            >
              <IconCheck className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-amber-600 hover:bg-amber-100 hover:text-amber-700'
              onClick={() => setOpen('bulkDisable')}
              title={t('common.buttons.disable')}
            >
              <IconBan className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='h-8 w-8 text-orange-600 hover:bg-orange-100 hover:text-orange-700'
              onClick={() => setOpen('bulkArchive')}
              title={t('common.buttons.archive')}
            >
              <IconArchive className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon'
              className='text-destructive h-8 w-8 hover:bg-red-100 hover:text-red-700'
              onClick={() => setOpen('bulkDelete')}
              title={t('common.buttons.delete')}
            >
              <IconTrash className='h-4 w-4' />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
