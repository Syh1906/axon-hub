'use client';

import { ColumnDef } from '@tanstack/react-table';
import { IconArrowsExchange, IconClockHour1, IconRoute } from '@tabler/icons-react';
import { Ban, FileText, Copy } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { copyTextToClipboard } from '@/lib/clipboard';
import { usePaginationSearch } from '@/hooks/use-pagination-search';
import { usePermissions } from '@/hooks/usePermissions';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { DataTableColumnHeader } from '@/components/data-table-column-header';
import { TokensCell, CostCell } from '@/components/request-usage-cells';
import { useGeneralSettings, useSecuritySettings, useUpdateSecuritySettings } from '@/features/system/data/system';
import { useRequestPermissions } from '../../../hooks/useRequestPermissions';
import { Request } from '../data/schema';
import { useRequestLogTimezone } from '../data/requests';
import {
  RequestLogStatusCell,
  RequestChannelCell,
  RequestCacheCell,
  RequestDurationCell,
  requestProvider,
  type RequestDiagnosticTab,
} from './request-log-cells';

interface UseRequestsColumnsOptions {
  onBodyClick?: (requestId: string, index: number, tab?: RequestDiagnosticTab) => void;
  onCallerFilter?: (kind: 'key' | 'group', value: string) => void;
  onViewDetail?: (requestId: string) => void;
}

