import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { usePermissions } from '@/hooks/usePermissions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { DataTableFacetedFilter } from '@/components/data-table-faceted-filter';
import { BudgetButton } from '@/features/budgets/budget-dialog';
import { useApiKeysContext } from '../context/apikeys-context';
import { readDefaultGroups, saveDefaultGroups, selectedGroupOptions } from '../data/default-groups';
import { useKeyGroups, useSaveKeyGroup, useDeleteKeyGroup } from '../data/groups';

export function KeyGroupFilter({ defaults = false }: { defaults?: boolean }) {
  return defaults ? <KeyGroupDefaults /> : <KeyGroupQuickFilter />;
}

function KeyGroupQuickFilter() {
  const { t } = useTranslation();
  const { groupFilters, setGroupFilters } = useApiKeysContext();
  const groups = useKeyGroups();
  const options = [{ id: 0, name: t('apikeys.groups.none') }, ...(groups.data ?? [])];
  const unavailable = selectedGroupOptions(groupFilters, options, (id) => t('apikeys.groups.unavailable', { id })).filter(
    (option) => !options.some((existing) => existing.id === option.id)
  );
  return (
    <DataTableFacetedFilter
      title={t('apikeys.groups.title')}
      options={[...options, ...unavailable].map((group) => ({ value: String(group.id), label: group.name }))}
      value={groupFilters.map(String)}
      onValueChange={(values) => setGroupFilters(values.map(Number))}
      footer={
        groups.error ? (
          <p role='alert' className='p-2 text-sm'>
            {groups.error.message}
          </p>
        ) : undefined
      }
    />
  );
}

function KeyGroupDefaults() {
  const { t } = useTranslation();
  const { setGroupFilters, groupStorageKey } = useApiKeysContext();
  const groups = useKeyGroups();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<number[]>([]);
  const [search, setSearch] = useState('');
  const options = [{ id: 0, name: t('apikeys.groups.none') }, ...(groups.data ?? [])];
  const selected = selectedGroupOptions(draft, options, (id) => t('apikeys.groups.unavailable', { id }));
  return (
    <>
      <Button
        variant='outline'
        size='sm'
        onClick={() => {
          setDraft(readDefaultGroups(groupStorageKey));
          setSearch('');
          setOpen(true);
        }}
      >
        {t('apikeys.groups.defaults')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='sm:max-w-xl'>
          <DialogHeader>
            <DialogTitle>{t('apikeys.groups.defaults')}</DialogTitle>
            <DialogDescription>{t('apikeys.groups.defaultHelp')}</DialogDescription>
          </DialogHeader>
          {selected.length > 0 && (
            <div className='flex max-h-24 flex-wrap gap-1 overflow-auto'>
              {selected.map((option) => (
                <Button
                  key={option.id}
                  size='sm'
                  variant='secondary'
                  className='max-w-full'
                  title={option.name}
                  aria-label={t('apikeys.groups.removeSelection', { name: option.name })}
                  onClick={() => setDraft(draft.filter((id) => id !== option.id))}
                >
                  <span className='truncate'>{option.name}</span>
                  <span aria-hidden>×</span>
                </Button>
              ))}
            </div>
          )}
          <Input
            aria-label={t('apikeys.groups.search')}
            placeholder={t('apikeys.groups.search')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          {groups.error && <p role='alert'>{groups.error.message}</p>}
          <div className='flex max-h-60 flex-col gap-2 overflow-auto'>
            {options
              .filter((g) => g.name.toLowerCase().includes(search.toLowerCase()))
              .map((g) => (
                <Label key={g.id} className='flex min-h-9 items-center gap-2'>
                  <Checkbox
                    checked={draft.includes(g.id)}
                    onCheckedChange={(checked) => setDraft(checked ? [...draft, g.id] : draft.filter((id) => id !== g.id))}
                  />
                  {g.name}
                </Label>
              ))}
          </div>
          <DialogFooter>
            <Button variant='ghost' onClick={() => setDraft([])}>
              {t('apikeys.groups.clear')}
            </Button>
            <Button variant='outline' onClick={() => setOpen(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button
              onClick={() => {
                try {
                  saveDefaultGroups(groupStorageKey, draft);
                  setGroupFilters(draft);
                  setOpen(false);
                } catch {
                  toast.error(t('apikeys.groups.saveFailed'));
                }
              }}
            >
              {t('apikeys.groups.saveApply')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

export function KeyGroupManager() {
  const { t } = useTranslation();
  const { hasProjectScope } = usePermissions();
  const groups = useKeyGroups();
  const save = useSaveKeyGroup();
  const remove = useDeleteKeyGroup();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [id, setID] = useState<number>();
  const [deleting, setDeleting] = useState<number>();
  if (!hasProjectScope('write_users') || !hasProjectScope('write_roles')) return null;
  const pending = save.isPending || remove.isPending;
  return (
    <>
      <Button
        variant='outline'
        size='sm'
        onClick={() => {
          setName('');
          setID(undefined);
          setDeleting(undefined);
          setOpen(true);
        }}
      >
        {t('apikeys.groups.title')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='sm:max-w-xl'>
          <DialogHeader>
            <DialogTitle>{t('apikeys.groups.title')}</DialogTitle>
            <DialogDescription>{t('apikeys.groups.manageHelp')}</DialogDescription>
          </DialogHeader>
          <Label htmlFor='key-group-name'>{t('apikeys.groups.name')}</Label>
          <Input id='key-group-name' value={name} maxLength={128} onChange={(e) => setName(e.target.value)} />
          <Button
            disabled={pending || !name.trim()}
            onClick={async () => {
              try {
                await save.mutateAsync({ id, name });
                setName('');
                setID(undefined);
              } catch (error) {
                toast.error((error as Error).message);
              }
            }}
          >
            {t(id ? 'common.buttons.save' : 'common.buttons.create')}
          </Button>
          {groups.error && <p role='alert'>{groups.error.message}</p>}
          <div className='flex max-h-60 flex-col gap-2 overflow-auto'>
            {groups.data?.map((g) => (
              <div key={g.id} className='flex min-h-9 items-center gap-2'>
                <span className='min-w-0 flex-1 truncate' title={g.name}>
                  {g.name}
                </span>
                <Button
                  variant='ghost'
                  size='sm'
                  disabled={pending}
                  onClick={() => {
                    setID(g.id);
                    setName(g.name);
                  }}
                >
                  {t('common.buttons.edit')}
                </Button>
                <BudgetButton target={{ kind: 'group', ownerID: g.id, name: g.name }} />
                <Button variant='ghost' size='sm' disabled={pending} onClick={() => setDeleting(g.id)}>
                  {t('common.buttons.delete')}
                </Button>
              </div>
            ))}
          </div>
          {deleting !== undefined && (
            <div className='flex flex-col gap-2 rounded-md border p-3'>
              <p>{t('apikeys.groups.deleteHelp')}</p>
              <div className='flex justify-end gap-2'>
                <Button variant='outline' onClick={() => setDeleting(undefined)}>
                  {t('common.buttons.cancel')}
                </Button>
                <Button
                  variant='destructive'
                  disabled={pending}
                  onClick={async () => {
                    try {
                      await remove.mutateAsync(deleting);
                      if (id === deleting) {
                        setID(undefined);
                        setName('');
                      }
                      setDeleting(undefined);
                    } catch (error) {
                      toast.error((error as Error).message);
                    }
                  }}
                >
                  {t('common.buttons.delete')}
                </Button>
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant='outline' onClick={() => setOpen(false)}>
              {t('common.buttons.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
