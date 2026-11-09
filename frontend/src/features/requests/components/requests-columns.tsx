'use client';

import { useMemo, useRef } from 'react';
import { ColumnDef } from '@tanstack/react-table';
import { Ban, FileText } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { usePaginationSearch } from '@/hooks/use-pagination-search';
import { usePermissions } from '@/hooks/usePermissions';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { DataTableColumnHeader } from '@/components/data-table-column-header';
import { TokensCell, CostCell } from '@/components/request-usage-cells';
import { useGeneralSettings, useSecuritySettings, useUpdateSecuritySettings } from '@/features/system/data/system';
import { useRequestPermissions } from '../../../hooks/useRequestPermissions';
import { useRequestLogTimezone } from '../data/requests';
import { Request } from '../data/schema';
import { RequestLogStatusCell, RequestChannelCell, RequestCacheCell, RequestDurationCell, finalRequestChannel, requestProvider, type RequestDiagnosticTab } from './request-log-cells';
import { RequestModelCell } from './request-model-cell';

interface UseRequestsColumnsOptions {
  allowContinuationExpansion?: boolean;
  continuationHistory?: boolean;
  onBodyClick?: (requestId: string, index: number, tab?: RequestDiagnosticTab) => void;
  onCallerFilter?: (kind: 'key' | 'group', value: string) => void;
  onViewDetail?: (requestId: string) => void;
}

export const DEFAULT_HIDDEN_COLUMN_IDS = ['status', 'source', 'channelTag', 'apiKeyGroup', 'apiFormat', 'clientIP', 'userAgent', 'channelAPIKeyIndex'];

export const DEFAULT_MOBILE_HIDDEN_COLUMN_IDS = [
  ...DEFAULT_HIDDEN_COLUMN_IDS,
  'channel',
  'tokens',
  'cache',
  'provider',
  'cost',
  'duration',
  'caller',
];

export const MODEL_ID_COLUMN = 'modelID' as const;