export const DEFAULT_HIDDEN_COLUMN_IDS = ['status', 'source', 'channelTag', 'apiKeyGroup', 'apiFormat', 'clientIP'];

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

  const blockedIPs = securitySettings?.blockedIPs ?? [];
  const showIPBanIcon = securitySettings?.showRequestLogIPBanIcon === true;

  const normalizeBlockedIPs = (ips: string[]) => Array.from(new Set(ips.map((ip) => ip.trim()).filter((ip) => ip.length > 0)));

  const handleBlockIP = async (clientIP: string) => {
    const normalizedIP = clientIP.trim();
    if (!normalizedIP) return;

    const nextBlockedIPs = normalizeBlockedIPs([...blockedIPs, normalizedIP]);
    if (nextBlockedIPs.length === blockedIPs.length && blockedIPs.includes(normalizedIP)) {
      toast.info(t('requests.actions.ipAlreadyBlocked'));
      return;
    }

    await updateSecuritySettings.mutateAsync({ blockedIPs: nextBlockedIPs });
  };

  const handleUnblockIP = async (clientIP: string) => {
    const normalizedIP = clientIP.trim();
    if (!normalizedIP) return;

    await updateSecuritySettings.mutateAsync({ blockedIPs: blockedIPs.filter((ip) => ip.trim() !== normalizedIP) });
  };

  const openDetail = (requestId: string) => {
    if (options?.onViewDetail) {
      options.onViewDetail(requestId);
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
        <RequestLogStatusCell request={row.original} onOpen={(tab) => options?.onBodyClick?.(row.original.id, row.index, tab)} />
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
    {
      id: 'modelID',
      accessorFn: (row) => row.modelID,
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.model')} />,
      enableSorting: false,
      enableHiding: false,
      cell: ({ row }) => {
        const request = row.original;
        const originalModelId = request.modelID || t('requests.columns.unknown');
        const executions = request.executions?.edges?.flatMap((edge) => (edge.node ? [edge.node] : [])) ?? [];
        const executionModelIds = Array.from(new Set(executions.map((exe) => exe.modelID || ''))).filter(
          (id) => id && id !== originalModelId
        );
        const reasoningEffort = executions[0]?.reasoningEffort ?? request.reasoningEffort;
        const inboundFormat = request.format;
        const outboundFormat = executions[0]?.format;
        const passThroughApplied = executions.some((execution) => execution.passThroughApplied);
        const oneHourPromptCache = (request.oneHourPromptCacheUsageLogs?.totalCount ?? 0) > 0;
        // Orange is reserved for a confirmed mismatch: a missing format on either
        // side is "unknown" and stays muted.
        const formatsComparable = Boolean(inboundFormat && outboundFormat);
        const outboundProtocolMatches = formatsComparable && outboundFormat === inboundFormat;
        const outboundProtocolTooltip = !formatsComparable
          ? t('requests.tooltips.outboundProtocolUnknown')
          : outboundProtocolMatches
            ? t('requests.tooltips.outboundProtocolMatching', { protocol: outboundFormat })
            : t('requests.tooltips.outboundProtocolConverted', { protocol: outboundFormat });

        const copyModelID = async () => {
          try {
            await copyTextToClipboard(request.modelID);
            toast.success(t('requests.actions.copied'));
          } catch {
            toast.error(t('requests.diagnostics.copyFailed'));
          }
        };
        const modelLabel =
          executionModelIds.length > 0 ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type='button'
                  onClick={copyModelID}
                  aria-label={t('requests.diagnostics.copyModel', { model: request.modelID })}
                  className='flex max-w-[180px] cursor-pointer items-center gap-1.5 rounded-lg border border-amber-200 bg-amber-50 px-2 py-0.5 font-mono text-xs font-medium text-amber-700 transition-colors hover:bg-amber-100 dark:border-amber-800/50 dark:bg-amber-900/30 dark:text-amber-300 dark:hover:bg-amber-900/50'
                >
                  <span className='truncate'>{originalModelId}</span>
                  <IconRoute className='h-3.5 w-3.5 opacity-80' />
                </button>
              </TooltipTrigger>
              <TooltipContent side='right' className='border-amber-200 bg-white dark:bg-zinc-900'>
                <div className='flex items-center gap-2 p-2'>
                  <span className='text-muted-foreground text-xs whitespace-nowrap'>{t('requests.columns.executedModelId')}:</span>
                  <span className='rounded bg-amber-100 px-2 py-0.5 text-xs font-medium whitespace-nowrap text-amber-800 dark:bg-amber-900/40 dark:text-amber-200'>
                    {executionModelIds[0]}
                  </span>
                </div>
              </TooltipContent>
            </Tooltip>
          ) : (
            <button
              type='button'
              onClick={copyModelID}
              className='flex max-w-[180px] items-center gap-1.5 text-left font-mono text-xs font-medium'
              aria-label={t('requests.diagnostics.copyModel', { model: request.modelID })}
            >
              <span className='truncate'>{originalModelId}</span>
              <Copy className='text-muted-foreground size-3 shrink-0' />
            </button>
          );

        return (
          <div className='flex min-w-[160px] flex-col gap-1'>
            {modelLabel}
            <div className='flex items-center gap-1.5'>
              {reasoningEffort && (
                <Badge className='border-sky-200 bg-sky-100 text-sky-800 dark:border-sky-800 dark:bg-sky-900/20 dark:text-sky-300'>
                  {reasoningEffort}
                </Badge>
              )}
              <Tooltip>
                <TooltipTrigger asChild>
                  <span
                    className={`inline-flex h-5 w-5 items-center justify-center ${
                      !formatsComparable
                        ? 'text-muted-foreground/45'
                        : outboundProtocolMatches
                          ? 'text-emerald-700 dark:text-emerald-300'
                          : 'text-orange-700 dark:text-orange-300'
                    }`}
                    tabIndex={0}
                    role='img'
                    aria-label={outboundProtocolTooltip}
                  >
                    <IconArrowsExchange className='h-3.5 w-3.5' />
                  </span>
                </TooltipTrigger>
                <TooltipContent>{outboundProtocolTooltip}</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span
                    className={`inline-flex h-5 w-5 items-center justify-center ${
                      passThroughApplied ? 'text-amber-700 dark:text-amber-300' : 'text-muted-foreground/45'
                    }`}
                    tabIndex={0}
                    role='img'
                    aria-label={t(passThroughApplied ? 'requests.tooltips.passThroughApplied' : 'requests.tooltips.passThroughNotApplied')}
                  >
                    <IconRoute className='h-3.5 w-3.5' />
                  </span>
                </TooltipTrigger>
                <TooltipContent>
                  {t(passThroughApplied ? 'requests.tooltips.passThroughApplied' : 'requests.tooltips.passThroughNotApplied')}
                </TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span
                    className={`inline-flex h-5 w-5 items-center justify-center ${
                      oneHourPromptCache ? 'text-violet-700 dark:text-violet-300' : 'text-muted-foreground/45'
                    }`}
                    tabIndex={0}
                    role='img'
                    aria-label={t(
                      oneHourPromptCache ? 'requests.tooltips.oneHourPromptCacheApplied' : 'requests.tooltips.oneHourPromptCacheNotApplied'
                    )}
                  >
                    <IconClockHour1 className='h-3.5 w-3.5' />
                  </span>
                </TooltipTrigger>
                <TooltipContent>
                  {t(oneHourPromptCache ? 'requests.tooltips.oneHourPromptCacheApplied' : 'requests.tooltips.oneHourPromptCacheNotApplied')}
                </TooltipContent>
              </Tooltip>
            </div>
          </div>
        );
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
            id: 'channel',
            accessorFn: (row) => row.executions?.edges?.[0]?.node?.channel?.id ?? row.channel?.id ?? '',
            header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.channel')} />,
            enableSorting: false,
            enableHiding: true,
            cell: ({ row }) => (
              <RequestChannelCell request={row.original} onOpen={() => options?.onBodyClick?.(row.original.id, row.index, 'routing')} />
            ),
            filterFn: (row, _id, value) => {
              if (value.length === 0) return true;
              const channel = row.original.executions?.edges?.[0]?.node?.channel ?? row.original.channel;
              return !!channel?.id && value.includes(channel.id);
            },
          },
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
        const usageLog = row.usageLogs?.edges?.[0]?.node;
        return (usageLog?.promptTokens || 0) + (usageLog?.completionTokens || 0);
      },
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.tokens')} />,
      cell: ({ row }) => {
        const log = row.original.usageLogs?.edges?.[0]?.node;
        return log ? (
          <TokensCell
            prompt={log.promptTokens || 0}
            completion={log.completionTokens || 0}
            reasoning={log.completionReasoningTokens || 0}
          />
        ) : (
          <div className='text-muted-foreground text-xs'>-</div>
        );
      },
      enableSorting: true,
      enableHiding: true,
      sortingFn: (rowA, rowB) => {
        const a =
          (rowA.original.usageLogs?.edges?.[0]?.node?.promptTokens || 0) +
          (rowA.original.usageLogs?.edges?.[0]?.node?.completionTokens || 0);
        const b =
          (rowB.original.usageLogs?.edges?.[0]?.node?.promptTokens || 0) +
          (rowB.original.usageLogs?.edges?.[0]?.node?.completionTokens || 0);
        return a - b;
      },
    },
    {
      id: 'cache',
      accessorFn: (row) => row.usageLogs?.edges?.[0]?.node?.promptCachedTokens ?? null,
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.cache')} />,
      cell: ({ row }) => <RequestCacheCell request={row.original} />,
      enableSorting: true,
      enableHiding: true,
    },
    {
      id: 'cost',
      accessorFn: (row) => row.usageLogs?.edges?.[0]?.node?.totalCost ?? null,
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('requests.columns.cost')} />,
      enableSorting: false,
      enableHiding: true,
      cell: ({ row }) => {
        const log = row.original.usageLogs?.edges?.[0]?.node;
        return (
          <CostCell
            total={log?.totalCost ?? null}
            currencyCode={settings?.currencyCode ?? 'USD'}
            breakdown={
              log
                ? {
                    items: (log.costItems ?? []).map((item) => ({
                      image: item.image,
                      imageRule: item.imageRule,
                      unpriced: item.unpriced,
                      itemCode: item.itemCode,
                      variant: item.promptWriteCacheVariantCode ?? null,
                      quantity: item.quantity,
                      subtotal: item.subtotal,
                      tiers: item.tierBreakdown ?? [],
                    })),
                    multiplier: log.costPriceMultiplier ?? 'mixed',
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
            {request.apiKey?.id ? (
              <button
                type='button'
                className='text-primary max-w-[160px] truncate text-left text-xs hover:underline'
                onClick={() => options?.onCallerFilter?.('key', request.apiKey!.id!)}
              >
                {request.apiKey.name}
              </button>
            ) : (
              <span>—</span>
            )}
            {request.apiKey && (
              <button
                type='button'
                className='text-muted-foreground max-w-[160px] truncate text-left text-xs hover:underline'
                onClick={() => options?.onCallerFilter?.('group', String(request.apiKey?.groupRef ?? 0))}
              >
                {request.apiKey.groupName || t('requests.filters.ungrouped')}
              </button>
            )}
          </div>
        );
      },
      filterFn: (row, _id, value) => value.length === 0 || value.includes(row.original.apiKey?.id ?? ''),
    },
    {
      accessorKey: 'createdAt',
      header: ({ column }) => <DataTableColumnHeader column={column} title={t('common.columns.createdAt')} />,
      enableSorting: true,
      enableHiding: true,
      cell: ({ row }) => (
        <span className='text-xs whitespace-nowrap'>
          {timezone ? new Intl.DateTimeFormat(i18n.language.startsWith('zh') ? 'zh-CN' : 'en-GB', {
            timeZone: timezone,
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
            hourCycle: 'h23',
          }).format(new Date(row.original.createdAt)) : '—'}
        </span>
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
}
