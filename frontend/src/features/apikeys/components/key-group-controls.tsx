import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { DataTableFacetedFilter } from '@/components/data-table-faceted-filter';
import { useApiKeysContext } from '../context/apikeys-context';
import { readDefaultGroups, saveDefaultGroups, selectedGroupOptions } from '../data/default-groups';
import { useKeyGroups } from '../data/groups';

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
  return (
    <Button variant='outline' size='sm' asChild>
      <Link to='/project/key-groups'>{t('kg.title')}</Link>
    </Button>
  );
}
