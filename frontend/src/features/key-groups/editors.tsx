import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { useSelectedProjectId } from '@/stores/projectStore';
import { extractNumberID } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { useApiKeyProfileTemplates } from '@/features/apikeys/data/apikeys';
import { useKeyGroups } from '@/features/apikeys/data/groups';
import type { BudgetPeriod, BudgetRule } from '@/features/budgets/data';
import type { BudgetPatch, KeyInput } from './data';

export function Choice({
  value,
  onChange,
  options,
  label,
  disabled = false,
}: {
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  label: string;
  disabled?: boolean;
}) {
  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger aria-label={label}>
        <SelectValue placeholder={label} />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  );
}
export function GroupChoice({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  const { t } = useTranslation();
  const groups = useKeyGroups();
  return (
    <Choice
      label={t('kg.group')}
      value={String(value)}
      onChange={(v) => onChange(Number(v))}
      options={[{ value: '0', label: t('kg.ungrouped') }, ...(groups.data ?? []).map((g) => ({ value: String(g.id), label: g.name }))]}
    />
  );
}
export function TemplateChoice({ value, onChange }: { value?: number; onChange: (value?: number) => void }) {
  const { t } = useTranslation();
  const project = useSelectedProjectId();
  const query = useApiKeyProfileTemplates(project);
  return (
    <>
      <Choice
        label={t('kg.template')}
        value={String(value ?? 0)}
        onChange={(v) => onChange(Number(v) || undefined)}
        options={[
          { value: '0', label: t('kg.noTemplate') },
          ...(query.data ?? []).map((v) => ({ value: extractNumberID(v.id), label: v.name })),
        ]}
      />
      {query.error && <p role='alert'>{query.error.message}</p>}
    </>
  );
}
export const periods: BudgetPeriod[] = ['five_hour', 'daily', 'weekly', 'monthly', 'total'];
export function BudgetPatchFields({ value = {}, onChange }: { value?: BudgetPatch; onChange: (value: BudgetPatch) => void }) {
  const { t } = useTranslation();
  const id = useId();
  const update = (period: BudgetPeriod, patch: Partial<BudgetRule>) =>
    onChange({
      ...value,
      rules: periods.flatMap((p) => {
        const rule = value.rules?.find((r) => r.period === p);
        return p === period
          ? [{ period: p, mode: p === 'five_hour' ? 'rolling' : 'fixed', limit: '', minute: 0, ...rule, ...patch } as BudgetRule]
          : rule
            ? [rule]
            : [];
      }),
    });
  const local = value.expiresAt
    ? new Date(new Date(value.expiresAt).getTime() - new Date(value.expiresAt).getTimezoneOffset() * 60000).toISOString().slice(0, 16)
    : '';
  return (
    <div className='flex flex-col gap-3'>
      <p className='text-muted-foreground text-xs'>{t('kg.patchHelp')}</p>
      <div className='overflow-x-auto rounded-md border'>
        <div className='min-w-[540px]'>
          {periods.map((p) => {
            const rule = value.rules?.find((r) => r.period === p);
            return (
              <div key={p} className='grid grid-cols-[110px_140px_1fr] items-center gap-3 border-b p-2 last:border-0'>
                <label className='flex items-center gap-2 text-sm'>
                  <Checkbox
                    checked={!!rule}
                    onCheckedChange={(v) => (v ? update(p, {}) : onChange({ ...value, rules: value.rules?.filter((r) => r.period !== p) }))}
                  />
                  {t('budgets.periods.' + p)}
                </label>
                <Input
                  aria-label={t('budgets.periods.' + p) + ' ' + t('kg.limit')}
                  disabled={!rule}
                  type='number'
                  min='0'
                  step='0.000001'
                  value={rule?.limit ?? ''}
                  placeholder={t('kg.unchanged')}
                  onChange={(e) => update(p, { limit: e.target.value })}
                />
                <div className='flex items-center gap-2'>
                  <Choice
                    label={t('kg.cycle')}
                    disabled={!rule || p === 'total'}
                    value={rule?.mode ?? (p === 'five_hour' ? 'rolling' : 'fixed')}
                    onChange={(v) => update(p, { mode: v as BudgetRule['mode'] })}
                    options={(p === 'total' ? ['fixed'] : ['fixed', 'rolling']).map((mode) => ({
                      value: mode,
                      label: t('budgets.mode.' + (p === 'total' ? 'total' : p + (mode === 'fixed' ? 'Fixed' : 'Rolling'))),
                    }))}
                  />
                  {p === 'daily' && rule?.mode === 'fixed' && (
                    <Input
                      aria-label={t('budgets.refreshTime')}
                      type='time'
                      className='w-28'
                      value={`${String(Math.floor(rule.minute / 60)).padStart(2, '0')}:${String(rule.minute % 60).padStart(2, '0')}`}
                      onChange={(e) => {
                        const [h, m] = e.target.value.split(':').map(Number);
                        update(p, { minute: h * 60 + m });
                      }}
                    />
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
      <Label htmlFor={id + 'zone'}>{t('budgets.timezone')}</Label>
      <Input
        id={id + 'zone'}
        placeholder={t('kg.inheritTimezone')}
        value={value.timezone ?? ''}
        onChange={(e) => onChange({ ...value, timezone: e.target.value || undefined })}
      />
      <label className='flex items-center gap-2 text-sm'>
        <Checkbox checked={!!value.setExpiry} onCheckedChange={(v) => onChange({ ...value, setExpiry: !!v })} />
        {t('kg.changeExpiry')}
      </label>
      {value.setExpiry && (
        <div className='flex flex-wrap items-center gap-2'>
          <Input
            className='w-auto'
            aria-label={t('budgets.expiry')}
            type='datetime-local'
            value={local}
            onChange={(e) => onChange({ ...value, expiresAt: e.target.value ? new Date(e.target.value).toISOString() : undefined })}
          />
          <Button variant='outline' size='sm' onClick={() => onChange({ ...value, expiresAt: null })}>
            {t('budgets.neverExpires')}
          </Button>
          <span className='text-muted-foreground text-xs'>{Intl.DateTimeFormat().resolvedOptions().timeZone}</span>
        </div>
      )}
      <p className='text-muted-foreground text-xs'>{t('budgets.windowHelp')}</p>
    </div>
  );
}
export function KeyInputFields({
  value,
  onChange,
  hideName = false,
  expandedRestrictions = false,
}: {
  value: KeyInput;
  onChange: (value: KeyInput) => void;
  hideName?: boolean;
  expandedRestrictions?: boolean;
}) {
  const { t } = useTranslation();
  const id = useId();
  return (
    <div className='flex flex-col gap-3'>
      {!hideName && (
        <>
          <Label htmlFor={id + 'name'}>{t('kg.keyName')}</Label>
          <Input id={id + 'name'} value={value.name} onChange={(e) => onChange({ ...value, name: e.target.value })} />
        </>
      )}
      <Label>{t('kg.keyType')}</Label>
      <Choice
        label={t('kg.keyType')}
        value={value.type}
        onChange={(v) => onChange({ ...value, type: v, scopes: undefined })}
        options={['personal', 'user', 'service_account'].map((v) => ({ value: v, label: t('apikeys.type.' + v) }))}
      />
      {value.type === 'service_account' && (
        <>
          <Label htmlFor={id + 'scopes'}>{t('kg.scopes')}</Label>
          <Textarea
            id={id + 'scopes'}
            value={value.scopes?.join('\n') ?? ''}
            onChange={(e) => onChange({ ...value, scopes: e.target.value.split(/[\s,]+/).filter(Boolean) })}
          />
        </>
      )}
      <Label>{t('kg.template')}</Label>
      <TemplateChoice value={value.templateID} onChange={(v) => onChange({ ...value, templateID: v })} />
      <details open={expandedRestrictions}>
        <summary className='cursor-pointer text-sm font-medium'>{t('kg.keyRestrictions')}</summary>
        <div className='flex flex-col gap-3 pt-3'>
          <Label htmlFor={id + 'ips'}>{t('kg.ips')}</Label>
          <Textarea
            id={id + 'ips'}
            value={value.allowedIPs?.join('\n') ?? ''}
            onChange={(e) => onChange({ ...value, allowedIPs: e.target.value.split(/[\s,]+/).filter(Boolean) })}
          />
          <BudgetPatchFields value={value.budget} onChange={(budget) => onChange({ ...value, budget })} />
        </div>
      </details>
    </div>
  );
}
