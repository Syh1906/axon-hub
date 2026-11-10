import { useState, useMemo, useCallback, useEffect, useRef, lazy, Suspense } from 'react';
import { SortingState } from '@tanstack/react-table';
import { useTranslation } from 'react-i18next';
import { useDebounce } from '@/hooks/use-debounce';
import { usePaginationSearch } from '@/hooks/use-pagination-search';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';

import { Main } from '@/components/layout/main';
import { PageHeader } from '@/components/layout/page-header';
import { createColumns } from './components/channels-columns';
import { ChannelsErrorBanner } from './components/channels-error-banner';
import { ChannelsPrimaryButtons } from './components/channels-primary-buttons';
import { ChannelsTable } from './components/channels-table';
import { ChannelsTypeTabs } from './components/channels-type-tabs';
import ChannelsProvider, { useChannels } from './context/channels-context';
import { useInfiniteChannels } from './data/channel-list';
import { ChannelControlsProvider } from './context/channel-controls-context';
import { DEFAULT_CHANNEL_COLUMN_VISIBILITY, useAllChannelSummarys, parseChannelColumnVisibility, useChannelTypes, useErrorChannelsCount, type ChannelListColumnVisibility } from './data/channels';
import { CHANNEL_TYPE_TO_PROVIDER } from './data/config_channels';
import { readDefaultChannelSort } from './utils/channel-sort';
import { readDefaultProvider, resolveChannelProviderSelection } from './utils/default-provider';
import { resolveChannelTagSelection } from './utils/default-tags';
import { groupChannelTypesByProvider, IMAGE_PRIMARY_API_FORMAT } from './utils/group-channel-types';
import { useQuotaRoutingSettings } from '@/features/system/data/system';
import { useQueryClient } from '@tanstack/react-query';
import { useAuthStore } from '@/stores/authStore';

const ChannelsDialogs = lazy(() => import('./components/channels-dialogs').then((m) => ({ default: m.ChannelsDialogs })));

const EMPTY_CHANNEL_PAGE = {
  edges: [],
  totalCount: 0,
  pageInfo: { hasNextPage: false, hasPreviousPage: false, startCursor: null, endCursor: null },
};

