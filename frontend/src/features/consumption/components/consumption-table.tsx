import { useCallback, useMemo, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronDown, ChevronRight, CornerDownRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { formatNumber } from '@/utils/format-number';
import { currencyFractionDigits } from '@/utils/currency-display';
import { useCurrencyDisplayDecimals } from '@/hooks/use-currency-display';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ExpandableTableRow } from '@/components/expandable-table-row';
import type { ConsumptionDimension, ConsumptionEntry, ConsumptionMetric, ConsumptionMetrics } from '../data/consumption';

type MetricKey = keyof ConsumptionMetrics;
type Column = {
  key: MetricKey;
  format: 'number' | 'tokens' | 'currency' | 'percent' | 'ms' | 'speed' | 'coefficient';
  hideInModel?: boolean;
  tooltip?: string;
};

function columnsFor(dimension: ConsumptionDimension, metric: ConsumptionMetric, view: 'leaderboard' | 'usage'): Column[] {
  if (metric === 'CACHE') {
    return [
      { key: 'cacheRequests', format: 'number', tooltip: 'cacheRequestsHelp' },
      { key: 'cacheHitRate', format: 'percent', tooltip: 'cacheHitRateHelp' },
      ...(dimension === 'CHANNEL' ? [{ key: 'cacheCoefficient', format: 'coefficient', tooltip: 'cacheCoefficientHelp' } as Column] : []),
      { key: 'cacheReadTokens', format: 'tokens' },
      { key: 'cacheInputTokens', format: 'tokens' },
      ...(dimension === 'MODEL'
        ? ([
            { key: 'avgFirstTokenMs', format: 'ms', tooltip: 'firstTokenHelp' },
            { key: 'avgTokensPerSecond', format: 'speed', tooltip: 'speedHelp' },
          ] as Column[])
        : []),
      { key: 'totalCost', format: 'currency', hideInModel: dimension !== 'CHANNEL', tooltip: 'totalCostHelp' },
    ];
  }
  if (dimension === 'MODEL')
    return [
      { key: 'requestCount', format: 'number' },
      { key: 'totalCost', format: 'currency', tooltip: 'totalCostHelp' },
      { key: 'totalTokens', format: 'tokens' },
      { key: 'successRate', format: 'percent', tooltip: 'successRateHelp' },
      { key: 'avgFirstTokenMs', format: 'ms', tooltip: 'firstTokenHelp' },
      { key: 'avgTokensPerSecond', format: 'speed', tooltip: 'speedHelp' },
      { key: 'cacheHitRate', format: 'percent', tooltip: 'overallCacheHitRateHelp' },
    ];
  if (dimension !== 'CHANNEL') {
    const costColumns: Column[] = [
      { key: 'totalCost', format: 'currency' },
      { key: 'avgCostPerMillionTokens', format: 'currency', tooltip: 'avgCostPerMillionTokensHelp' },
    ];
    if (view === 'usage') costColumns.reverse();
    return [
      { key: 'requestCount', format: 'number' },
      { key: 'totalTokens', format: 'tokens' },
      { key: 'cacheHitRate', format: 'percent', tooltip: 'overallCacheHitRateHelp' },
      ...costColumns,
    ];
  }
  return [
    { key: 'requestCount', format: 'number' },
    { key: 'totalCost', format: 'currency' },
    { key: 'totalTokens', format: 'tokens' },
    { key: 'successRate', format: 'percent', tooltip: 'channelSuccessRateHelp' },
    { key: 'avgFirstTokenMs', format: 'ms', tooltip: 'firstTokenHelp' },
    { key: 'avgTokensPerSecond', format: 'speed', tooltip: 'speedHelp' },
    { key: 'avgCostPerRequest', format: 'currency' },
    { key: 'avgCostPerMillionTokens', format: 'currency', tooltip: 'avgCostPerMillionTokensHelp' },
    { key: 'cacheCoefficient', format: 'coefficient', tooltip: 'cacheCoefficientHelp' },
    { key: 'cacheHitRate', format: 'percent', tooltip: 'overallCacheHitRateHelp' },
  ];
}

