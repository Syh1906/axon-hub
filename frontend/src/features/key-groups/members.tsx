import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { extractNumberID } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { BudgetDialog } from '@/features/budgets/budget-dialog';
import { useManagedMembers, type Member } from './data';
import { periods } from './editors';
import { KeySettingsDialog } from './key-settings-dialog';
import { MemberRow } from './member-row';

export function Members({
  groupRef,
  selecting,
  selected,
  onSelect,
  onDeferred,
  onProfile,
  writable,
  onAdd,
  onMove,
}: {
  groupRef: number;
  selecting: boolean;
  selected: Map<string, Member>;
  onSelect: (keys: Member[], checked: boolean) => void;
  onDeferred: () => void;
  onProfile: (id: string) => void;
  writable: boolean;
  onAdd: () => void;
  onMove?: () => void;
}) {
  const { t } = useTranslation();
  const [offset, setOffset] = useState(0);
  const [editing, setEditing] = useState<Member | null>(null);
  const [budget, setBudget] = useState<Member | null>(null);
  const query = useManagedMembers(groupRef, offset);
  const items = query.data?.items ?? [];
  return (
    <div className='bg-background/70 m-3 flex flex-col gap-2 rounded-lg border p-3'>
      <div className='flex items-center justify-between'>
        <h3 className='font-medium'>
          {t('kg.members')} · {query.data?.total ?? '…'}
        </h3>
        {writable && onMove && (
          <Button size='sm' variant='outline' onClick={onMove}>
            {t('kg.moveExisting')}
          </Button>
        )}
        {selecting && (
          <Button size='sm' variant='outline' disabled={!items.length} onClick={() => onSelect(items, true)}>
            {t('kg.selectLoaded')}
          </Button>
        )}
      </div>
      {query.isPending && <p role='status'>{t('kg.loading')}</p>}
      {query.error && (
        <div role='alert'>
          {query.error.message}
          <Button variant='outline' onClick={() => query.refetch()}>
            {t('kg.reload')}
          </Button>
        </div>
      )}
      <div className='max-h-96 overflow-auto'>
        <Table className='min-w-[1280px]'>
          <TableHeader>
            <TableRow>
              {selecting && (
                <TableHead>
                  <Checkbox
                    aria-label={t('kg.selectLoaded')}
                    checked={items.length > 0 && items.every((k) => selected.has(k.id))}
                    onCheckedChange={(v) => onSelect(items, !!v)}
                  />
                </TableHead>
              )}
              {['keyName', 'credential', 'activeProfile'].map((key) => (
                <TableHead key={key}>{t('kg.' + key)}</TableHead>
              ))}
              <TableHead>{t('budgets.expiry')}</TableHead>
              {periods.map((period) => (
                <TableHead key={period}>{t('budgets.periods.' + period)}</TableHead>
              ))}
              <TableHead>{t('kg.status')}</TableHead>
              <TableHead>{t('kg.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((member) => (
              <MemberRow
                key={member.id}
                member={member}
                selecting={selecting}
                selected={selected.has(member.id)}
                onSelect={(checked) => onSelect([member], checked)}
                onEdit={() => setEditing(member)}
                onProfile={() => onProfile(member.id)}
                onBudget={() => setBudget(member)}
                onDeferred={onDeferred}
                writable={writable}
              />
            ))}
            {!query.isPending && !query.error && !items.length && (
              <TableRow>
                <TableCell colSpan={selecting ? 12 : 11} className='py-6 text-center'>
                  {t('kg.emptyGroup')}{' '}
                  {writable && (
                    <Button size='sm' variant='link' onClick={onAdd}>
                      {t('kg.addKey')}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <div className='text-muted-foreground flex items-center justify-between gap-2 text-xs'>
        <span>{t(groupRef ? 'kg.memberHelp' : 'kg.ungroupedHelp')}</span>
        <div className='flex items-center gap-2'>
          <Button size='sm' variant='ghost' disabled={offset === 0} onClick={() => setOffset((n) => n - 10)}>
            {t('kg.previous')}
          </Button>
          <span>{Math.floor(offset / 10) + 1}</span>
          <Button size='sm' variant='ghost' disabled={offset + 10 >= (query.data?.total ?? 0)} onClick={() => setOffset((n) => n + 10)}>
            {t('kg.next')}
          </Button>
        </div>
      </div>
      {editing && <KeySettingsDialog member={editing} onClose={() => setEditing(null)} onDeferred={onDeferred} />}
      {budget && (
        <BudgetDialog
          target={{ kind: 'key', ownerID: Number(extractNumberID(budget.id)), name: budget.name }}
          onClose={() => setBudget(null)}
        />
      )}
    </div>
  );
}
