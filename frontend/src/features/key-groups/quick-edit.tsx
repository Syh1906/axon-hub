import { useEffect, useId, useState, type ReactNode } from 'react';
import Decimal from 'decimal.js-light';
import { Pencil } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import type { BudgetPeriod, BudgetView } from '@/features/budgets/data';
import { extendBudgetExpiry } from '@/features/budgets/expiry';
import { useGeneralSettings } from '@/features/system/data/system';
import { useManagementAPI, type Group, type ManagementRow } from './data';
import { localExpiry as toLocal, expiryInstant } from './quick-edit-date';
import { useSubmission } from './submission';

type EditKind = 'name' | 'note' | 'expiry' | BudgetPeriod;
export type QuickEditTarget = {
  kind: 'group' | 'key';
  name: string;
  note?: string;
  groupRef?: number;
  budget?: BudgetView;
  row: Pick<ManagementRow, 'action' | 'targetID' | 'rowID' | 'revision' | 'updatedAt'>;
};
function groupTarget(group: Group): QuickEditTarget {
  return {
    kind: 'group',
    name: group.name,
    note: group.note,
    budget: group.budget,
    row: { action: 'updateGroup', targetID: group.id, rowID: String(group.id), revision: group.revision },
  };
}

export function useQuickSave(target: QuickEditTarget, onSaved?: () => void) {
  const flow = useSubmission(false, {
    verifyGroups: target.kind === 'group',
    verifyKey: target.kind === 'key' ? { ownerID: target.row.targetID!, groupRef: target.groupRef ?? 0 } : undefined,
    notifyCompletion: true,
  });
  const api = useManagementAPI();
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [readError, setReadError] = useState(false);
  const result = flow.results[0];
  useEffect(() => {
    if (result?.success && !saving && !readError) {
      flow.clear();
      toast.success(t('kg.quick.saved'));
      onSaved?.();
    }
  }, [result, saving, readError]);
  const execute = async (recover = false, rows: ManagementRow[] = []) => {
    setSaving(true);
    try {
      await flow.execute(recover, rows);
    } catch {
      // Submission receipts have already been received; only the readback failed.
      setReadError(true);
    } finally {
      setSaving(false);
    }
  };
  return {
    ...flow,
    busy: flow.busy || saving,
    execute,
    readError,
    retryRead: async () => {
      setSaving(true);
      try {
        await api.refresh(
          target.kind === 'group',
          target.kind === 'key' ? { ownerID: target.row.targetID!, groupRef: target.groupRef ?? 0 } : undefined
        );
        setReadError(false);
      } catch {
        setReadError(true);
      } finally {
        setSaving(false);
      }
    },
    message: flow.error || (result && !result.success ? result.error : ''),
    save: (patch: Partial<ManagementRow>) => {
      flow.clear();
      return execute(false, [{ ...target.row, ...patch }]);
    },
  };
}

export function GroupToggle({ group, disabled }: { group: Group; disabled: boolean }) {
  return <QuickToggle target={groupTarget(group)} enabled={group.enabled} disabled={disabled} />;
}

export function QuickToggle({ target, enabled, disabled }: { target: QuickEditTarget; enabled: boolean; disabled: boolean }) {
  const { t } = useTranslation();
  const flow = useQuickSave(target);
  return (
    <div className='flex flex-col items-end gap-1'>
      <Switch
        checked={enabled}
        disabled={disabled || flow.busy || flow.uncertain || flow.readError}
        aria-label={t(target.kind === 'group' ? 'kg.quick.toggle' : 'kg.quick.toggleKey', { name: target.name })}
        onCheckedChange={(enabled) => void flow.save({ enabled })}
      />
      {flow.message && (
        <span role='alert' className='text-destructive max-w-48 text-xs'>
          {flow.message}
        </span>
      )}
      {flow.uncertain && (
        <Button size='sm' variant='outline' disabled={flow.busy} onClick={() => void flow.execute(true)}>
          {t('kg.recover')}
        </Button>
      )}
      {flow.readError && (
        <>
          <span role='alert' className='text-destructive max-w-48 text-xs'>
            {t('kg.quick.readFailed')}
          </span>
          <Button size='sm' variant='outline' disabled={flow.busy} onClick={() => void flow.retryRead()}>
            {t('kg.reload')}
          </Button>
        </>
      )}
    </div>
  );
}

export function GroupQuickEdit({
  group,
  ...props
}: {
  group: Group;
  kind: EditKind;
  disabled: boolean;
  children: ReactNode;
  onDeferred: () => void;
}) {
  return <QuickEdit target={groupTarget(group)} {...props} />;
}

