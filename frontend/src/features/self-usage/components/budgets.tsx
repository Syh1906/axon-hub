import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { BudgetTable, BudgetTime } from '@/features/budgets/budget-table';
import { hasBudgetLimits } from '@/features/budgets/data';
import { fetchBudgets, SelfUsageError, type SelfUsageMeta } from '../api';
import { PageError } from './page-error';

export function SelfUsageBudgets({
  keyValue,
  meta,
  onUnauthorized,
}: {
  keyValue: string;
  meta: SelfUsageMeta;
  onUnauthorized: () => void;
}) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const query = useQuery({
    queryKey: ['selfUsage', 'budgets'],
    queryFn: ({ signal }) => fetchBudgets(keyValue, signal),
    staleTime: 0,
    gcTime: 0,
    refetchInterval: 30_000,
  });
  useEffect(() => {
    if (query.error instanceof SelfUsageError && query.error.status === 401) onUnauthorized();
  }, [query.error, onUnauthorized]);
  if (query.error)
    return (
      <PageError
        error={query.error}
        retry={() => {
          void query.refetch();
        }}
      />
    );
  const views = query.data?.filter(hasBudgetLimits) ?? [];
  if (!views.length) return null;
  return (
    <section className='bg-card rounded-lg border p-4' aria-label={t('budgets.title')}>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='space-y-1'>
          <h2 className='font-semibold'>{t('budgets.title')}</h2>
          <p className='text-muted-foreground text-xs'>{t('budgets.statsIndependent')}</p>
        </div>
        <div className='flex flex-wrap items-center gap-x-6 gap-y-2 text-sm'>
          {views.map((view) => {
            const tightest = view.windows
              .filter((w) => Number(w.limit) > 0)
              .reduce((a, b) => (Number(a.available) / Number(a.limit) < Number(b.available) / Number(b.limit) ? a : b));
            return (
              <div key={view.ownerKind} className='space-y-1'>
                <div className='font-medium'>
                  {t(view.ownerKind === 'group' ? 'budgets.groupBudget' : 'budgets.keyBudget')} · {t('budgets.periods.' + tightest.period)}{' '}
                  {t('budgets.remainingPercent', { percent: Math.floor((Number(tightest.available) / Number(tightest.limit)) * 100) })}
                </div>
                {view.expiresAt && (
                  <div className={view.expired ? 'text-destructive text-xs' : 'text-muted-foreground text-xs'}>
                    {t(view.expired ? 'budgets.expired' : 'budgets.expiresAt')}{' '}
                    <BudgetTime value={view.expiresAt} timezone={view.timezone} />
                  </div>
                )}
              </div>
            );
          })}
        </div>
        <Button
          size='sm'
          variant='ghost'
          aria-expanded={expanded}
          aria-controls='self-budget-details'
          onClick={() => setExpanded(!expanded)}
        >
          {t(expanded ? 'budgets.collapse' : 'budgets.expand')}
          {expanded ? <ChevronUp className='size-4' /> : <ChevronDown className='size-4' />}
        </Button>
      </div>
      {expanded && (
        <div id='self-budget-details' className='mt-4 space-y-3 border-t pt-4'>
          <div className={views.length === 2 ? 'grid gap-4 xl:grid-cols-2' : 'grid gap-4'}>
            {views.map((view) => (
              <div key={view.ownerKind} className='rounded-lg border p-3'>
                <h3 className='mb-2 font-medium'>
                  {t(view.ownerKind === 'group' ? 'budgets.groupBudget' : 'budgets.keyBudget')}
                  {view.ownerKind === 'group' && meta.groupName ? ' · ' + meta.groupName : ''}
                </h3>
                <BudgetTable view={view} currency={meta.currencyCode} onlyLimited />
              </div>
            ))}
          </div>
          <p className='text-muted-foreground text-xs'>{t('budgets.reservedHelp')}</p>
        </div>
      )}
    </section>
  );
}
