import { useTranslation } from 'react-i18next';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { CostCell, TokensCell } from '@/components/request-usage-cells';
import { RequestCacheCell, RequestDurationCell, RequestLogStatusCell } from '@/features/requests/components/request-log-cells';
import { RequestModelCell } from '@/features/requests/components/request-model-cell';
import type { SelfUsageMeta, UsageRequest } from '../api';

export function UsageRequestTable({ items, meta }: { items: UsageRequest[]; meta: SelfUsageMeta }) {
  const { t, i18n } = useTranslation();
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
    <div className='overflow-x-auto'>
      <Table>
        <TableHeader className='bg-[var(--table-header)]'>
          <TableRow>
            <TableHead>{t('common.columns.id')}</TableHead>
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
          {items.map((item) => (
            <TableRow key={item.id} className='[&>td]:border-b [&>td]:border-[var(--table-border)] [&>td]:py-3'>
              <TableCell>
                <RequestLogStatusCell
                  request={{
                    id: String(item.id),
                    stream: item.stream,
                    status: item.requestStatus,
                    clientStatusCode: item.clientStatusCode,
                  }}
                />
                {item.retryCount > 0 && (
                  <span className='text-muted-foreground text-xs'>{t('requests.diagnostics.retries', { count: item.retryCount })}</span>
                )}
              </TableCell>
              <TableCell>
                <RequestModelCell
                  request={{
                    modelID: item.model,
                    reasoningEffort: item.reasoningEffort,
                    format: item.format,
                    executions: {
                      edges: (item.executedModels.length ? item.executedModels : ['']).map((modelID) => ({
                        node: {
                          modelID,
                          format: item.outboundFormat ?? undefined,
                          passThroughApplied: item.passThroughApplied,
                        },
                      })),
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
                    usageLogs:
                      item.usageRecords === 0
                        ? null
                        : {
                            edges: [
                              {
                                node: {
                                  promptTokens: item.inputTokens,
                                  promptCachedTokens: item.cacheReadTokens,
                                  promptWriteCachedTokens: item.cacheWriteTokens,
                                  promptWriteCachedTokens5m: item.cacheWriteTokens5m,
                                  promptWriteCachedTokens1h: item.cacheWriteTokens1h,
                                },
                              },
                            ],
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
                    outputTokensPerSecond: item.outputTokensPerSecond,
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
          ))}
          {!items.length && (
            <TableRow>
              <TableCell colSpan={8}>{t('selfUsage.requests.empty')}</TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </div>
  );
}
