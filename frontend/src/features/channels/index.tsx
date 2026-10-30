import { useState, useMemo, useCallback, useEffect, lazy, Suspense } from 'react';
import { SortingState } from '@tanstack/react-table';
import { useTranslation } from 'react-i18next';
import { useDebounce } from '@/hooks/use-debounce';
import { usePaginationSearch } from '@/hooks/use-pagination-search';
import { usePermissions } from '@/hooks/usePermissions';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { useProvidersData } from '@/features/models/data/providers';
import { createColumns } from './components/channels-columns';
import { ChannelsErrorBanner } from './components/channels-error-banner';
import { ChannelsPrimaryButtons } from './components/channels-primary-buttons';
import { ChannelsTable } from './components/channels-table';
import { ChannelsTypeTabs } from './components/channels-type-tabs';
import ChannelsProvider, { useChannels } from './context/channels-context';
import { useChannelUpstreamStates, useRefreshUpstreamOnPage } from './data/channel-upstream';
import { useChannelTodayUsage } from './data/channel-usage';
import {
  DEFAULT_CHANNEL_COLUMN_VISIBILITY,
  useInfiniteChannels,
  useAllChannelSummarys,
  useChannelTypes,
  useErrorChannelsCount,
  useChannelProbeData,
  type ChannelListColumnVisibility,
} from './data/channels';
import { CHANNEL_TYPE_TO_PROVIDER } from './data/config_channels';
import { readDefaultChannelSort } from './utils/channel-sort';
import { readDefaultProvider, resolveChannelProviderSelection } from './utils/default-provider';
import { resolveChannelTagSelection } from './utils/default-tags';
import { groupChannelTypesByProvider, IMAGE_PRIMARY_API_FORMAT } from './utils/group-channel-types';

const ChannelsDialogs = lazy(() => import('./components/channels-dialogs').then((m) => ({ default: m.ChannelsDialogs })));

const EMPTY_CHANNEL_PAGE = {
  edges: [],
  totalCount: 0,
  pageInfo: { hasNextPage: false, hasPreviousPage: false, startCursor: null, endCursor: null },
};

function ChannelsContent() {
  const { t } = useTranslation();
  useProvidersData();
  const { channelPermissions } = usePermissions();
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
  const [modelFilter, setModelFilter] = useState<string>('');
  const [selectedTypeTab, setSelectedTypeTab] = useState(() => readDefaultProvider(CHANNEL_TYPE_TO_PROVIDER));
  const [showErrorOnly, setShowErrorOnly] = useState<boolean>(false);
  const [showHealthGateAbnormal, setShowHealthGateAbnormal] = useState(false);
  const [sorting, setSorting] = useState<SortingState>(readDefaultChannelSort);
  const [columnVisibility, setColumnVisibility] = useState<ChannelListColumnVisibility>(() => {
    const stored = localStorage.getItem('channels-table-column-visibility');
    if (stored) {
      try {
        return { ...DEFAULT_CHANNEL_COLUMN_VISIBILITY, ...JSON.parse(stored) };
      } catch {
        return DEFAULT_CHANNEL_COLUMN_VISIBILITY;
      }
    }
    return DEFAULT_CHANNEL_COLUMN_VISIBILITY;
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

  const [atTop, setAtTop] = useState(true);
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
      model: modelFilter || undefined,
      primaryApiFormat: selectedTypeGroup?.primaryApiFormat,
      excludePrimaryApiFormat: selectedTypeTab !== 'all' && !selectedTypeGroup?.primaryApiFormat ? IMAGE_PRIMARY_API_FORMAT : undefined,
      healthGateAbnormal: showHealthGateAbnormal || undefined,
      columnVisibility,
    },
    !tagQueryBlocked,
    atTop
  );
  const { data: queryData, isLoading, isPlaceholderData } = query;
  const filtersPending = nameFilter !== debouncedNameFilter;
  const data = tagQueryBlocked || isPlaceholderData || filtersPending ? EMPTY_CHANNEL_PAGE : queryData;

  const channelIDs = useMemo(() => {
    return data?.edges?.map((edge) => edge.node.id) || [];
  }, [data?.edges]);

  const { data: probeData } = useChannelProbeData(channelIDs, { enabled: columnVisibility.health !== false });
  const upstreamQuery = useChannelUpstreamStates(channelIDs);
  const todayUsageQuery = useChannelTodayUsage(channelIDs);
  useRefreshUpstreamOnPage(channelIDs, channelPermissions.canWrite && upstreamQuery.isSuccess);

  const channelsWithProbeData = useMemo(() => {
    if (!data?.edges) return [];

    const probeMap = new Map(probeData?.map((probe) => [probe.channelID, probe.points]) || []);

    return data.edges.map((edge) => ({
      ...edge.node,
      probePoints: probeMap.get(edge.node.id) || [],
      upstreamState: upstreamQuery.data?.find((state) => state.channelId === edge.node.id),
      upstreamLoadError: upstreamQuery.isError,
      todayUsage: todayUsageQuery.data?.find((usage) => usage.channelId === edge.node.id),
      todayUsageError: todayUsageQuery.isError,
    }));
  }, [data?.edges, probeData, upstreamQuery.data, upstreamQuery.isError, todayUsageQuery.data, todayUsageQuery.isError]);

  const scroll = {
    resetKey: query.resetKey,
    loadingMore: query.isFetchingNextPage,
    fetching: query.isFetching,
    hasMore: !!query.hasNextPage,
    error: query.isError,
    onLoadMore: query.loadMore,
    onTrim: query.trimAfter,
    onReset: query.resetToTop,
    onAtTopChange: (value: boolean) => {
      setAtTop(value);
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
    (filter: string) => {
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
    () => createColumns(t, channelPermissions.canWrite, tagFilter.length === 1 ? tagFilter[0] : ''),
    [t, channelPermissions.canWrite, tagFilter]
  );

  return (
    <div className='flex flex-1 flex-col overflow-hidden'>
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
      <ChannelsTable
        loading={isLoading || isPlaceholderData || filtersPending || (tagFilter.length > 1 && tagChannels.isLoading)}
        data={channelsWithProbeData}
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
        onNameFilterChange={handleNameFilterChange}
        onTypeFilterChange={handleTypeFilterChange}
        onStatusFilterChange={handleStatusFilterChange}
        onTagFilterChange={handleTagFilterChange}
        onModelFilterChange={handleModelFilterChange}
        columnVisibility={columnVisibility}
        onColumnVisibilityChange={setColumnVisibility}
        canWrite={channelPermissions.canWrite}
      />
    </div>
  );
}

export default function ChannelsManagement() {
  const { t } = useTranslation();

  return (
    <ChannelsProvider>
      <Header fixed>
        <div className='flex w-full flex-1 flex-col gap-2 md:flex-row md:items-center md:justify-between md:gap-0'>
          <div className='min-w-0'>
            <h2 className='text-xl font-bold tracking-tight'>{t('channels.title')}</h2>
            <p className='text-muted-foreground text-sm'>{t('channels.description')}</p>
          </div>
          <ChannelsPrimaryButtons />
        </div>
      </Header>

      <Main fixed>
        <ChannelsContent />
      </Main>
      <Suspense fallback={null}>
        <ChannelsDialogs />
      </Suspense>
    </ChannelsProvider>
  );
}
