import { Fragment, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Award, ChevronDown, ChevronRight, Medal, Trophy } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { ExpandableTableRow } from '@/components/expandable-table-row';
import { cn } from '@/lib/utils';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { formatNumber } from '@/utils/format-number';
import type { AnalyticsAPIKeyStat } from '../data/analytics';

interface APIKeyAnalyticsTableProps {
  data: AnalyticsAPIKeyStat[];
  isLoading: boolean;
  currencyCode: string;
}

function RankIcon({ rank }: { rank: number }) {
  if (rank === 1) return <Trophy className='h-4 w-4 text-amber-500' />;
  if (rank === 2) return <Medal className='h-4 w-4 text-slate-400' />;
  if (rank === 3) return <Award className='h-4 w-4 text-orange-500' />;
  return <span className='text-xs text-muted-foreground'>#{rank}</span>;
}

export function APIKeyAnalyticsTable({
  data,
  isLoading,
  currencyCode,
}: APIKeyAnalyticsTableProps) {
  const { t, i18n } = useTranslation();
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(new Set());
  const locale = i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US';

  useEffect(() => {
    setExpandedKeys((current) => {
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

  const toggleExpanded = (id: string) => {
    setExpandedKeys((current) => {
      const next = new Set(current);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  if (isLoading) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{t('analytics.apiKeyAnalytics.table.title')}</CardTitle>
        </CardHeader>
        <CardContent>
          <Skeleton className='h-[420px] w-full' />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('analytics.apiKeyAnalytics.table.title')}</CardTitle>
      </CardHeader>
      <CardContent>
        {data.length === 0 ? (
          <div
            className={cn(
              'flex h-[240px] items-center justify-center',
              'text-sm text-muted-foreground'
            )}
          >
            {t('analytics.apiKeyAnalytics.table.noData')}
          </div>
        ) : (
          <div className='overflow-x-auto'>
            <Table className='min-w-[720px]'>
              <TableHeader>
                <TableRow>
                  <TableHead className='w-20 text-center'>
                    {t('analytics.apiKeyAnalytics.table.rank')}
                  </TableHead>
                  <TableHead>{t('analytics.apiKeyAnalytics.table.name')}</TableHead>
                  <TableHead className='text-right'>
                    {t('analytics.apiKeyAnalytics.table.requests')}
                  </TableHead>
                  <TableHead className='text-right'>
                    {t('analytics.apiKeyAnalytics.table.tokens')}
                  </TableHead>
                  <TableHead className='text-right'>
                    {t('analytics.apiKeyAnalytics.table.cost')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.map((apiKey, index) => {
                  const rank = index + 1;
                  const isExpanded = expandedKeys.has(apiKey.id);
                  const label = isExpanded
                    ? t('analytics.apiKeyAnalytics.table.collapse', {
                        name: apiKey.name,
                      })
                    : t('analytics.apiKeyAnalytics.table.expand', {
                        name: apiKey.name,
                      });

                  return (
                    <Fragment key={apiKey.id}>
                      <ExpandableTableRow expanded={isExpanded} onToggle={() => toggleExpanded(apiKey.id)} className='bg-muted/20'>
                        <TableCell className='text-center'>
                          <div className='flex items-center justify-center gap-1.5'>
                            <RankIcon rank={rank} />
                          </div>
                        </TableCell>
                        <TableCell className='max-w-[360px]'>
                          <button
                            type='button'
                            className={cn(
                              'flex max-w-full items-center gap-2 text-left',
                              'font-medium hover:text-primary'
                            )}
                            aria-expanded={isExpanded}
                            aria-label={label}
                            title={label}
                            onClick={() => toggleExpanded(apiKey.id)}
                          >
                            {isExpanded ? (
                              <ChevronDown className='h-4 w-4 shrink-0' />
                            ) : (
                              <ChevronRight className='h-4 w-4 shrink-0' />
                            )}
                            <span className='truncate'>{apiKey.name}</span>
                          </button>
                        </TableCell>
                        <TableCell className='text-right font-medium'>
                          {formatNumber(apiKey.requestCount, { digits: 2 })}
                        </TableCell>
                        <TableCell className='text-right font-medium'>
                          {formatNumber(apiKey.totalTokens, { digits: 2 })}
                        </TableCell>
                        <TableCell className='text-right font-semibold'>
                          {formatCurrency(apiKey.cost)}
                        </TableCell>
                      </ExpandableTableRow>
                      {isExpanded &&
                        apiKey.models.map((model) => (
                          <TableRow
                            key={`${apiKey.id}-${model.id}`}
                            className='bg-background'
                          >
                            <TableCell />
                            <TableCell className='max-w-[360px] pl-12'>
                              <span className='block truncate'>{model.name}</span>
                            </TableCell>
                            <TableCell className='text-right'>
                              {formatNumber(model.requestCount, { digits: 2 })}
                            </TableCell>
                            <TableCell className='text-right'>
                              {formatNumber(model.totalTokens, { digits: 2 })}
                            </TableCell>
                            <TableCell className='text-right'>
                              {formatCurrency(model.cost)}
                            </TableCell>
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
