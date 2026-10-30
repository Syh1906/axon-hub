import { useTranslation } from 'react-i18next';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import type { BudgetView, BudgetWindow } from './data';

export function BudgetTime({ value, timezone }: { value: string | null; timezone: string }) {
  const { i18n } = useTranslation();
  return (
    <time dateTime={value ?? undefined} title={timezone}>
      {value
        ? new Intl.DateTimeFormat(i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US', {
            timeZone: timezone,
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            hourCycle: 'h23',
          }).format(new Date(value))
        : '—'}
    </time>
  );
}
export function BudgetSchedule({ window }: { window: BudgetWindow }) {
  const { t } = useTranslation();
  return (
    <div className='text-muted-foreground space-y-1 text-xs'>
      <div>
        {t(
          `budgets.mode.${window.period === 'total' ? 'total' : window.mode === 'rolling' ? window.period + 'Rolling' : window.period + 'Fixed'}`
        )}
      </div>
      {window.effectiveSchedule && (
        <div>
          {t('budgets.restarted')}: {window.effectiveSchedule}
        </div>
      )}
      {(window.mode === 'rolling' ? window.nextReleaseAt : window.windowEnd) && (
        <div>
          {t(window.mode === 'rolling' ? 'budgets.nextRelease' : 'budgets.nextRefresh')}{' '}
          <BudgetTime value={window.mode === 'rolling' ? window.nextReleaseAt : window.windowEnd} timezone={window.timezone} />
        </div>
      )}
    </div>
  );
}
export function BudgetTable({ view, currency, onlyLimited = false }: { view: BudgetView; currency: string; onlyLimited?: boolean }) {
  const { t, i18n } = useTranslation();
  const amount = (value: string) =>
    t('currencies.format', {
      val: Number(value),
      currency,
      locale: i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US',
      minimumFractionDigits: 6,
      maximumFractionDigits: 8,
    });
  return (
    <div className='overflow-x-auto'>
      <Table className='table-fixed text-center tabular-nums'>
        <TableHeader>
          <TableRow>
            {['period', 'usedLimit', 'reserved', 'available'].map((key) => (
              <TableHead key={key} className='text-center'>
                {t('budgets.' + key)}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {view.windows
            .filter((w) => !onlyLimited || Number(w.limit) > 0)
            .map((w) => (
              <TableRow key={w.period}>
                <TableCell className='space-y-1 text-center'>
                  <div className='font-medium'>{t('budgets.periods.' + w.period)}</div>
                  <BudgetSchedule window={w} />
                </TableCell>
                <TableCell className='text-center break-words'>
                  {amount(w.settled)}
                  <div className='text-muted-foreground text-xs'>/ {Number(w.limit) > 0 ? amount(w.limit) : t('budgets.unlimited')}</div>
                </TableCell>
                <TableCell className='text-center break-words'>{amount(w.reserved)}</TableCell>
                <TableCell className='text-center break-words'>{Number(w.limit) > 0 ? amount(w.available) : '—'}</TableCell>
              </TableRow>
            ))}
        </TableBody>
      </Table>
    </div>
  );
}
