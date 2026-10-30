import { Fragment, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronDown, ChevronRight, CornerDownRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { formatNumber } from '@/utils/format-number';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import type { ConsumptionDimension, ConsumptionEntry, ConsumptionMetric, ConsumptionMetrics } from '../data/consumption';

type MetricKey = keyof ConsumptionMetrics;
type Column = {
  key: MetricKey;
  format: 'number' | 'tokens' | 'currency' | 'percent' | 'ms' | 'speed' | 'coefficient';
  hideInModel?: boolean;
  tooltip?: string;
};

function columnsFor(dimension: ConsumptionDimension, metric: ConsumptionMetric): Column[] {
  if (metric === 'CACHE') {
    return [
      { key: 'cacheRequests', format: 'number', tooltip: 'cacheRequestsHelp' },
      { key: 'cacheHitRate', format: 'percent', tooltip: 'cacheHitRateHelp' },
      ...(dimension === 'CHANNEL' ? [{ key: 'cacheCoefficient', format: 'coefficient', tooltip: 'cacheCoefficientHelp' } as Column] : []),
      { key: 'cacheReadTokens', format: 'tokens' },
      { key: 'cacheInputTokens', format: 'tokens' },
      ...(dimension !== 'CHANNEL' ? [{ key: 'totalCost', format: 'currency', hideInModel: true } as Column] : []),
    ];
  }
  if (dimension !== 'CHANNEL')
    return [
      { key: 'requestCount', format: 'number' },
      { key: 'totalTokens', format: 'tokens' },
      { key: 'cacheHitRate', format: 'percent', tooltip: 'cacheHitRateHelp' },
      { key: 'totalCost', format: 'currency' },
    ];
  return [
    { key: 'requestCount', format: 'number' },
    { key: 'totalCost', format: 'currency' },
    { key: 'totalTokens', format: 'tokens' },
    { key: 'successRate', format: 'percent', tooltip: 'successRateHelp' },
    { key: 'avgFirstTokenMs', format: 'ms', tooltip: 'firstTokenHelp' },
    { key: 'avgTokensPerSecond', format: 'speed', tooltip: 'speedHelp' },
    { key: 'avgCostPerRequest', format: 'currency' },
    { key: 'avgCostPerMillionTokens', format: 'currency' },
    { key: 'cacheCoefficient', format: 'coefficient', tooltip: 'cacheCoefficientHelp' },
  ];
}

export function ConsumptionTable({
  entries,
  dimension,
  metric,
  currencyCode,
  expandable = true,
}: {
  entries: ConsumptionEntry[];
  dimension: ConsumptionDimension;
  metric: ConsumptionMetric;
  currencyCode: string;
  expandable?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [sort, setSort] = useState<{ key: MetricKey; direction: 'asc' | 'desc' } | null>(null);
  const columns = columnsFor(dimension, metric);
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
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const formatMetric = (value: number | null, column: Column) => {
    if (value === null) return '—';
    switch (column.format) {
      case 'currency':
        return t('currencies.format', { val: value, currency: currencyCode, locale, minimumFractionDigits: 4, maximumFractionDigits: 6 });
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
        title={metrics[column.key] === null ? t('consumption.noSample') : undefined}
      >
        {child && column.hideInModel ? '—' : formatMetric(metrics[column.key], column)}
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
        'consumption-table border-separate border-spacing-0',
        dimension === 'CHANNEL' && metric === 'COST' ? 'min-w-[1200px]' : 'min-w-[720px]'
      )}
    >
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
        {sorted.map((entry, index) => {
          const hasModels = expandable && entry.models.length > 0;
          const isExpanded = hasModels && expanded.has(entry.id);
          return (
            <Fragment key={entry.id}>
              <TableRow className={cn('consumption-parent-row', isExpanded && 'consumption-parent-open')}>
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
                <TableCell className='text-center font-medium'>
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
                    <span className='mx-auto block max-w-72 truncate' title={entry.name}>
                      {entry.name}
                    </span>
                  )}
                </TableCell>
                {metricCells(entry.metrics)}
              </TableRow>
              {isExpanded &&
                entry.models.map((model, modelIndex) => (
                  <TableRow
                    key={`${entry.id}:${model.model}`}
                    className={cn('consumption-model-row', modelIndex % 2 === 1 && 'consumption-model-alternate')}
                  >
                    <TableCell aria-hidden />
                    <TableCell className='text-center font-medium'>
                      <div className='px-6'>
                        <span className='relative inline-block max-w-full align-middle'>
                          <CornerDownRight
                            className='text-muted-foreground absolute top-1/2 right-full mr-3 size-4 -translate-y-1/2'
                            aria-hidden
                          />
                          <span className='block max-w-72 truncate' title={model.model}>
                            {model.model}
                          </span>
                        </span>
                      </div>
                    </TableCell>
                    {metricCells(model.metrics, true)}
                  </TableRow>
                ))}
            </Fragment>
          );
        })}
      </TableBody>
    </Table>
  );
}