export function QuickEdit({
  target,
  kind,
  disabled,
  children,
  onDeferred,
}: {
  target: QuickEditTarget;
  kind: EditKind;
  disabled: boolean;
  children: ReactNode;
  onDeferred: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [locked, setLocked] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const close = () => {
    if (uncertain) onDeferred();
    setLocked(false);
    setOpen(false);
  };
  return (
    <Popover
      open={open}
      onOpenChange={(value) => {
        if (!locked) {
          if (value) setOpen(true);
          else close();
        }
      }}
    >
      <PopoverTrigger asChild>
        <button
          disabled={disabled}
          className='hover:bg-muted focus-visible:ring-ring inline-flex max-w-full items-center gap-1 rounded px-1 py-1 text-left outline-none focus-visible:ring-2 disabled:cursor-default'
        >
          {children}
          {(kind === 'name' || kind === 'note') && !disabled && <Pencil className='text-muted-foreground size-3 shrink-0' />}
        </button>
      </PopoverTrigger>
      {open && <QuickForm target={target} kind={kind} onClose={close} onLock={setLocked} onPendingChange={setUncertain} />}
    </Popover>
  );
}

function QuickForm({
  target,
  kind,
  onClose,
  onLock,
  onPendingChange,
}: {
  target: QuickEditTarget;
  kind: EditKind;
  onClose: () => void;
  onLock: (locked: boolean) => void;
  onPendingChange: (pending: boolean) => void;
}) {
  const { t, i18n } = useTranslation();
  const id = useId();
  // Capture the revision with the values being edited, rather than silently overwriting newer changes.
  const [snapshot] = useState(target);
  const flow = useQuickSave(snapshot, onClose);
  useEffect(() => {
    onLock(flow.busy);
    onPendingChange(flow.uncertain);
  }, [flow.busy, flow.uncertain, onLock, onPendingChange]);
  const { data: settings } = useGeneralSettings();
  const basic = kind === 'name' || kind === 'note';
  const expiry = kind === 'expiry';
  const window = snapshot.budget?.windows.find((w) => w.period === kind);
  const [name, setName] = useState(snapshot.name);
  const [note, setNote] = useState(snapshot.note ?? '');
  const [mode, setMode] = useState('set');
  const [amount, setAmount] = useState(window?.limit ?? '0');
  const [date, setDate] = useState(toLocal(snapshot.budget?.expiresAt ?? null));
  const [never, setNever] = useState(!snapshot.budget?.expiresAt);
  const parsedExpiry = expiryInstant(date, snapshot.budget?.expiresAt);
  const validExpiry = Number.isFinite(parsedExpiry.getTime());
  const validAmount = /^\d+(\.\d{1,8})?$/.test(amount);
  const limit = validAmount ? new Decimal(amount).plus(mode === 'add' ? (window?.limit ?? '0') : '0').toFixed() : null;
  const remaining = limit && window ? new Decimal(limit).minus(window.settled).minus(window.reserved) : null;
  const available = remaining?.isNegative() ? '0' : (remaining?.toFixed() ?? null);
  const valid = basic
    ? !!name.trim() && [...name.trim()].length <= 128 && [...note].length <= 1024
    : expiry
      ? never || (!!date && validExpiry)
      : !!limit && (mode !== 'add' || new Decimal(amount).greaterThan(0));
  const title = basic
    ? t(snapshot.kind === 'group' ? 'kg.quick.basic' : 'kg.keyName')
    : expiry
      ? t('budgets.expiry')
      : t('budgets.periods.' + kind) + ' · ' + t('kg.limit');
  const money = (value: string) =>
    t('currencies.format', {
      val: Number(value),
      currency: settings?.currencyCode || 'USD',
      locale: i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US',
      minimumFractionDigits: 2,
      maximumFractionDigits: 8,
    });
  function save() {
    if (!valid) return;
    if (basic)
      void flow.save({
        ...(name.trim() !== snapshot.name ? { name: name.trim() } : {}),
        ...(snapshot.kind === 'group' && note !== snapshot.note ? { note } : {}),
      });
    else if (expiry)
      void flow.save({
        budget: { revision: snapshot.budget?.revision, setExpiry: true, expiresAt: never ? null : parsedExpiry.toISOString() },
      });
    else if (window && limit !== null)
      void flow.save({
        budget: {
          revision: snapshot.budget?.revision,
          rules: [
            {
              period: window.period,
              mode: window.pendingRule?.mode ?? window.mode,
              minute: window.pendingRule?.minute ?? window.minute,
              limit,
            },
          ],
        },
      });
  }
  return (
    <PopoverContent
      aria-labelledby={id + 'title'}
      align='start'
      className='max-h-[min(80dvh,600px)] w-[340px] max-w-[calc(100vw-24px)] overflow-y-auto'
      collisionPadding={12}
      onInteractOutside={(event) => {
        if (flow.busy) event.preventDefault();
      }}
      onEscapeKeyDown={(event) => {
        if (flow.busy) event.preventDefault();
      }}
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          save();
        }}
        className='flex flex-col gap-3'
      >
        <div className='flex items-center justify-between gap-2'>
          <h3 id={id + 'title'} className='font-semibold'>
            {title}
          </h3>
          <span className='text-muted-foreground truncate text-xs'>{snapshot.name}</span>
        </div>
        <fieldset disabled={flow.busy || flow.uncertain || flow.readError} className='flex min-w-0 flex-col gap-3'>
          {basic ? (
            <>
              <Label htmlFor={id + 'name'}>{t('kg.field.name')}</Label>
              <Input id={id + 'name'} autoFocus={kind === 'name'} value={name} onChange={(e) => setName(e.target.value)} />
              {snapshot.kind === 'group' && (
                <>
                  <Label htmlFor={id + 'note'}>{t('kg.note')}</Label>
                  <Textarea
                    id={id + 'note'}
                    autoFocus={kind === 'note'}
                    rows={3}
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                    placeholder={t('kg.quick.optionalNote')}
                  />
                </>
              )}
            </>
          ) : expiry ? (
            <>
              <p className='text-muted-foreground text-xs'>
                {t('kg.quick.current')}: {toLocal(snapshot.budget?.expiresAt ?? null).replace('T', ' ') || t('budgets.neverExpires')}
              </p>
              <div className='flex flex-wrap gap-1'>
                {[0, 1, 3, 12].map((months) => (
                  <Button
                    key={months}
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() => {
                      const base = snapshot.budget?.expiresAt ?? '';
                      const next = extendBudgetExpiry(base, months);
                      if (!months) next.setDate(next.getDate() + 7);
                      setDate(toLocal(next.toISOString()));
                      setNever(false);
                    }}
                  >
                    {t('kg.quick.extend' + months)}
                  </Button>
                ))}
              </div>
              <Label htmlFor={id + 'date'}>{t('budgets.expiry')}</Label>
              <Input
                id={id + 'date'}
                type='datetime-local'
                value={date}
                aria-invalid={!never && !validExpiry}
                onInput={(e) => {
                  setDate(e.currentTarget.value);
                  setNever(false);
                }}
              />
              {!never && !validExpiry && (
                <p role='alert' className='text-destructive text-sm'>
                  {t('kg.quick.invalidExpiry')}
                </p>
              )}
              <span className='text-muted-foreground text-xs'>
                {t('budgets.timezone')}: {Intl.DateTimeFormat().resolvedOptions().timeZone}
              </span>
              <Button
                type='button'
                variant={never ? 'secondary' : 'ghost'}
                onClick={() => {
                  setNever(true);
                  setDate('');
                }}
              >
                {t('budgets.neverExpires')}
              </Button>
              <p className='text-muted-foreground text-xs'>{t('kg.quick.expiryHelp')}</p>
            </>
          ) : (
            <>
              <Tabs
                value={mode}
                onValueChange={(v) => {
                  setMode(v);
                  setAmount(v === 'add' ? '' : (window?.limit ?? '0'));
                }}
              >
                <TabsList className='w-full'>
                  <TabsTrigger value='set'>{t('kg.quick.set')}</TabsTrigger>
                  <TabsTrigger value='add'>{t('kg.quick.add')}</TabsTrigger>
                </TabsList>
              </Tabs>
              <Label htmlFor={id + 'amount'}>{t('kg.limit')}</Label>
              <Input id={id + 'amount'} inputMode='decimal' autoFocus value={amount} onChange={(e) => setAmount(e.target.value)} />
              {limit !== null && (
                <div className='text-sm tabular-nums'>
                  <p>
                    {t('kg.limit')}: {money(window?.limit ?? '0')} → {Number(limit) === 0 ? t('budgets.notConfigured') : money(limit)}
                  </p>
                  <p>
                    {t('kg.quick.available')}: {money(window?.available ?? '0')} →{' '}
                    {Number(limit) === 0 ? t('budgets.notConfigured') : money(available ?? '0')}
                  </p>
                </div>
              )}
              <p className='text-muted-foreground text-xs'>{t('kg.quick.quotaHelp')}</p>
              <Button
                type='button'
                variant='ghost'
                onClick={() => {
                  setMode('set');
                  setAmount('0');
                }}
              >
                {t('kg.quick.clearLimit')}
              </Button>
            </>
          )}
        </fieldset>
        {flow.message && (
          <p role='alert' className='text-destructive text-sm'>
            {flow.message}
          </p>
        )}
        {flow.readError && (
          <p role='alert' className='text-destructive text-sm'>
            {t('kg.quick.readFailed')}
          </p>
        )}
        <div className='flex justify-end gap-2'>
          <Button type='button' variant='outline' disabled={flow.busy} onClick={onClose}>
            {t(flow.uncertain ? 'kg.deferRecovery' : 'common.buttons.cancel')}
          </Button>
          {flow.readError ? (
            <Button type='button' disabled={flow.busy} onClick={() => void flow.retryRead()}>
              {t('kg.reload')}
            </Button>
          ) : flow.uncertain ? (
            <Button type='button' disabled={flow.busy} onClick={() => void flow.execute(true)}>
              {t('kg.recover')}
            </Button>
          ) : (
            <Button type='submit' disabled={!valid || flow.busy || (!basic && !expiry && !window)}>
              {t(flow.busy ? 'kg.working' : 'common.buttons.save')}
            </Button>
          )}
        </div>
      </form>
    </PopoverContent>
  );
}
