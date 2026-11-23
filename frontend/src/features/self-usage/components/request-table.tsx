import { useCallback, type RefObject } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { CostCell, TokensCell } from '@/components/request-usage-cells';
import { getProviderFromChannelType } from '@/features/channels/data/config_providers';
import { RequestChannelDisplayCell } from '@/features/requests/components/request-channel-display-cell';
import { RequestCacheCell, RequestDurationCell, RequestLogStatusCell } from '@/features/requests/components/request-log-cells';
import { RequestModelCell } from '@/features/requests/components/request-model-cell';
import type { SelfUsageMeta, UsageRequest } from '../api';

// Fixed column widths keep the header and visible rows aligned when virtual rows change.
const requestColumnWidths = { id: 145, model: 180, tokens: 136, cache: 141, cost: 144, duration: 166, caller: 136, createdAt: 150 };
const standardWidths = Object.values(requestColumnWidths);
const channelWidths = [requestColumnWidths.id, 210, ...standardWidths.slice(1)];

export function UsageRequestTable({
  items,
  onSessionFilter,
  meta,
  channelVisible,
  containerRef,
  animate,
}: {
  items: UsageRequest[];
  onSessionFilter: (id: string) => void;
  meta: SelfUsageMeta;
  channelVisible: boolean;
  containerRef: RefObject<HTMLDivElement | null>;
  animate: boolean;
}) {
  const { t, i18n } = useTranslation();
  const getItemKey = useCallback((index: number) => items[index].id, [items]);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => containerRef.current,
    getItemKey,
    estimateSize: () => 80,
    overscan: 8,
    scrollMargin: 40,
    useFlushSync: false,
  });
  // A head refresh can scroll to zero before the virtualizer receives its scroll event.
  // Only compensate for measurements above the actual reading position.
  virtualizer.shouldAdjustScrollPositionOnItemSizeChange = (row) => row.end <= (containerRef.current?.scrollTop ?? 0);
  const rows = virtualizer.getVirtualItems();
  const top = Math.max(0, (rows[0]?.start ?? 40) - 40);
  const bottom = Math.max(0, virtualizer.getTotalSize() - ((rows.at(-1)?.end ?? 40) - 40));
  const widths = channelVisible ? channelWidths : standardWidths;
  const colSpan = widths.length;
  const date = new Intl.DateTimeFormat(i18n.language.startsWith('zh') ? 'zh-CN' : 'en-GB', {
    timeZone: meta.timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  });
  return (
    <Table className='table-fixed [&_td]:whitespace-nowrap' style={{ minWidth: widths.reduce((total, width) => total + width, 0) }}>
      <colgroup>
        {widths.map((width, column) => <col key={column} style={{ width }} />)}
      </colgroup>
      <TableHeader className='sticky top-0 z-10 bg-[var(--table-header)]'>
        <TableRow>
          <TableHead className='min-w-[145px]'>{t('common.columns.id')}</TableHead>
          {channelVisible && <TableHead>{t('requests.columns.channel')}</TableHead>}
          <TableHead>{t('requests.columns.model')}</TableHead>
          <TableHead>{t('requests.columns.tokens')}</TableHead>
          <TableHead>{t('requests.columns.cache')}</TableHead>
          <TableHead>{t('requests.columns.cost')}</TableHead>
          <TableHead>{t('requests.columns.duration')}</TableHead>
          <TableHead>{t('requests.columns.caller')}</TableHead>
          <TableHead>{t('selfUsage.requests.createdAt', { timezone: meta.timezone })}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {top > 0 && (
          <tr aria-hidden='true'>
            <td colSpan={colSpan} style={{ height: top, padding: 0 }} />
          </tr>
        )}
        {rows.map((row) => {
          const item = items[row.index];
          return (
            <TableRow
              key={item.id}
              data-index={row.index}
              data-request-id={item.id}
              ref={virtualizer.measureElement}
              className={cn(
                '[&>td]:border-b [&>td]:border-[var(--table-border)] [&>td]:py-3',
                animate && row.index === 0 && 'motion-safe:animate-in motion-safe:fade-in-0 duration-200'
              )}
            >
              <TableCell>
                <RequestLogStatusCell
                  onSessionFilter={onSessionFilter}
                  retryCount={item.retryCount}
                  request={{
                    clientSessionID: item.clientSessionID,
                    id: String(item.id),
                    stream: item.stream,
                    status: item.requestStatus,
                    clientStatusCode: item.clientStatusCode,
                  }}
                />
              </TableCell>
              {channelVisible && (
                <TableCell>
                  <RequestChannelDisplayCell
                    channel={
                      item.channel && {
                        ...item.channel,
                        provider:
                          item.channel.provider ||
                          (item.channel.channelType
                            ? getProviderFromChannelType(item.channel.channelType) || item.channel.channelType
                            : undefined),
                        multiplier: item.channel.multiplierMixed ? 'mixed' : item.channel.multiplier,
                      }
                    }
                  />
                </TableCell>
              )}
              <TableCell>
                <RequestModelCell
                  displayExecution={{
                    outboundServiceTier: item.outboundServiceTier,
                    resultUnconfirmed: item.resultUnconfirmed,
                    format: item.outboundFormat,
                    reasoningEffort: item.reasoningEffort,
                    reasoningEvidence: item.reasoningEvidence,
                  }}
                  request={{
                    modelID: item.model,
                    status: item.requestStatus,
                    reasoningEffort: item.reasoningEffort,
                    format: item.format,
                    executions: {
                      edges: item.modelExecutions.map((node) => ({ node })),
                    },
                    oneHourPromptCacheUsageLogs: { totalCount: item.cacheWriteTokens1h > 0 ? 1 : 0 },
                  }}
                />
              </TableCell>
              <TableCell>
                {item.usageRecords === 0 ? (
                  '—'
                ) : (
                  <TokensCell
                    prompt={item.inputTokens}
                    completion={item.outputTokens}
                    reasoning={item.reasoningTokens}
                    cached={item.cacheReadTokens}
                    writeCached={item.cacheWriteTokens}
                  />
                )}
              </TableCell>
              <TableCell>
                <RequestCacheCell
                  request={{
                    usageSummary:
                      item.usageRecords === 0
                        ? null
                        : {
                            promptTokens: item.inputTokens,
                            promptCachedTokens: item.cacheReadTokens,
                            promptWriteCachedTokens: item.cacheWriteTokens,
                            promptWriteCachedTokens5m: item.cacheWriteTokens5m,
                            promptWriteCachedTokens1h: item.cacheWriteTokens1h,
                          },
                  }}
                />
              </TableCell>
              <TableCell>
                {item.usageRecords === 0 ? (
                  '—'
                ) : (
                  <CostCell
                    total={item.cost}
                    currencyCode={meta.currencyCode}
                    breakdown={{
                      cacheWriteTokens5m: item.cacheWriteTokens5m,
                      cacheWriteTokens1h: item.cacheWriteTokens1h,
                      items: item.costItems.map((costItem) => ({
                        pricingEvidence: costItem.pricingEvidence,
                        image: costItem.image,
                        imageRule: costItem.imageRule,
                        unpriced: costItem.unpriced,
                        itemCode: costItem.itemCode,
                        variant: costItem.promptWriteCacheVariantCode,
                        quantity: costItem.quantity,
                        subtotal: costItem.subtotal,
                        tiers: costItem.tierBreakdown,
                      })),
                      multiplier: item.costMultiplierMixed ? 'mixed' : (item.costMultiplier ?? 1),
                      unpricedRecords: item.unpricedRecords,
                    }}
                  />
                )}
              </TableCell>
              <TableCell>
                <RequestDurationCell
                  request={{
                    stream: item.stream,
                    metricsLatencyMs: item.latencyMs,
                    metricsFirstTokenLatencyMs: item.firstTokenLatencyMs,
                    streamTiming: item.streamTiming,
                    outputTokensPerSecond: item.outputTokensPerSecond,
                    outputRate: item.outputRate,
                    outputBatches: item.outputBatches,
                  }}
                />
              </TableCell>
              <TableCell>
                <div className='flex min-w-[120px] flex-col gap-0.5 text-xs'>
                  <span className='text-primary max-w-[160px] truncate font-medium' title={item.callerGroupName ?? undefined}>
                    {item.callerGroupName || t('requests.filters.ungrouped')}
                  </span>
                  <span className='text-muted-foreground max-w-[160px] truncate' title={item.callerKeyName}>
                    {item.callerKeyName}
                  </span>
                </div>
              </TableCell>
              <TableCell className='text-xs whitespace-nowrap'>{date.format(new Date(item.createdAt))}</TableCell>
            </TableRow>
          );
        })}
        {bottom > 0 && (
          <tr aria-hidden='true'>
            <td colSpan={colSpan} style={{ height: bottom, padding: 0 }} />
          </tr>
        )}
        {!items.length && (
          <TableRow>
            <TableCell colSpan={channelVisible ? 9 : 8}>{t('selfUsage.requests.empty')}</TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}