export function ConsumptionTable({
  entries,
  dimension,
  metric,
  currencyCode,
  expandable = true,
  scrollElement,
  view = 'leaderboard',
}: {
  entries: ConsumptionEntry[];
  dimension: ConsumptionDimension;
  metric: ConsumptionMetric;
  currencyCode: string;
  expandable?: boolean;
  scrollElement: HTMLDivElement | null;
  view?: 'leaderboard' | 'usage';
}) {
  const { t, i18n } = useTranslation();
  const currencyDisplayDecimals = useCurrencyDisplayDecimals();
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [sort, setSort] = useState<{ key: MetricKey; direction: 'asc' | 'desc' } | null>(null);
  const columns = useMemo(() => columnsFor(dimension, metric, view), [dimension, metric, view]);
  const visibleSort = sort && columns.some((column) => column.key === sort.key) ? sort : null;
  const sorted = useMemo(() => {
    if (!visibleSort) return entries;
    const { key, direction } = visibleSort;
    return [...entries].sort((a, b) => {
      const left = a.metrics[key],
        right = b.metrics[key];
      if (left === null && right === null) return 0;
      if (left === null) return 1;
      if (right === null) return -1;
      return (left - right) * (direction === 'asc' ? 1 : -1);
    });
  }, [entries, visibleSort]);
  // Flatten expanded models so a single large entity cannot mount an unbounded subtree.
  const rows = useMemo(
    () =>
      sorted.flatMap((entry, rank) => [
        { key: entry.id, entry, rank, model: null as ConsumptionEntry['models'][number] | null },
        ...(expandable && expanded.has(entry.id)
          ? entry.models.map((model) => ({ key: `${entry.id}:${model.model}`, entry, rank, model }))
          : []),
      ]),
    [sorted, expanded, expandable]
  );
  const getItemKey = useCallback((index: number) => rows[index].key, [rows]);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollElement,
    getItemKey,
    estimateSize: () => 64,
    overscan: 6,
    scrollMargin: 58,
    useFlushSync: false,
  });
  const items = virtualizer.getVirtualItems();
  const top = Math.max(0, (items[0]?.start ?? 58) - 58);
  const bottom = Math.max(0, virtualizer.getTotalSize() - ((items.at(-1)?.end ?? 58) - 58));
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const formatMetric = (value: number | null, column: Column) => {
    if (value === null) return '—';
    switch (column.format) {
      case 'currency':
        return t('currencies.format', { val: value, currency: currencyCode, locale, ...currencyFractionDigits(currencyDisplayDecimals, 4, 6) });
      case 'percent':
        return `${(value * 100).toFixed(1)}%`;
      case 'ms':
        return `${Math.round(value).toLocaleString(locale)} ms`;
      case 'speed':
        return `${value.toFixed(1)} tok/s`;
      case 'coefficient':
        return (value / 10_000).toFixed(2);
      case 'tokens':
        return formatNumber(value);
      default:
        return value.toLocaleString(locale);
    }
  };
  const metricCells = (metrics: ConsumptionMetrics, child = false) =>
    columns.map((column) => (
      <TableCell
        key={column.key}
        className='text-center tabular-nums'
        title={
          column.key === 'avgTokensPerSecond' ? t('consumption.speedSamples', { count: metrics.speedSampleCount }) : undefined
        }
      >
        {child && column.hideInModel ? (
          '—'
        ) : column.key === 'totalCost' && metrics.unpricedRequestCount > 0 && metrics.pricedRequestCount === 0 && metrics.totalCost === 0 ? (
          '—'
        ) : (
          formatMetric(metrics[column.key], column)
        )}
      </TableCell>
    ));
  const toggle = (id: string) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  return (
    <Table
      className={cn(
        'consumption-table table-fixed border-separate border-spacing-0',
        dimension === 'CHANNEL' && metric === 'COST' ? 'min-w-[1600px]' : dimension === 'MODEL' ? 'min-w-[1150px]' : 'min-w-[900px]'
      )}
    >
      <colgroup>
        <col style={{ width: 56 }} />
        <col style={{ width: dimension === 'MODEL' ? 240 : dimension === 'CHANNEL' ? 280 : 380 }} />
        {columns.map((column) => (
          <col key={column.key} />
        ))}
      </colgroup>
      <TableHeader className='sticky top-0 z-10 bg-[var(--table-header)]'>
        <TableRow>
          <TableHead className='w-14 text-center'>{t('consumption.rank')}</TableHead>
          <TableHead className='min-w-52 text-center'>
            {t(`consumption.${expandable ? 'entityColumns' : 'dimensions'}.${dimension}`)}
          </TableHead>
          {columns.map((column) => (
            <TableHead
              key={column.key}
              className='text-center'
              aria-sort={visibleSort?.key === column.key ? (visibleSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}
            >
              <Button
                variant='ghost'
                size='sm'
                className='h-auto w-full justify-center px-4 py-2 text-xs whitespace-normal'
                title={column.tooltip ? t(`consumption.${column.tooltip}`) : undefined}
                onClick={() =>
                  setSort((current) => ({
                    key: column.key,
                    direction: current?.key === column.key && current.direction === 'desc' ? 'asc' : 'desc',
                  }))
                }
              >
                <span className='relative'>
                  {t(`consumption.columns.${column.key}`)}
                  <span className='absolute top-1/2 left-full ml-1 -translate-y-1/2' aria-hidden>
                    {visibleSort?.key === column.key ? (
                      visibleSort.direction === 'asc' ? (
                        <ArrowUp data-icon='inline-end' />
                      ) : (
                        <ArrowDown data-icon='inline-end' />
                      )
                    ) : (
                      <ArrowUpDown data-icon='inline-end' className='opacity-35' />
                    )}
                  </span>
                </span>
              </Button>
            </TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {top > 0 && (
          <tr aria-hidden='true'>
            <td colSpan={columns.length + 2} style={{ height: top, padding: 0, border: 0 }} />
          </tr>
        )}
        {items.map((item) => {
          const { entry, rank: index, model } = rows[item.index];
          if (model)
            return (
              <TableRow key={item.key} ref={virtualizer.measureElement} data-index={item.index} className='consumption-model-row'>
                <TableCell aria-hidden />
                <TableCell className='text-center font-medium'>
                  <div className='flex min-w-0 items-center justify-center gap-3 px-6'>
                    <CornerDownRight className='text-muted-foreground size-4 shrink-0' aria-hidden />
                    <span className='truncate' title={model.model}>
                      {model.model}
                    </span>
                  </div>
                </TableCell>
                {metricCells(model.metrics, true)}
              </TableRow>
            );
          const hasModels = expandable && entry.models.length > 0;
          const isExpanded = hasModels && expanded.has(entry.id);
          return (
            <ExpandableTableRow
              key={item.key}
              ref={virtualizer.measureElement}
              data-index={item.index}
              expanded={isExpanded}
              onToggle={hasModels ? () => toggle(entry.id) : undefined}
              className={cn('consumption-parent-row', isExpanded && 'consumption-parent-open')}
            >
              <TableCell className='text-muted-foreground text-center'>
                <span
                  className={cn(
                    'inline-flex size-6 items-center justify-center rounded-md',
                    index === 0 && 'border border-amber-400/50 bg-amber-400/25 font-semibold text-amber-700 dark:text-amber-300',
                    index === 1 && 'border border-slate-400/40 bg-slate-300/25 font-semibold text-slate-600 dark:text-slate-300',
                    index === 2 && 'border border-orange-400/40 bg-orange-300/25 font-semibold text-orange-700 dark:text-orange-300'
                  )}
                >
                  {index + 1}
                </span>
              </TableCell>
              <TableCell className={cn('font-medium', dimension === 'MODEL' ? 'text-left' : 'text-center')}>
                {hasModels ? (
                  <div className='px-14'>
                    <Button
                      variant='ghost'
                      className='relative h-auto max-w-full justify-center px-0 py-1'
                      aria-expanded={isExpanded}
                      aria-label={t(isExpanded ? 'consumption.collapseEntity' : 'consumption.expandEntity', { name: entry.name })}
                      onClick={() => toggle(entry.id)}
                    >
                      <span className='absolute top-1/2 right-full mr-2 -translate-y-1/2' aria-hidden>
                        {isExpanded ? <ChevronDown /> : <ChevronRight />}
                      </span>
                      <span className='max-w-72 truncate' title={entry.name}>
                        {entry.name}
                      </span>
                      <span className='text-muted-foreground absolute top-1/2 left-full ml-2 -translate-y-1/2 text-xs font-normal'>
                        {t('consumption.modelCount', { count: entry.models.length })}
                      </span>
                    </Button>
                  </div>
                ) : (
                  <span className={cn('block max-w-72 truncate', dimension !== 'MODEL' && 'mx-auto')} title={entry.name}>
                    {entry.name || '—'}
                  </span>
                )}
              </TableCell>
              {metricCells(entry.metrics)}
            </ExpandableTableRow>
          );
        })}
        {bottom > 0 && (
          <tr aria-hidden='true'>
            <td colSpan={columns.length + 2} style={{ height: bottom, padding: 0, border: 0 }} />
          </tr>
        )}
      </TableBody>
    </Table>
  );
}
