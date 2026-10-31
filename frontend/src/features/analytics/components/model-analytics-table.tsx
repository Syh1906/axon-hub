import { Fragment, useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, ArrowUpDown, Award, ChevronDown, ChevronRight, Medal, Trophy } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { formatDuration } from '@/utils/format-duration';
import { formatNumber } from '@/utils/format-number';
import { Button } from '@/components/ui/button';
import { ExpandableTableRow } from '@/components/expandable-table-row';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import type { AnalyticsModelChannelStat, AnalyticsModelStat } from '../data/analytics';
import {
  DEFAULT_MODEL_ANALYTICS_SORT,
  getModelAnalyticsSortDirection,
  getNextModelAnalyticsSort,
  sortModelAnalyticsStats,
  type ModelAnalyticsSort,
  type ModelAnalyticsSortDirection,
  type ModelAnalyticsSortKey,
} from '../utils/model-analytics-sort';

interface ModelAnalyticsTableProps {
  data: AnalyticsModelStat[];
  isLoading: boolean;
  currencyCode: string;
}

function RankIcon({ rank }: { rank: number }) {
  if (rank === 1) return <Trophy className='h-4 w-4 text-amber-500' />;
  if (rank === 2) return <Medal className='h-4 w-4 text-slate-400' />;
  if (rank === 3) return <Award className='h-4 w-4 text-orange-500' />;
  return <span className='text-muted-foreground text-xs'>#{rank}</span>;
}

interface SortableTableHeadProps {
  label: string;
  sortKey: ModelAnalyticsSortKey;
  direction: ModelAnalyticsSortDirection | null;
  actionLabel: string;
  align?: 'left' | 'right';
  className?: string;
  onSort: (key: ModelAnalyticsSortKey) => void;
}

function SortableTableHead({ label, sortKey, direction, actionLabel, align = 'right', className, onSort }: SortableTableHeadProps) {
  return (
    <TableHead
      aria-sort={direction === 'asc' ? 'ascending' : direction === 'desc' ? 'descending' : 'none'}
      className={cn('px-1', align === 'right' && 'text-right', className)}
    >
      <Button
        type='button'
        variant='ghost'
        size='sm'
        className={cn(
          'h-auto min-h-8 w-full gap-1 px-1 py-1 font-medium whitespace-normal has-[>svg]:px-1',
          align === 'right' ? 'justify-end' : 'justify-start'
        )}
        aria-label={actionLabel}
        title={actionLabel}
        onClick={() => onSort(sortKey)}
      >
        <span className='min-w-0 leading-tight'>{label}</span>
        {direction === 'desc' ? (
          <ArrowDown className='text-muted-foreground/60 h-4 w-4' />
        ) : direction === 'asc' ? (
          <ArrowUp className='text-muted-foreground/60 h-4 w-4' />
        ) : (
          <ArrowUpDown className='text-muted-foreground/60 h-4 w-4' />
        )}
      </Button>
    </TableHead>
  );
}

