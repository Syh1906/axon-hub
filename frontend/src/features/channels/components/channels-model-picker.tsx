import { useId, useState } from 'react';
import { Info, Search, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useDebounce } from '@/hooks/use-debounce';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useConfiguredModelOptions } from '../data/configured-model-options';

export function ChannelsModelPicker({
  supportedModels,
  onAdd,
  onClose,
}: {
  supportedModels: string[];
  onAdd: (models: string[]) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const selectAllID = useId();
  const query = useConfiguredModelOptions(true);
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const filter = useDebounce(search, 200).trim().toLowerCase();
  const existing = new Set(supportedModels);
  const options =
    query.data?.options.filter(
      (option) => !filter || option.id.toLowerCase().includes(filter) || option.names.some((name) => name.toLowerCase().includes(filter))
    ) ?? [];
  const available = options.filter((option) => !existing.has(option.id));
  const selectedCount = [...selected].filter((id) => !existing.has(id)).length;
  const allSelected = available.length > 0 && available.every((option) => selected.has(option.id));
  const someSelected = available.some((option) => selected.has(option.id));
  const toggleAll = (checked: boolean) =>
    setSelected((previous) => {
      const next = new Set(previous);
      available.forEach(({ id }) => (checked ? next.add(id) : next.delete(id)));
      return next;
    });
  return (
    <div className='flex h-full min-h-0 flex-col gap-3' aria-label={t('channels.modelPicker.title')}>
      <div className='flex items-center justify-between gap-2'>
        <h3 className='font-semibold'>{t('channels.modelPicker.title')}</h3>
        <Button type='button' variant='ghost' size='icon' onClick={onClose} aria-label={t('common.buttons.close')}>
          <X />
        </Button>
      </div>
      <div className='relative'>
        <Search className='text-muted-foreground absolute top-2.5 left-3 size-4' />
        <Input
          className='pl-9'
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={t('channels.modelPicker.search')}
          aria-label={t('channels.modelPicker.search')}
        />
      </div>
      <p className='text-muted-foreground text-xs'>{t('channels.modelPicker.source')}</p>
      <div className='flex items-center justify-between gap-2'>
        <div className='flex items-center gap-2'>
          <Checkbox
            id={selectAllID}
            checked={allSelected || (someSelected ? 'indeterminate' : false)}
            disabled={!available.length}
            onCheckedChange={(checked) => toggleAll(checked === true)}
          />
          <Label htmlFor={selectAllID}>{t('channels.modelPicker.selectAll')}</Label>
        </div>
        <Button type='button' size='sm' variant='ghost' onClick={() => toggleAll(false)} disabled={!someSelected}>
          {t('channels.modelPicker.clearAll')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>{t('channels.modelPicker.filterHint')}</p>
      <div className='min-h-0 flex-1 overflow-auto rounded-md border'>
        {options.map((option) => {
          const added = existing.has(option.id);
          return (
            <label
              key={option.id}
              className={`flex items-start gap-3 border-b px-3 py-3 last:border-b-0 ${added ? 'bg-muted/40 text-muted-foreground' : 'hover:bg-accent cursor-pointer'}`}
            >
              <Checkbox
                className='mt-0.5'
                checked={added || selected.has(option.id)}
                disabled={added}
                aria-label={option.id}
                onCheckedChange={(checked) =>
                  setSelected((previous) => {
                    const next = new Set(previous);
                    if (checked) next.add(option.id);
                    else next.delete(option.id);
                    return next;
                  })
                }
              />
              <div className='min-w-0 flex-1'>
                <div className='text-sm break-all'>{option.id}</div>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {option.fromRule ? t('channels.modelPicker.fromRule', { name: option.names.join('、') }) : option.names.join('、')}
                </p>
              </div>
              {added && (
                <Badge variant='secondary' className='shrink-0'>
                  {t('channels.modelPicker.added')}
                </Badge>
              )}
            </label>
          );
        })}
        {!options.length && (
          <p className='text-muted-foreground p-6 text-center text-sm'>
            {t(query.isPending ? 'common.loading' : query.isError ? 'channels.modelPicker.loadFailed' : 'channels.modelPicker.empty')}
          </p>
        )}
      </div>
      <div className='flex shrink-0 flex-col gap-2 border-t pt-3'>
        <p className='text-sm'>{t('channels.modelPicker.selected', { count: selectedCount })}</p>
        <Button
          type='button'
          disabled={!selectedCount || query.isError}
          onClick={() => {
            onAdd([...selected].filter((id) => !existing.has(id)));
            setSelected(new Set());
          }}
        >
          {t('channels.modelPicker.addSelected', { count: selectedCount })}
        </Button>
        <p className='text-muted-foreground text-xs'>{t('channels.modelPicker.saveHint')}</p>
        <p className='text-muted-foreground flex items-start gap-1 text-xs'>
          <Info className='size-3.5 shrink-0' />
          {t('channels.modelPicker.ruleHint')}
        </p>
        {!!query.data?.rulesWithoutIDs.length && (
          <p className='text-muted-foreground text-xs'>
            {t('channels.modelPicker.rulesWithoutIDs', { names: query.data.rulesWithoutIDs.join('、') })}
          </p>
        )}
      </div>
    </div>
  );
}
