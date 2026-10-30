import { useState } from 'react';
import { Wallet } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useDashboardCostStats } from '../data/dashboard';

const ranges = { allTime: 'all', thisMonth: 'month', thisWeek: 'week', today: 'day' } as const;

export function CostStatsCard() {
  const { t, i18n } = useTranslation();
  const { data, isLoading, error } = useDashboardCostStats();
  const [range, setRange] = useState<keyof typeof ranges>('today');
  const period = data?.[range];
  const allUnpriced = period && period.usageCount > 0 && period.unpricedCount === period.usageCount;

  return (
    <Card className='hover-card min-w-0'>
      <CardHeader className='flex flex-wrap items-center justify-between gap-2 pb-2'>
        <div className='flex min-w-0 items-center gap-2'>
          <div className='bg-primary/10 text-primary dark:bg-primary/20 shrink-0 rounded-lg p-1.5'>
            <Wallet className='h-4 w-4' />
          </div>
          <CardTitle className='text-sm font-medium'>{t('dashboard.cards.costStats')}</CardTitle>
        </div>
        <Tabs value={range} onValueChange={(value) => setRange(value as keyof typeof ranges)}>
          <TabsList className='h-6 p-0.5'>
            {Object.entries(ranges).map(([value, label]) => (
              <TabsTrigger key={value} value={value} className='h-5 px-1.5 text-[10px]'>
                {t(`dashboard.stats.${label}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <Skeleton className='h-8 w-28' />
        ) : error ? (
          <div className='text-destructive text-sm'>{t('common.loadError')}</div>
        ) : period ? (
          <div className='space-y-1'>
            <div className='font-mono text-xl font-bold break-all'>
              {allUnpriced
                ? '—'
                : t('currencies.format', {
                    val: period.totalCost,
                    currency: data?.currencyCode,
                    locale: i18n.language === 'zh' ? 'zh-CN' : 'en-US',
                    minimumFractionDigits: 6,
                  })}
            </div>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