export function useRequestsColumns(options?: UseRequestsColumnsOptions): ColumnDef<Request>[] {
  const { t, i18n } = useTranslation();
  const permissions = useRequestPermissions();
  const { hasSystemScope } = usePermissions();
  const { data: timezone } = useRequestLogTimezone();
  const { data: settings } = useGeneralSettings();
  const { data: securitySettings } = useSecuritySettings();
  const updateSecuritySettings = useUpdateSecuritySettings();
  const { navigateWithSearch } = usePaginationSearch({ defaultPageSize: 20 });
  const canManageSecuritySettings = hasSystemScope('write_settings');
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const mutateSecuritySettings = updateSecuritySettings.mutateAsync;

  return useMemo(() => {
    const blockedIPs = securitySettings?.blockedIPs ?? [];
    const showIPBanIcon = securitySettings?.showRequestLogIPBanIcon === true;
    const dateFormatter = timezone
      ? new Intl.DateTimeFormat(i18n.language.startsWith('zh') ? 'zh-CN' : 'en-GB', {
          timeZone: timezone,
          year: 'numeric',
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
          hourCycle: 'h23',
        })
      : undefined;

    const normalizeBlockedIPs = (ips: string[]) => Array.from(new Set(ips.map((ip) => ip.trim()).filter((ip) => ip.length > 0)));

    const handleBlockIP = async (clientIP: string) => {
      const normalizedIP = clientIP.trim();
      if (!normalizedIP) return;

      const nextBlockedIPs = normalizeBlockedIPs([...blockedIPs, normalizedIP]);
      if (nextBlockedIPs.length === blockedIPs.length && blockedIPs.includes(normalizedIP)) {
        toast.info(t('requests.actions.ipAlreadyBlocked'));
        return;
      }

      await mutateSecuritySettings({ blockedIPs: nextBlockedIPs });
    };

    const handleUnblockIP = async (clientIP: string) => {
      const normalizedIP = clientIP.trim();
      if (!normalizedIP) return;

      await mutateSecuritySettings({ blockedIPs: blockedIPs.filter((ip) => ip.trim() !== normalizedIP) });
    };

    const openDetail = (requestId: string) => {
      if (optionsRef.current?.onViewDetail) {
        optionsRef.current.onViewDetail(requestId);
        return;
      }

      navigateWithSearch({
        to: '/project/requests/$requestId',
        params: { requestId },
      });
    };

    const columns: ColumnDef<Request>[] = [
      {
        accessorKey: 'id',
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('common.columns.id')} />,
        enableSorting: true,
        enableHiding: false,
        cell: ({ row }) => (
          <RequestLogStatusCell
            request={row.original}
            onToggleContinuation={optionsRef.current?.allowContinuationExpansion ? row.getToggleExpandedHandler() : undefined}
            continuationExpanded={row.getIsExpanded()}
            continued={optionsRef.current?.continuationHistory}
            onOpen={(tab) => optionsRef.current?.onBodyClick?.(row.original.id, row.index, tab)}
          />
        ),
      },
      {
        id: 'status',
        accessorKey: 'status',
        enableHiding: false,
        filterFn: (row, id, value) => value.includes(row.getValue(id)),
        cell: () => null,
      },
      {
        id: 'channelTag',
        accessorFn: (row: Request) => row.channel?.tags ?? [],
        getUniqueValues: (row: Request) => row.channel?.tags ?? [],
        enableHiding: false,
        cell: () => null,
      },
      { id: 'apiKeyGroup', accessorFn: (row: Request) => String(row.apiKey?.groupRef ?? 0), enableHiding: false, cell: () => null },
      ...(permissions.canViewChannels
        ? ([
            {
              id: 'channel',
              accessorFn: (row) => finalRequestChannel(row).channel?.id ?? '',
              header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.channel')} />,
              enableSorting: false,
              enableHiding: true,
              cell: ({ row }) => (
                <RequestChannelCell
                  request={row.original}
                  onOpen={() => optionsRef.current?.onBodyClick?.(row.original.id, row.index, 'routing')}
                />
              ),
              filterFn: (row, _id, value) => {
                if (value.length === 0) return true;
                const { channel } = finalRequestChannel(row.original);
                return !!channel?.id && value.includes(channel.id);
              },
            },
          ] as ColumnDef<Request>[])
        : []),
      {
        id: 'modelID',
        accessorFn: (row) => row.modelID,
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.model')} />,
        enableSorting: false,
        enableHiding: false,
        cell: ({ row }) => {
          return <RequestModelCell request={row.original} />;
        },
      },
      {
        id: 'apiFormat',
        accessorFn: (row) => row.format,
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.apiFormat')} />,
        enableSorting: false,
        enableHiding: true,
        cell: ({ row }) => {
          const format = row.original.format;
          return format ? (
            <span className='inline-flex items-center rounded-md border border-zinc-200 bg-zinc-50 px-2 py-0.5 text-xs font-medium text-zinc-700 dark:border-zinc-700 dark:bg-zinc-800/50 dark:text-zinc-300'>
              {format}
            </span>
          ) : (
            <span className='text-muted-foreground text-xs'>-</span>
          );
        },
      },
      {
        id: 'source',
        accessorKey: 'source',
        enableHiding: false,
        filterFn: (row, id, value) => value.includes(row.getValue(id)),
        cell: () => null,
      },
      {
        id: 'clientIP',
        accessorKey: 'clientIP',
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.clientIP')} />,
        enableSorting: false,
        enableHiding: true,
        cell: ({ row }) => {
          const normalizedIP = row.original.clientIP?.trim() ?? '';
          if (!normalizedIP) return <span className='text-muted-foreground text-xs'>-</span>;

          const isBlocked = blockedIPs.includes(normalizedIP);
          return (
            <div className='flex items-center gap-2'>
              <span className='font-mono text-xs'>{normalizedIP}</span>
              {canManageSecuritySettings &&
                showIPBanIcon &&
                (isBlocked ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon-sm'
                        className='h-6 w-6 shrink-0 text-red-500/80 hover:bg-red-50 hover:text-red-600 dark:text-red-300/80 dark:hover:bg-red-950/30 dark:hover:text-red-300'
                        disabled={updateSecuritySettings.isPending}
                        onClick={() => void handleUnblockIP(normalizedIP)}
                        aria-label={t('requests.actions.unblockIP')}
                      >
                        <Ban className='h-3.5 w-3.5' />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>{t('requests.actions.unblockIP')}</TooltipContent>
                  </Tooltip>
                ) : (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon-sm'
                        className='text-muted-foreground h-6 w-6 shrink-0 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/30 dark:hover:text-red-300'
                        disabled={updateSecuritySettings.isPending}
                        onClick={() => void handleBlockIP(normalizedIP)}
                        aria-label={t('requests.actions.blockIP')}
                      >
                        <Ban className='h-3.5 w-3.5' />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>{t('requests.actions.blockIP')}</TooltipContent>
                  </Tooltip>
                ))}
            </div>
          );
        },
      },
      ...(permissions.canViewChannels
        ? ([
            {
              id: 'provider',
              accessorFn: requestProvider,
              header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.provider')} />,
              enableSorting: false,
              enableHiding: true,
              cell: ({ row }) => {
                const provider = requestProvider(row.original);
                return (
                  <span className='text-xs whitespace-nowrap'>
                    {provider ? t(`channels.providers.${provider}`, { defaultValue: provider }) : '—'}
                  </span>
                );
              },
            },
          ] as ColumnDef<Request>[])
        : []),
      {
        id: 'tokens',
        accessorFn: (row) => {
          const usageLog = row.usageSummary;
          return (usageLog?.promptTokens || 0) + (usageLog?.completionTokens || 0);
        },
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.tokens')} />,
        cell: ({ row }) => {
          const log = row.original.usageSummary;
          return log ? (
            <TokensCell
              prompt={log.promptTokens || 0}
              completion={log.completionTokens || 0}
              reasoning={log.completionReasoningTokens || 0}
              cached={log.promptCachedTokens ?? 0}
              writeCached={log.promptWriteCachedTokens ?? 0}
            />
          ) : (
            <div className='text-muted-foreground text-xs'>-</div>
          );
        },
        enableSorting: true,
        enableHiding: true,
        sortingFn: (rowA, rowB) => {
          const a =
            (rowA.original.usageSummary?.promptTokens || 0) +
            (rowA.original.usageSummary?.completionTokens || 0);
          const b =
            (rowB.original.usageSummary?.promptTokens || 0) +
            (rowB.original.usageSummary?.completionTokens || 0);
          return a - b;
        },
      },
      {
        id: 'cache',
        accessorFn: (row) => row.usageSummary?.promptCachedTokens ?? null,
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.cache')} />,
        cell: ({ row }) => <RequestCacheCell request={row.original} />,
        enableSorting: true,
        enableHiding: true,
      },
      {
        id: 'cost',
        accessorFn: (row) => row.usageSummary?.totalCost ?? null,
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.cost')} />,
        enableSorting: false,
        enableHiding: true,
        cell: ({ row }) => {
          const log = row.original.usageSummary;
          return (
            <CostCell
              total={log?.totalCost ?? null}
              currencyCode={settings?.currencyCode ?? 'USD'}
              breakdown={
                log?.costItems
                  ? {
                      cacheWriteTokens5m: log.promptWriteCachedTokens5m,
                      cacheWriteTokens1h: log.promptWriteCachedTokens1h,
                      items: (log.costItems ?? []).map((item) => ({
                        pricingEvidence: item.pricingEvidence,
                        image: item.image,
                        imageRule: item.imageRule,
                        unpriced: item.unpriced,
                        unpricedReason: item.unpricedReason,
                        itemCode: item.itemCode,
                        variant: item.promptWriteCacheVariantCode ?? null,
                        quantity: item.quantity,
                        subtotal: item.subtotal,
                        tiers: item.tierBreakdown ?? [],
                      })),
                      multiplier: log.costPriceMultiplier ?? 'mixed',
                      unpricedRecords: log.unpricedRecords,
                    }
                  : null
              }
              emptyLabel='-'
            />
          );
        },
      },
      {
        id: 'duration',
        accessorFn: (row) => row.metricsLatencyMs ?? null,
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.duration')} />,
        enableSorting: true,
        enableHiding: true,
        cell: ({ row }) => <RequestDurationCell request={row.original} />,
        sortingFn: (rowA, rowB) => (rowA.original.metricsLatencyMs ?? 0) - (rowB.original.metricsLatencyMs ?? 0),
      },
      {
        id: 'caller',
        accessorFn: (row) => row.apiKey?.id ?? '',
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.caller')} />,
        enableSorting: false,
        enableHiding: true,
        cell: ({ row }) => {
          const request = row.original;
          if (request.source !== 'api') {
            return <Badge variant='secondary'>{t(`requests.source.${request.source}`)}</Badge>;
          }

          return (
            <div className='flex min-w-[120px] flex-col gap-0.5'>
              {request.apiKey && (
                <button
                  type='button'
                  className='text-primary max-w-[160px] truncate text-left text-xs font-medium hover:underline'
                  onClick={() => optionsRef.current?.onCallerFilter?.('group', String(request.apiKey?.groupRef ?? 0))}
                >
                  {request.apiKey.groupName || t('requests.filters.ungrouped')}
                </button>
              )}
              {request.apiKey?.id ? (
                <button
                  type='button'
                  className='text-muted-foreground max-w-[160px] truncate text-left text-xs hover:underline'
                  onClick={() => optionsRef.current?.onCallerFilter?.('key', request.apiKey!.id!)}
                >
                  {request.apiKey.name}
                </button>
              ) : (
                <span>—</span>
              )}
            </div>
          );
        },
        filterFn: (row, _id, value) => value.length === 0 || value.includes(row.original.apiKey?.id ?? ''),
      },
    {
      id: 'userAgent',
      accessorKey: 'userAgent',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.userAgent')} />,
      enableSorting: false,
      enableHiding: true,
      cell: ({ row }) => {
        const userAgent = row.original.userAgent?.trim() ?? '';
        if (!userAgent) return <span className='text-muted-foreground text-xs'>-</span>;

        return (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className='block max-w-[240px] cursor-help truncate font-mono text-xs'>{userAgent}</span>
            </TooltipTrigger>
            <TooltipContent className='max-w-[420px] break-all'>{userAgent}</TooltipContent>
          </Tooltip>
        );
      },
    },
      ...(permissions.canViewChannels ? [
          {
            id: 'channelAPIKeyIndex',
            accessorFn: (row) => row.executions?.edges?.[0]?.node?.channelAPIKeyIndex ?? '',
            header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.channelAPIKeyIndex')} />,
            enableSorting: false,
            enableHiding: true,
            cell: ({ row }) => {
              const index = row.original.executions?.edges?.[0]?.node?.channelAPIKeyIndex;

              if (index == null) return <span className='text-muted-foreground font-mono text-xs'>-</span>;

              return <span className='font-mono text-xs'>key{index}</span>;
            },
          },
      ] as ColumnDef<Request>[] : []),
      {
        accessorKey: 'createdAt',
        header: ({ column }) => <DataTableColumnHeader column={column} title={t('common.columns.createdAt')} />,
        enableSorting: true,
        enableHiding: true,
        cell: ({ row }) => (
          <span className='text-xs whitespace-nowrap'>{dateFormatter?.format(new Date(row.original.createdAt)) ?? '—'}</span>
        ),
      },
      {
        id: 'details',
        header: () => <span className='sr-only'>{t('requests.columns.details')}</span>,
        cell: ({ row }) => (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type='button'
                variant='ghost'
                size='icon-sm'
                className='h-8 w-8'
                onClick={() => openDetail(row.original.id)}
                aria-label={t('requests.actions.viewDetails')}
              >
                <FileText className='h-4 w-4' />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('requests.actions.viewDetails')}</TooltipContent>
          </Tooltip>
        ),
        enableHiding: false,
      },
    ];

    return columns;
  }, [
    t,
    i18n.language,
    permissions.canViewChannels,
    timezone,
    settings,
    securitySettings,
    canManageSecuritySettings,
    mutateSecuritySettings,
    navigateWithSearch,
  ]);
}
