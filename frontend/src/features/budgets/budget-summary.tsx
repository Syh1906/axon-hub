import { createContext, useContext, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useGeneralSettings } from '@/features/system/data/system';
import { useCurrencyDisplayDecimals } from '@/hooks/use-currency-display';
import { currencyFractionDigits } from '@/utils/currency-display';
import { BudgetTime } from './budget-table';
import { useBudget, useBudgetSummaries, type BudgetTarget } from './data';

const BudgetSummariesContext = createContext<ReturnType<typeof useBudgetSummaries> | null>(null);

export function BudgetSummariesProvider({ ownerIDs, children }: { ownerIDs: number[]; children: ReactNode }) {
  const summaries = useBudgetSummaries(ownerIDs, true);
  return <BudgetSummariesContext.Provider value={summaries}>{children}</BudgetSummariesContext.Provider>;
}

export function BudgetSummary({ target }: { target: BudgetTarget }) {
  const { t, i18n } = useTranslation();
  const currencyDisplayDecimals = useCurrencyDisplayDecimals();
  const summaries = useContext(BudgetSummariesContext);
  const own = useBudget(target, !summaries);
  const group = useBudget({ kind: 'group', ownerID: target.groupID ?? 0, name: '' }, !summaries && !!target.groupID);
  const { data: settings } = useGeneralSettings();
  if (summaries ? summaries.isPending : own.isPending || (target.groupID && group.isPending)) return <span>{t('budgets.loading')}</span>;
  if (summaries ? summaries.error : own.error || group.error) return <span className='text-destructive'>{t('budgets.reload')}</span>;
  const views = summaries
    ? (summaries.data ?? []).filter(
        (view) => (view.ownerKind === target.kind && view.ownerID === target.ownerID) || (view.ownerKind === 'group' && view.ownerID === target.groupID)
      )
    : [own.data, group.data].filter((v) => v !== undefined);
  const expired = views.find((v) => v.expired);
  const limited = views
    .flatMap((v) => v.windows.filter((w) => Number(w.limit) > 0).map((w) => ({ view: v, window: w })))
    .sort((a, b) => Number(a.window.available) - Number(b.window.available));
  const nearest = views.filter((v) => v.expiresAt).sort((a, b) => Date.parse(a.expiresAt!) - Date.parse(b.expiresAt!))[0];
  const first = limited[0];
  let label = t('budgets.notConfigured');
  if (expired) label = (expired.ownerKind === 'group' ? t('budgets.groupBudget') + ' · ' : '') + t('budgets.expired');
  else if (first) {
    const prefix =
      (first.view.ownerKind === 'group' ? t('budgets.groupBudget') + ' · ' : '') + t('budgets.periods.' + first.window.period) + ' ';
    label =
      prefix +
      (Number(first.window.available) <= 0
        ? t('budgets.exhausted')
        : t('budgets.available') +
          ' ' +
          t('currencies.format', {
            val: Number(first.window.available),
            currency: settings?.currencyCode || 'USD',
            locale: i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US',
            ...currencyFractionDigits(currencyDisplayDecimals, 6, 8),
          }));
  }
  return (
    <span className='block max-w-56 text-center'>
      <span className={expired || (first && Number(first.window.available) <= 0) ? 'text-destructive block truncate' : 'block truncate'}>
        {label} ›
      </span>
      <span className='text-muted-foreground block text-xs'>
        {nearest ? (
          <>
            <BudgetTime value={nearest.expiresAt} timezone={nearest.timezone} /> {t('budgets.expiresAt')}
          </>
        ) : (
          t('budgets.neverExpires')
        )}
      </span>
    </span>
  );
}
