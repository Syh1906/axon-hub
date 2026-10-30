import { MoreHorizontal } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { extractNumberID } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { TableCell, TableRow } from '@/components/ui/table';
import { BudgetTime } from '@/features/budgets/budget-table';
import { useBudget } from '@/features/budgets/data';
import { useGeneralSettings } from '@/features/system/data/system';
import { Credential } from './credential';
import type { Member } from './data';
import { periods } from './editors';
import { keyQuickTarget } from './key-settings-dialog';
import { QuickEdit, QuickToggle } from './quick-edit';

export function MemberRow({
  member,
  selecting,
  selected,
  onSelect,
  onEdit,
  onProfile,
  onBudget,
  onDeferred,
  writable,
}: {
  member: Member;
  selecting: boolean;
  selected: boolean;
  onSelect: (checked: boolean) => void;
  onEdit: () => void;
  onProfile: () => void;
  onBudget: () => void;
  onDeferred: () => void;
  writable: boolean;
}) {
  const { t, i18n } = useTranslation();
  const { data: settings } = useGeneralSettings();
  const budget = useBudget({ kind: 'key', ownerID: Number(extractNumberID(member.id)), name: member.name });
  const target = { ...keyQuickTarget(member), budget: budget.data };
  const currency = (value: string) =>
    t('currencies.format', {
      val: Number(value),
      currency: settings?.currencyCode ?? 'USD',
      locale: i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US',
      minimumFractionDigits: 2,
      maximumFractionDigits: 8,
    });
  return (
    <TableRow>
      {selecting && (
        <TableCell>
          <Checkbox aria-label={member.name} checked={selected} onCheckedChange={(value) => onSelect(!!value)} />
        </TableCell>
      )}
      <TableCell className='max-w-40 font-medium'>
        <QuickEdit target={target} kind='name' disabled={!writable} onDeferred={onDeferred}>
          <span className='truncate' title={member.name}>
            {member.name}
          </span>
        </QuickEdit>
      </TableCell>
      <TableCell>
        <Credential id={member.id} />
      </TableCell>
      <TableCell>
        <Button variant='link' size='sm' onClick={onProfile}>
          {member.profiles?.activeProfile || t('kg.defaultProfile')}
        </Button>
      </TableCell>
      <TableCell>
        {budget.isError ? (
          <Button variant='ghost' size='sm' onClick={() => void budget.refetch()}>
            {t('kg.reload')}
          </Button>
        ) : !budget.data ? (
          '…'
        ) : (
          <QuickEdit target={target} kind='expiry' disabled={!writable} onDeferred={onDeferred}>
            {budget.data.expiresAt ? (
              <BudgetTime value={budget.data.expiresAt} timezone={budget.data.timezone} />
            ) : (
              t('budgets.neverExpires')
            )}
          </QuickEdit>
        )}
      </TableCell>
      {periods.map((period) => {
        const window = budget.data?.windows.find((window) => window.period === period);
        return (
          <TableCell key={period} className='min-w-24 text-xs tabular-nums'>
            <QuickEdit target={target} kind={period} disabled={!writable || !budget.data} onDeferred={onDeferred}>
              {!budget.data ? (
                '—'
              ) : window && Number(window.limit) > 0 ? (
                <span>
                  {currency(window.available)}
                  <span className='text-muted-foreground block'>/ {currency(window.limit)}</span>
                </span>
              ) : (
                t('budgets.notConfigured')
              )}
            </QuickEdit>
          </TableCell>
        );
      })}
      <TableCell>
        <QuickToggle target={target} enabled={member.status === 'enabled'} disabled={!writable || member.status === 'archived'} />
      </TableCell>
      <TableCell>
        <div className='flex items-center gap-1'>
          <Button size='sm' variant='outline' disabled={!writable} onClick={onEdit}>
            {t('kg.settings')}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant='ghost' size='icon' aria-label={t('kg.quick.advanced') + ' ' + member.name}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end'>
              <DropdownMenuGroup>
                <DropdownMenuItem onSelect={onBudget}>{t('kg.quick.advanced')}</DropdownMenuItem>
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </TableCell>
    </TableRow>
  );
}