function ChannelsContent() {
  const { t } = useTranslation();
  const { channelPermissions, hasSystemScope } = usePermissions();
  const { data: quotaRoutingSettings } = useQuotaRoutingSettings();
  const queryClient = useQueryClient();
  const authUserId = useAuthStore((state) => state.auth.user?.id);
  const canReadSystemSettings = hasSystemScope('read_settings');
  const quotaRoutingCacheKey = `${authUserId ?? 'signed-out'}:${canReadSystemSettings}`;
  const previousQuotaRoutingCacheKey = useRef<string | undefined>(undefined);
  const currentQuotaRoutingSettings = previousQuotaRoutingCacheKey.current === quotaRoutingCacheKey ? quotaRoutingSettings : undefined;
  useEffect(() => {
    if (previousQuotaRoutingCacheKey.current == null) {
      previousQuotaRoutingCacheKey.current = quotaRoutingCacheKey;
      return;
    }
    if (previousQuotaRoutingCacheKey.current === quotaRoutingCacheKey) return;
    previousQuotaRoutingCacheKey.current = quotaRoutingCacheKey;
    void queryClient.removeQueries({ queryKey: ['quotaRoutingSettings'] });
  }, [queryClient, quotaRoutingCacheKey]);
  const { showTypeTabs, setShowTypeTabs, selectedTag: tagFilter, setSelectedTag: setTagFilter } = useChannels();
  const { pageSize, resetCursor } = usePaginationSearch({
    defaultPageSize: 20,
    pageSizeStorageKey: 'channels-table-page-size',
  });
  const tagChannels = useAllChannelSummarys(undefined, { enabled: tagFilter.length > 1, includeArchived: true });
  const multiTagSelection = useMemo(
    () =>
      resolveChannelTagSelection(
        tagFilter,
        tagChannels.data?.edges.map((edge) => edge.node)
      ),
    [tagFilter, tagChannels.data]
  );
  const tagQueryBlocked = multiTagSelection.disableQuery || (tagFilter.length > 1 && tagChannels.isError);
  const [nameFilter, setNameFilter] = useState<string>('');
  const [typeFilter, setTypeFilter] = useState<string[]>([]);
  const [statusFilter, setStatusFilter] = useState<string[]>([]);
  const [modelFilter, setModelFilter] = useState<string[]>([]);
  const [selectedTypeTab, setSelectedTypeTab] = useState(() => readDefaultProvider(CHANNEL_TYPE_TO_PROVIDER));
  const [showErrorOnly, setShowErrorOnly] = useState<boolean>(false);
  const [showHealthGateAbnormal, setShowHealthGateAbnormal] = useState(false);
  const [sorting, setSorting] = useState<SortingState>(readDefaultChannelSort);
  const [columnVisibility, setColumnVisibility] = useState<ChannelListColumnVisibility>(() => {
    const stored = localStorage.getItem('channels-table-column-visibility');
    if (!stored) return DEFAULT_CHANNEL_COLUMN_VISIBILITY;
    try {
      return parseChannelColumnVisibility(JSON.parse(stored));
    } catch {
      return DEFAULT_CHANNEL_COLUMN_VISIBILITY;
    }
  });

  useEffect(() => {
    if (!showTypeTabs && selectedTypeTab !== 'all') {
      setSelectedTypeTab('all');
      resetCursor();
    }
  }, [showTypeTabs, selectedTypeTab, resetCursor]);

  // Fetch channel types for tabs
  const { data: channelTypeCounts = [] } = useChannelTypes(statusFilter.length > 0 ? statusFilter : ['enabled', 'disabled']);

  // Fetch error channels count independently
  const { data: errorCount = 0 } = useErrorChannelsCount();

  // Debounce the name filter to avoid excessive API calls
  const debouncedNameFilter = useDebounce(nameFilter, 300);

  // Group protocol variants of the same vendor into one tab
  const channelTypeGroups = useMemo(() => groupChannelTypesByProvider(channelTypeCounts, CHANNEL_TYPE_TO_PROVIDER), [channelTypeCounts]);

  const selectedTypeGroup = useMemo(() => {
    const group = channelTypeGroups.find((group) => group.key === selectedTypeTab);
    if (group) return group;
    // Resolve saved defaults before counts load, and when the selected provider has no channels.
    const selection = resolveChannelProviderSelection(selectedTypeTab, CHANNEL_TYPE_TO_PROVIDER);
    return selection
      ? {
          key: selectedTypeTab,
          ...selection,
          totalCount: 0,
          primaryApiFormat: selection.isImage ? IMAGE_PRIMARY_API_FORMAT : undefined,
        }
      : undefined;
  }, [channelTypeGroups, selectedTypeTab]);
  const visibleTypeGroups = useMemo(
    () =>
      selectedTypeGroup && !channelTypeGroups.some((group) => group.key === selectedTypeTab)
        ? [...channelTypeGroups, selectedTypeGroup]
        : channelTypeGroups,
    [channelTypeGroups, selectedTypeGroup, selectedTypeTab]
  );

  // Get types for the selected tab
  const tabFilteredTypes = useMemo(() => {
    if (selectedTypeTab === 'all') {
      return [];
    }
    return selectedTypeGroup?.types ?? [];
  }, [selectedTypeTab, selectedTypeGroup]);

  // Build where clause with filters using useMemo
  const whereClause = useMemo(() => {
    const where: Record<string, unknown> = {};
    if (multiTagSelection.ids !== undefined) where.idIn = multiTagSelection.ids;
    if (debouncedNameFilter) {
      where.nameContainsFold = debouncedNameFilter;
    }
    // Combine tab filter with manual type filter
    const combinedTypeFilter = [...typeFilter];
    if (tabFilteredTypes.length > 0) {
      // If tab is selected, use tab types
      combinedTypeFilter.push(...tabFilteredTypes);
    }
    if (combinedTypeFilter.length > 0) {
      where.typeIn = Array.from(new Set(combinedTypeFilter)); // Remove duplicates
    }
    if (statusFilter.length > 0) {
      where.statusIn = statusFilter;
    } else {
      // By default, exclude archived channels when no status filter is applied
      where.statusIn = ['enabled', 'disabled'];
    }
    if (showErrorOnly) {
      where.errorMessageNotNil = true;
    }
    return Object.keys(where).length > 0 ? where : undefined;
  }, [debouncedNameFilter, tabFilteredTypes, typeFilter, statusFilter, showErrorOnly, multiTagSelection]);

  const currentOrderBy = useMemo(() => {
    if (sorting.length === 0) {
      return { field: 'CREATED_AT', direction: 'DESC' } as const;
    }
    const [primary] = sorting;
    switch (primary.id) {
      case 'orderingWeight':
        return { field: 'ORDERING_WEIGHT', direction: primary.desc ? 'DESC' : 'ASC' } as const;
      case 'name':
        return { field: 'NAME', direction: primary.desc ? 'DESC' : 'ASC' } as const;
      case 'status':
        return { field: 'STATUS', direction: primary.desc ? 'DESC' : 'ASC' } as const;
      case 'provider':
      case 'type':
        return { field: 'TYPE', direction: primary.desc ? 'DESC' : 'ASC' } as const;
      case 'createdAt':
        return { field: 'CREATED_AT', direction: primary.desc ? 'DESC' : 'ASC' } as const;
      case 'updatedAt':
        return { field: 'UPDATED_AT', direction: primary.desc ? 'DESC' : 'ASC' } as const;
      default:
        return { field: 'CREATED_AT', direction: 'DESC' } as const;
    }
  }, [sorting]);

  const query = useInfiniteChannels(
    {
      first: pageSize,
      where: whereClause,
      orderBy: currentOrderBy,
      routingOrder:
        sorting[0]?.id === 'priority' || sorting[0]?.id === 'costMultiplier'
          ? { field: sorting[0].id === 'priority' ? 'PRIORITY' : 'COST_MULTIPLIER', direction: sorting[0].desc ? 'DESC' : 'ASC' }
          : undefined,
      hasTag: tagFilter.length === 1 ? tagFilter[0] : undefined,
      models: modelFilter.length ? modelFilter : undefined,
      primaryApiFormat: selectedTypeGroup?.primaryApiFormat,
      excludePrimaryApiFormat: selectedTypeTab !== 'all' && !selectedTypeGroup?.primaryApiFormat ? IMAGE_PRIMARY_API_FORMAT : undefined,
      healthGateAbnormal: showHealthGateAbnormal || undefined,
      columnVisibility,
    },
    !tagQueryBlocked
  );
  const { data: queryData, isLoading, isPlaceholderData } = query;
  const filtersPending = nameFilter !== debouncedNameFilter;
  const data = tagQueryBlocked || isPlaceholderData || filtersPending ? EMPTY_CHANNEL_PAGE : queryData;

  const channelIDs = useMemo(() => {
    return data?.edges?.map((edge) => edge.node.id) || [];
  }, [data?.edges]);

  const channels = useMemo(() => data?.edges.map((edge) => edge.node) ?? [], [data?.edges]);

  const scroll = {
    resetKey: query.resetKey,
    loadingMore: query.isFetchingNextPage,
    fetching: query.isFetching,
    hasMore: !!query.hasNextPage,
    error: query.isError,
    onLoadMore: query.loadMore,
    hasPrevious: !!query.hasPreviousPage,
    loadingPrevious: query.isFetchingPreviousPage,
    onLoadPrevious: query.loadPrevious,
    onReset: query.resetToTop,
    onAtTopChange: (value: boolean) => {
      if (!value) query.pauseRefresh();
    },
  };

  // A cursor encodes the previous order's value, so it is meaningless once the order changes.
  const handleSortingChange = useCallback(
    (updater: SortingState | ((prev: SortingState) => SortingState)) => {
      setSorting(updater);
      resetCursor();
    },
    [resetCursor]
  );

  const handleNameFilterChange = useCallback(
    (filter: string) => {
      setNameFilter(filter);
      resetCursor();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  const handleTypeFilterChange = useCallback(
    (filters: string[]) => {
      setTypeFilter(filters);
      resetCursor();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  const handleTabChange = useCallback(
    (tab: string) => {
      setSelectedTypeTab(tab);
      setTypeFilter([]);
      resetCursor();
    },
    [setSelectedTypeTab, setTypeFilter, resetCursor]
  );

  const handleStatusFilterChange = useCallback(
    (filters: string[]) => {
      setStatusFilter(filters);
      resetCursor();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  const handleTagFilterChange = useCallback(
    (filter: string[]) => {
      setTagFilter(filter);
      resetCursor();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  const handleModelFilterChange = useCallback(
    (filter: string[]) => {
      setModelFilter(filter);
      resetCursor();
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  const handleFilterErrorChannels = useCallback(() => {
    setShowErrorOnly(true);
    resetCursor();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleExitErrorOnlyMode = useCallback(() => {
    setShowErrorOnly(false);
    resetCursor();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const handleHealthGateAbnormalChange = useCallback(
    (checked: boolean) => {
      setShowHealthGateAbnormal(checked);
      resetCursor();
    },
    [resetCursor]
  );

  const columns = useMemo(
    () => createColumns(t, channelPermissions.canWrite, tagFilter.length === 1 ? tagFilter[0] : '', currentQuotaRoutingSettings?.defaultMode),
    [t, channelPermissions.canWrite, tagFilter, currentQuotaRoutingSettings?.defaultMode]
  );

  return (
    <div className='flex min-w-0 flex-1 flex-col overflow-hidden'>
      <ChannelsErrorBanner
        errorCount={errorCount}
        onFilterErrorChannels={handleFilterErrorChannels}
        showErrorOnly={showErrorOnly}
        onExitErrorOnlyMode={handleExitErrorOnlyMode}
      />
      <ChannelsTypeTabs
        groups={visibleTypeGroups}
        selectedTab={selectedTypeTab}
        onTabChange={handleTabChange}
        className={showTypeTabs ? '' : 'hidden'}
      />
      {tagFilter.length > 1 && tagChannels.isError && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('common.loadError')}
            <Button variant='outline' size='sm' onClick={() => void tagChannels.refetch()}>
              {t('channels.defaultTags.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      <ChannelControlsProvider ids={channelIDs} visibility={columnVisibility} canWrite={channelPermissions.canWrite}>
      <ChannelsTable
        loading={isLoading || isPlaceholderData || filtersPending || (tagFilter.length > 1 && tagChannels.isLoading)}
        data={channels}
        columns={columns}
        pageSize={pageSize}
        totalCount={data?.totalCount}
        nameFilter={nameFilter}
        typeFilter={typeFilter}
        statusFilter={statusFilter}
        tagFilter={tagFilter}
        modelFilter={modelFilter}
        selectedTypeTab={selectedTypeTab}
        showErrorOnly={showErrorOnly}
        providerGroups={channelTypeGroups}
        onApplyProvider={(provider) => {
          setShowTypeTabs(true);
          handleTabChange(provider);
        }}
        showHealthGateAbnormal={showHealthGateAbnormal}
        onHealthGateAbnormalChange={handleHealthGateAbnormalChange}
        sorting={sorting}
        onSortingChange={handleSortingChange}
        onExitErrorOnlyMode={handleExitErrorOnlyMode}
        scroll={scroll}
        selectionScopeKey={query.selectionScopeKey}
        onNameFilterChange={handleNameFilterChange}
        onTypeFilterChange={handleTypeFilterChange}
        onStatusFilterChange={handleStatusFilterChange}
        onTagFilterChange={handleTagFilterChange}
        onModelFilterChange={handleModelFilterChange}
        columnVisibility={columnVisibility}
        onColumnVisibilityChange={setColumnVisibility}
        canWrite={channelPermissions.canWrite}
      />
      </ChannelControlsProvider>
    </div>
  );
}

export default function ChannelsManagement() {
  const { t } = useTranslation();

  return (
    <ChannelsProvider>
      <PageHeader title={t('channels.title')} description={t('channels.description')} actions={<ChannelsPrimaryButtons />} actionLayout='scroll' />

      <Main fixed className='py-2 sm:py-6 [--page-body-min-height:34rem]'>
        <ChannelsContent />
      </Main>
      <Suspense fallback={null}>
        <ChannelsDialogs />
      </Suspense>
    </ChannelsProvider>
  );
}
