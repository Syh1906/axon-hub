import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { useGeneralSettings } from '@/features/system/data/system';
import { BudgetSummary } from './budget-summary';
import { BudgetTable, BudgetTime } from './budget-table';
import {
  useBudget,
  useBudgetMutation,
  useBudgetResetPreview,
  type BudgetRule,
  type BudgetTarget,
  type BudgetView,
  type BudgetPeriod,
} from './data';
import { extendBudgetExpiry } from './expiry';

const minuteText = (minute: number) => String(Math.floor(minute / 60)).padStart(2, '0') + ':' + String(minute % 60).padStart(2, '0');
function localDate(value: string | null) {
  if (!value) return '';
  const d = new Date(value);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}
export function BudgetButton({ target, summary = false }: { target: BudgetTarget; summary?: boolean }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        type='button'
        variant={summary ? 'ghost' : 'outline'}
        className={summary ? 'h-auto min-h-10 py-1.5' : undefined}
        size='sm'
        aria-label={t('budgets.title')}
        onClick={() => setOpen(true)}
      >
        {summary ? <BudgetSummary target={target} /> : t('budgets.title')}
      </Button>
      {open && <BudgetDialog target={target} onClose={() => setOpen(false)} />}
    </>
  );
}
export function BudgetDialog({
  target,
  onClose,
  initialView = 'settings',
}: {
  target: BudgetTarget;
  onClose: () => void;
  initialView?: 'settings' | 'usage' | 'reset';
}) {
  const { t } = useTranslation();
  const query = useBudget(target);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className='flex max-h-[90vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[800px]'>
        <DialogHeader className='shrink-0 border-b px-6 py-5'>
          <DialogTitle>{t('budgets.title')}</DialogTitle>
          <DialogDescription>{target.name}</DialogDescription>
        </DialogHeader>
        {query.isPending && (
          <p className='p-6' role='status'>
            {t('budgets.loading')}
          </p>
        )}
        {query.error && (
          <div className='space-y-3 p-6' role='alert'>
            <p>{query.error.message}</p>
            <Button onClick={() => query.refetch()}>{t('budgets.reload')}</Button>
          </div>
        )}
        {query.data && (
          <BudgetForm
            key={target.kind + target.ownerID + query.data.revision}
            target={target}
            view={query.data}
            onClose={onClose}
            initialView={initialView}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
function BudgetForm({
  target,
  view,
  onClose,
  initialView,
}: {
  target: BudgetTarget;
  view: BudgetView;
  onClose: () => void;
  initialView: 'settings' | 'usage' | 'reset';
}) {
  const { t } = useTranslation();
  const { apiKeyPermissions, hasProjectScope } = usePermissions();
  const writable = apiKeyPermissions.canWrite && hasProjectScope('write_users') && hasProjectScope('write_roles');
  const [rules, setRules] = useState<BudgetRule[]>(() =>
    view.windows.map((w) => ({
      period: w.period,
      mode: w.pendingRule?.mode ?? w.mode,
      limit: w.limit,
      minute: w.pendingRule?.minute ?? w.minute,
    }))
  );
  const [enabled, setEnabled] = useState(() => view.windows.map((w) => Number(w.limit) > 0));
  const [timezone, setTimezone] = useState(view.timezone);
  const [expiry, setExpiry] = useState(localDate(view.expiresAt));
  const [details, setDetails] = useState(initialView === 'usage');
  const [reset, setReset] = useState(initialView === 'reset');
  const [operationID] = useState(() => crypto.randomUUID());
  const mutation = useBudgetMutation(target);
  const patch = (index: number, change: Partial<BudgetRule>) =>
    setRules((values) => values.map((value, i) => (i === index ? { ...value, ...change } : value)));
  const { data: settings } = useGeneralSettings();
  const currency = settings?.currencyCode || 'USD';
  async function save() {
    try {
      new Intl.DateTimeFormat('en', { timeZone: timezone });
      if (rules.some((r, i) => enabled[i] && (!/^\d+(\.\d{1,8})?$/.test(r.limit) || Number(r.limit) <= 0))) {
        toast.error(t('budgets.invalidAmount'));
        return;
      }
      await mutation.mutateAsync({
        rules: rules.map((r, i) => ({ ...r, limit: enabled[i] ? r.limit : '0' })),
        timezone,
        expiresAt: expiry ? new Date(expiry).toISOString() : null,
        revision: view.revision,
        operationID,
      });
      toast.success(t('budgets.saved'));
      onClose();
    } catch (error) {
      toast.error((error as Error).message);
    }
  }
  return (
    <>
      <div className='min-h-0 space-y-5 overflow-y-auto px-6 py-5'>
        <p className='bg-muted rounded-md p-3 text-sm'>{t(target.kind === 'group' ? 'budgets.groupHelp' : 'budgets.keyHelp')}</p>
        {!view.accountingStartedAt && <p className='text-muted-foreground text-xs'>{t('budgets.firstActivation')}</p>}
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <h3 className='font-semibold'>{t('budgets.amountLimits')}</h3>
          <span className='text-muted-foreground text-sm'>{currency}</span>
        </div>
        <div className='overflow-x-auto rounded-lg border'>
          <div className='min-w-[580px]'>
            <div className='bg-muted grid grid-cols-[64px_80px_minmax(140px,1fr)_minmax(210px,1.4fr)] items-center gap-3 px-3 py-2 text-center text-sm'>
              {['enabled', 'period', 'limit', 'refreshMode'].map((k) => (
                <span key={k}>{t('budgets.' + k)}</span>
              ))}
            </div>
            {rules.map((r, index) => (
              <div
                key={r.period}
                className='grid grid-cols-[64px_80px_minmax(140px,1fr)_minmax(210px,1.4fr)] items-center gap-3 border-t px-3 py-3 text-center text-sm'
              >
                <Switch
                  className='mx-auto'
                  checked={enabled[index]}
                  disabled={!writable || mutation.isPending}
                  aria-label={t('budgets.enablePeriod', { period: t('budgets.periods.' + r.period) })}
                  onCheckedChange={(value) => setEnabled((values) => values.map((old, i) => (i === index ? value : old)))}
                />
                <span className='font-medium'>{t('budgets.periods.' + r.period)}</span>
                <Input
                  className='text-center tabular-nums'
                  inputMode='decimal'
                  aria-label={t('budgets.limitPeriod', { period: t('budgets.periods.' + r.period) })}
                  value={r.limit}
                  disabled={!enabled[index] || !writable || mutation.isPending}
                  onChange={(e) => patch(index, { limit: e.target.value })}
                />
                <div className='space-y-1'>
                  {r.period === 'total' ? (
                    <span className='text-muted-foreground'>{t('budgets.mode.total')}</span>
                  ) : r.period === 'weekly' || r.period === 'monthly' ? (
                    <div
                      className='grid grid-cols-2 rounded-md border p-0.5'
                      role='group'
                      aria-label={t('budgets.periods.' + r.period) + ' ' + t('budgets.refreshMode')}
                    >
                      {(['fixed', 'rolling'] as const).map((mode) => (
                        <Button
                          key={mode}
                          variant={r.mode === mode ? 'default' : 'ghost'}
                          size='sm'
                          className='px-2'
                          aria-pressed={r.mode === mode}
                          disabled={!writable || mutation.isPending}
                          onClick={() => patch(index, { mode })}
                        >
                          {t('budgets.mode.' + r.period + (mode === 'fixed' ? 'Fixed' : 'Rolling'))}
                        </Button>
                      ))}
                    </div>
                  ) : (
                    <div className='flex items-center justify-center gap-2'>
                      <Select
                        value={r.mode}
                        disabled={!writable || mutation.isPending}
                        onValueChange={(value) => patch(index, { mode: value as BudgetRule['mode'] })}
                      >
                        <SelectTrigger className='justify-center gap-2'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {(['fixed', 'rolling'] as const).map((mode) => (
                            <SelectItem key={mode} value={mode}>
                              {t('budgets.mode.' + r.period + (mode === 'fixed' ? 'Fixed' : 'Rolling'))}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      {r.period === 'daily' && r.mode === 'fixed' && (
                        <Input
                          type='time'
                          className='w-28 shrink-0 text-center'
                          aria-label={t('budgets.refreshTime')}
                          disabled={!writable}
                          value={minuteText(r.minute)}
                          onChange={(e) => {
                            const [h, m] = e.target.value.split(':').map(Number);
                            patch(index, { minute: h * 60 + m });
                          }}
                        />
                      )}
                    </div>
                  )}
                  {view.windows[index].effectiveSchedule && (
                    <div className='text-muted-foreground text-xs'>
                      {t('budgets.restarted')}: {view.windows[index].effectiveSchedule}
                      <Button variant='link' size='sm' disabled={!writable} onClick={() => patch(index, { restoreCalendar: true })}>
                        {t(r.restoreCalendar ? 'budgets.restoreSelected' : 'budgets.restoreCalendar')}
                      </Button>
                    </div>
                  )}
                  {view.windows[index].pendingAt && (
                    <p className='text-muted-foreground text-xs'>
                      {t('budgets.pending')} <BudgetTime value={view.windows[index].pendingAt} timezone={view.windows[index].timezone} />
                    </p>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
        <p className='text-muted-foreground text-xs'>{t('budgets.windowHelp')}</p>
        <div className='grid gap-2'>
          <Label htmlFor='budget-timezone'>{t('budgets.timezone')}</Label>
          <Input id='budget-timezone' disabled={!writable} value={timezone} onChange={(e) => setTimezone(e.target.value)} />
        </div>
        <section className='space-y-3 border-t pt-4'>
          <h3 className='font-semibold'>{t('budgets.expiry')}</h3>
          <div className='flex flex-wrap items-center gap-3'>
            <Input
              className='w-auto'
              type='datetime-local'
              aria-label={t('budgets.expiry')}
              disabled={!writable}
              value={expiry}
              onChange={(e) => setExpiry(e.target.value)}
            />
            <span className='text-muted-foreground text-xs'>{Intl.DateTimeFormat().resolvedOptions().timeZone}</span>
            <label className='flex items-center gap-2 text-sm'>
              <Checkbox
                checked={!expiry}
                disabled={!writable}
                onCheckedChange={(value) => setExpiry(value ? '' : localDate(new Date(Date.now() + 30 * 86400000).toISOString()))}
              />
              {t('budgets.neverExpires')}
            </label>
          </div>
          <div className='flex flex-wrap gap-2'>
            {[7, 30, 90].map((days) => (
              <Button
                key={days}
                size='sm'
                variant='outline'
                disabled={!writable}
                onClick={() =>
                  setExpiry(
                    localDate(new Date(Math.max(Date.now(), expiry ? new Date(expiry).getTime() : 0) + days * 86400000).toISOString())
                  )
                }
              >
                {t('budgets.extendDays', { days })}
              </Button>
            ))}
            {[1, 3, 12].map((months) => (
              <Button
                key={'months-' + months}
                size='sm'
                variant='outline'
                disabled={!writable}
                onClick={() => setExpiry(localDate(extendBudgetExpiry(expiry, months).toISOString()))}
              >
                {t(months === 12 ? 'budgets.extendYear' : 'budgets.extendMonths', { months })}
              </Button>
            ))}
          </div>
        </section>
        <div className='flex items-center justify-between gap-3 border-t pt-4'>
          <Button variant='ghost' size='sm' aria-expanded={details} onClick={() => setDetails(!details)}>
            {t(details ? 'budgets.hideUsage' : 'budgets.showUsage')}
          </Button>
          <Button variant='outline' size='sm' disabled={!writable || mutation.isPending} onClick={() => setReset(true)}>
            {t('budgets.reset')}
          </Button>
        </div>
        {details && (
          <>
            <BudgetTable view={view} currency={currency} />
            <p className='text-muted-foreground text-xs'>{t('budgets.reservedHelp')}</p>
          </>
        )}
      </div>
      <DialogFooter className='shrink-0 items-center border-t px-6 py-4'>
        <span className='text-muted-foreground mr-auto text-xs'>{t('budgets.saveHelp')}</span>
        <Button variant='outline' onClick={onClose} disabled={mutation.isPending}>
          {t('common.buttons.cancel')}
        </Button>
        {writable && (
          <Button onClick={save} disabled={mutation.isPending}>
            {t('common.buttons.save')}
          </Button>
        )}
      </DialogFooter>
      {reset && writable && (
        <BudgetResetDialog
          target={target}
          view={view}
          onClose={() => {
            setReset(false);
            if (initialView === 'reset') onClose();
          }}
        />
      )}
    </>
  );
}
function BudgetResetDialog({ target, view, onClose }: { target: BudgetTarget; view: BudgetView; onClose: () => void }) {
  const { t } = useTranslation();
  const [periods, setPeriods] = useState<BudgetPeriod[]>([]);
  const [resetCycle, setResetCycle] = useState(false);
  const [operationID] = useState(() => crypto.randomUUID());
  const mutation = useBudgetMutation(target, true);
  const canRestartCycle = view.windows.some((w) => periods.includes(w.period) && w.canResetCycle);
  const restartSelectedCycle = canRestartCycle && resetCycle;
  const preview = useBudgetResetPreview(target, { periods, resetCycle: restartSelectedCycle, revision: view.revision, operationID });
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('budgets.reset')}</DialogTitle>
          <DialogDescription>{t('budgets.resetHelp')}</DialogDescription>
        </DialogHeader>
        <label className='flex items-center gap-2'>
          <Checkbox
            checked={periods.length === view.windows.length}
            onCheckedChange={(value) => setPeriods(value ? view.windows.map((w) => w.period) : [])}
          />
          {t('budgets.allPeriods')}
        </label>
        <div className='grid gap-2'>
          {view.windows.map((w) => (
            <label key={w.period} className='grid grid-cols-[20px_60px_1fr] items-center gap-2 rounded-md border p-2 text-center text-sm'>
              <Checkbox
                checked={periods.includes(w.period)}
                onCheckedChange={(value) => setPeriods((old) => (value ? [...old, w.period] : old.filter((p) => p !== w.period)))}
              />
              <span>{t('budgets.periods.' + w.period)}</span>
              <span className='text-muted-foreground'>
                {t(
                  restartSelectedCycle && periods.includes(w.period) && w.canResetCycle ? 'budgets.restartSelected' : 'budgets.keepSchedule'
                )}
                {w.mode === 'fixed' && w.period !== 'total' && periods.includes(w.period) && preview.data && (
                  <div>
                    <BudgetTime value={w.windowEnd} timezone={w.timezone} /> →{' '}
                    <BudgetTime value={preview.data.windows.find((p) => p.period === w.period)?.windowEnd ?? null} timezone={w.timezone} />
                  </div>
                )}
              </span>
            </label>
          ))}
        </div>
        <label className='flex items-center gap-2 text-sm'>
          <Checkbox
            checked={restartSelectedCycle}
            disabled={!canRestartCycle || mutation.isPending}
            onCheckedChange={(value) => setResetCycle(value === true)}
          />
          {t('budgets.restartCycle')}
        </label>
        <p className='text-muted-foreground text-xs'>{t('budgets.resetNote')}</p>
        {preview.error && (
          <p role='alert' className='text-destructive text-sm'>
            {preview.error.message}
          </p>
        )}
        <DialogFooter>
          <Button variant='outline' disabled={mutation.isPending} onClick={onClose}>
            {t('common.buttons.cancel')}
          </Button>
          <Button
            disabled={!periods.length || mutation.isPending || preview.isFetching || !preview.data || !!preview.error}
            onClick={async () => {
              try {
                await mutation.mutateAsync({ periods, resetCycle: restartSelectedCycle, revision: view.revision, operationID });
                toast.success(t('budgets.resetDone'));
                onClose();
              } catch (error) {
                toast.error((error as Error).message);
              }
            }}
          >
            {t('budgets.confirmReset')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
