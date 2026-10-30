import { useState } from 'react';
import { Copy, Eye, EyeOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { extractNumberID } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { BudgetButton } from '@/features/budgets/budget-dialog';
import { useManagementAPI, useManagedMembers, type Member } from './data';

function Credential({ id }: { id: string }) {
  const { t } = useTranslation();
  const api = useManagementAPI();
  const [value, setValue] = useState<string>();
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  async function access(copy: boolean) {
    setBusy(true);
    try {
      const secret = value ?? (await api.credentials([Number(extractNumberID(id))]))[0]?.key;
      if (!secret) throw new Error(t('kg.resultsUnavailable'));
      if (copy) {
        await navigator.clipboard.writeText(secret);
        toast.success(t('kg.copied'));
      } else {
        setValue(secret);
        setVisible(true);
      }
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className='flex max-w-60 items-center gap-1'>
      <code className='bg-muted max-w-32 truncate rounded px-2 py-1'>{visible ? value : '••••••••'}</code>
      <Button
        variant='ghost'
        size='icon'
        disabled={busy}
        aria-label={t(visible ? 'kg.hide' : 'kg.reveal')}
        onClick={() => (visible ? setVisible(false) : access(false))}
      >
        {visible ? <EyeOff className='size-4' /> : <Eye className='size-4' />}
      </Button>
      <Button variant='ghost' size='icon' disabled={busy} aria-label={t('kg.copyKey')} onClick={() => access(true)}>
        <Copy className='size-4' />
      </Button>
    </div>
  );
}
export function Members({
  groupRef,
  selecting,
  selected,
  onSelect,
  onEdit,
  onProfile,
  writable,
  onAdd,
  onMove,
}: {
  groupRef: number;
  selecting: boolean;
  selected: Map<string, Member>;
  onSelect: (keys: Member[], checked: boolean) => void;
  onEdit: (member: Member) => void;
  onProfile: (id: string) => void;
  writable: boolean;
  onAdd: () => void;
  onMove?: () => void;
}) {
  const { t } = useTranslation();
  const [offset, setOffset] = useState(0);
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
        <Table className='min-w-[860px]'>
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
              {['keyName', 'credential', 'activeProfile', 'individualBudget', 'status', 'actions'].map((k) => (
                <TableHead key={k}>{t('kg.' + k)}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((k) => (
              <TableRow key={k.id}>
                {selecting && (
                  <TableCell>
                    <Checkbox aria-label={k.name} checked={selected.has(k.id)} onCheckedChange={(v) => onSelect([k], !!v)} />
                  </TableCell>
                )}
                <TableCell className='max-w-40 truncate font-medium' title={k.name}>
                  {k.name}
                </TableCell>
                <TableCell>
                  <Credential id={k.id} />
                </TableCell>
                <TableCell>
                  <Button variant='link' size='sm' onClick={() => onProfile(k.id)}>
                    {k.profiles?.activeProfile || t('kg.defaultProfile')}
                  </Button>
                </TableCell>
                <TableCell>
                  <BudgetButton summary target={{ kind: 'key', ownerID: Number(extractNumberID(k.id)), name: k.name }} />
                </TableCell>
                <TableCell>
                  <Badge variant={k.status === 'enabled' ? 'secondary' : 'outline'}>
                    {t(k.status === 'enabled' ? 'kg.keyEnabled' : 'kg.' + k.status)}
                  </Badge>
                </TableCell>
                <TableCell>
                  <Button size='sm' variant='outline' disabled={!writable} onClick={() => onEdit(k)}>
                    {t('kg.settings')}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {!query.isPending && !query.error && !items.length && (
              <TableRow>
                <TableCell colSpan={selecting ? 7 : 6} className='py-6 text-center'>
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
    </div>
  );
}