export function ModelAnalyticsTable({ data, isLoading, currencyCode }: ModelAnalyticsTableProps) {
  const { t, i18n } = useTranslation();
  const [expandedModels, setExpandedModels] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<ModelAnalyticsSort>(DEFAULT_MODEL_ANALYTICS_SORT);
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';
  const sortedData = useMemo(() => sortModelAnalyticsStats(data, sort), [data, sort]);

  useEffect(() => {
    setExpandedModels((current) => {
      const available = new Set(data.map((item) => item.id));
      return new Set(Array.from(current).filter((id) => available.has(id)));
    });
  }, [data]);

  const formatCurrency = (value: number) =>
    t('currencies.format', {
      val: value,
      currency: currencyCode,
      locale,
      minimumFractionDigits: 2,
      maximumFractionDigits: 4,
    });

  const formatLatency = (value: number | null) => (value == null ? '-' : formatDuration(value));

  const formatOutputSpeed = (value: number | null) => (value == null ? '-' : `${formatNumber(value, { digits: 2 })} tok/s`);

  const toggleExpanded = (id: string) => {
    setExpandedModels((current) => {
      const next = new Set(current);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const handleSort = (key: ModelAnalyticsSortKey) => {
    setSort((current) => getNextModelAnalyticsSort(current, key));
  };

  const getSortActionLabel = (key: ModelAnalyticsSortKey, label: string) => {
    const direction = getModelAnalyticsSortDirection(sort, key);
    if (direction === 'desc') {
      return t('analytics.modelAnalytics.table.sortAscending', {
        column: label,
      });
    }
    if (direction === 'asc') {
      return t('analytics.modelAnalytics.table.sortReset', {
        column: label,
      });
    }
    return t('analytics.modelAnalytics.table.sortDescending', {
      column: label,
    });
  };

  const renderSortableHead = (key: ModelAnalyticsSortKey, label: string, align: 'left' | 'right' = 'right', className?: string) => (
    <SortableTableHead
      label={label}
      sortKey={key}
      direction={getModelAnalyticsSortDirection(sort, key)}
      actionLabel={getSortActionLabel(key, label)}
      align={align}
      className={className}
      onSort={handleSort}
    />
  );

  const renderMetrics = (item: AnalyticsModelStat | AnalyticsModelChannelStat) => (
    <>
      <TableCell className='px-1.5 text-right'>{formatNumber(item.requestCount, { digits: 2 })}</TableCell>
      <TableCell className='px-1.5 text-right'>{formatNumber(item.totalTokens, { digits: 2 })}</TableCell>
      <TableCell className='px-1.5 text-right'>{formatNumber(item.successRate, { digits: 2 })}%</TableCell>
      <TableCell className='px-1.5 text-right'>{formatNumber(item.cacheHitRate, { digits: 2 })}%</TableCell>
      <TableCell className='px-1.5 text-right'>{formatLatency(item.avgFirstTokenLatencyMs)}</TableCell>
      <TableCell className='px-1.5 text-right'>{formatOutputSpeed(item.avgOutputTokensPerSecond)}</TableCell>
      <TableCell className='px-1.5 text-right'>{formatCurrency(item.costPerMillion)}</TableCell>
      <TableCell className='px-1.5 text-right'>{formatCurrency(item.cost)}</TableCell>
    </>
  );

  if (isLoading) {
    return (
      <Card>
        <CardContent>
          <Skeleton className='h-[420px] w-full' />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardContent>
        {data.length === 0 ? (
          <div className={cn('flex h-[240px] items-center justify-center', 'text-muted-foreground text-sm')}>
            {t('analytics.modelAnalytics.table.noData')}
          </div>
        ) : (
          <div className='overflow-x-auto'>
            <Table className='min-w-[1040px] table-fixed'>
              <colgroup>
                <col className='w-[56px]' />
                <col className='w-[192px]' />
                <col className='w-[84px]' />
                <col className='w-[92px]' />
                <col className='w-[84px]' />
                <col className='w-[104px]' />
                <col className='w-[100px]' />
                <col className='w-[116px]' />
                <col className='w-[96px]' />
                <col className='w-[116px]' />
              </colgroup>
              <TableHeader>
                <TableRow>
                  <TableHead className='px-1 text-center'>{t('analytics.modelAnalytics.table.rank')}</TableHead>
                  {renderSortableHead('name', t('analytics.modelAnalytics.table.name'), 'left')}
                  {renderSortableHead('requestCount', t('analytics.modelAnalytics.table.requests'))}
                  {renderSortableHead('totalTokens', t('analytics.modelAnalytics.table.tokens'))}
                  {renderSortableHead('successRate', t('analytics.modelAnalytics.table.successRate'))}
                  {renderSortableHead('cacheHitRate', t('analytics.modelAnalytics.table.cacheHitRate'))}
                  {renderSortableHead('avgFirstTokenLatencyMs', t('analytics.modelAnalytics.table.avgTTFB'))}
                  {renderSortableHead('avgOutputTokensPerSecond', t('analytics.modelAnalytics.table.avgOutputSpeed'))}
                  {renderSortableHead('costPerMillion', t('analytics.modelAnalytics.table.costPerMillion'))}
                  {renderSortableHead('cost', t('analytics.modelAnalytics.table.cost'))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedData.map((model, index) => {
                  const rank = index + 1;
                  const isExpanded = expandedModels.has(model.id);
                  const label = isExpanded
                    ? t('analytics.modelAnalytics.table.collapse', {
                        name: model.name,
                      })
                    : t('analytics.modelAnalytics.table.expand', {
                        name: model.name,
                      });

                  return (
                    <Fragment key={model.id}>
                      <ExpandableTableRow expanded={isExpanded} onToggle={() => toggleExpanded(model.id)} className='bg-muted/20 font-medium'>
                        <TableCell className='text-center'>
                          <div className='flex items-center justify-center gap-1.5'>
                            <RankIcon rank={rank} />
                          </div>
                        </TableCell>
                        <TableCell className='max-w-96'>
                          <button
                            type='button'
                            className={cn('flex max-w-full items-center gap-2 text-left', 'hover:text-primary')}
                            aria-expanded={isExpanded}
                            aria-label={label}
                            title={label}
                            onClick={() => toggleExpanded(model.id)}
                          >
                            {isExpanded ? <ChevronDown className='h-4 w-4 shrink-0' /> : <ChevronRight className='h-4 w-4 shrink-0' />}
                            <span className='truncate'>{model.name}</span>
                          </button>
                        </TableCell>
                        {renderMetrics(model)}
                      </ExpandableTableRow>
                      {isExpanded &&
                        model.channels.map((channel) => (
                          <TableRow key={`${model.id}-${channel.id}`} className='bg-background'>
                            <TableCell />
                            <TableCell className='max-w-96 pl-12'>
                              <span className='block truncate'>{channel.name}</span>
                            </TableCell>
                            {renderMetrics(channel)}
                          </TableRow>
                        ))}
                    </Fragment>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
