import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useDebounce } from '@/hooks/use-debounce';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import type { ModelAssociation } from '../data/schema';
import { sameAssociation, MAX_ASSOCIATION_PRIORITY, clampAssociationPriority } from '../utils/bulk-associations';

type Channel = { value: number; label: string; tags: string[]; allModelEntries: { requestModel: string; actualModel: string }[] };
export function AssociationBulkPicker({
  channels,
  existing,
  modelID,
  onCancel,
  onAdd,
  portalContainer,
}: {
  channels: Channel[];
  portalContainer: HTMLElement | null;
  existing: ModelAssociation[];
  modelID: string;
  onCancel: () => void;
  onAdd: (rules: ModelAssociation[]) => void;
}) {
  const { t } = useTranslation();
  const label = (key: string) => t(`models.bulk.${key}`);
  const [search, setSearch] = useState('');
  const [tag, setTag] = useState<string | null>(null);
  const tags = [...new Set(channels.flatMap((channel) => channel.tags))].sort();
  const query = useDebounce(search, 200).toLowerCase();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [priority, setPriority] = useState(0);
  const rows = useMemo(
    () =>
      channels.flatMap((channel) =>
        [...new Map(channel.allModelEntries.map((entry) => [entry.requestModel, entry])).values()].map((entry) => ({
          key: JSON.stringify([channel.value, entry.requestModel]),
          channel,
          entry,
          rule: {
            type: 'channel_model',
            priority,
            disabled: false,
            channelModel: { channelId: channel.value, modelId: entry.requestModel },
          } as ModelAssociation,
        }))
      ),
    [channels, priority]
  );
  const available = rows.filter((row) => !existing.some((rule) => sameAssociation(rule, row.rule)));
  const visible = rows.filter(
    (row) =>
      (tag === null || row.channel.tags.includes(tag)) &&
      `${row.channel.label} ${row.channel.tags.join(' ')} ${row.entry.requestModel}`.toLowerCase().includes(query)
  );
  const visibleAvailable = visible.filter((row) => available.includes(row));
  const chosen = available.filter((row) => selected.has(row.key));
  function toggle(keys: string[], checked: boolean) {
    setSelected((previous) => {
      const next = new Set(previous);
      keys.forEach((key) => (checked ? next.add(key) : next.delete(key)));
      return next;
    });
  }
  return (
    <div className='flex min-h-0 flex-1 flex-col gap-3'>
      <Button variant='ghost' className='self-start' onClick={onCancel}>
        ← {label('back')}
      </Button>
      <h3 className='font-semibold'>{label('add')}</h3>
      <p>
        {label('singleHelp')} <code>{modelID}</code>
      </p>
      <div className='flex flex-wrap gap-2'>
        <Input
          className='min-w-48 flex-1'
          aria-label={label('search')}
          placeholder={label('search')}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Select value={tag === null ? 'all' : `tag:${tag}`} onValueChange={(value) => setTag(value === 'all' ? null : value.slice(4))}>
          <SelectTrigger className='w-40' aria-label={label('tags')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent container={portalContainer}>
            <SelectGroup>
              <SelectItem value='all'>{label('allTags')}</SelectItem>
              {tags.map((value) => (
                <SelectItem key={value} value={`tag:${value}`}>
                  {value}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Button
          variant='outline'
          onClick={() =>
            toggle(
              available.filter((row) => row.entry.requestModel === modelID).map((row) => row.key),
              true
            )
          }
        >
          {label('sameName')}
        </Button>
      </div>
      <div className='flex items-center justify-between gap-3'>
        <label className='flex items-center gap-2'>
          <Checkbox
            checked={visibleAvailable.length > 0 && visibleAvailable.every((row) => selected.has(row.key))}
            onCheckedChange={(checked) =>
              toggle(
                visibleAvailable.map((row) => row.key),
                checked === true
              )
            }
          />
          {label('selectVisible')}
        </label>
        <label className='flex items-center gap-2'>
          {label('priority')}
          <Input
            className='w-20'
            type='number'
            min={0}
            max={MAX_ASSOCIATION_PRIORITY}
            value={priority}
            onChange={(e) => setPriority(clampAssociationPriority(Number(e.target.value)))}
          />
        </label>
      </div>
      <div className='min-h-0 flex-1 overflow-auto rounded-lg border'>
        <table className='w-full text-left text-sm'>
          <thead className='bg-background sticky top-0'>
            <tr>
              <th className='p-3'>{label('select')}</th>
              <th>{label('channel')}</th>
              <th>{label('model')}</th>
              <th>{label('forward')}</th>
              <th>{label('status')}</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((row) => {
              const exists = !available.includes(row);
              return (
                <tr key={row.key} className='h-12 border-t'>
                  <td className='p-3'>
                    <Checkbox
                      aria-label={`${row.channel.label} ${row.entry.requestModel}`}
                      disabled={exists}
                      checked={exists || selected.has(row.key)}
                      onCheckedChange={(checked) => toggle([row.key], checked === true)}
                    />
                  </td>
                  <td className='max-w-48 truncate'>{row.channel.label}</td>
                  <td>{row.entry.requestModel}</td>
                  <td>{row.entry.actualModel}</td>
                  <td>{exists ? label('existing') : selected.has(row.key) ? label('selected') : '—'}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {!visible.length && <p className='p-4'>{label('empty')}</p>}
      </div>
      <p className='text-muted-foreground text-xs'>{label('draftHelp')}</p>
      <div className='flex shrink-0 items-center justify-between gap-3 border-t pt-3'>
        <span>
          {t('models.bulk.chosen', { count: chosen.length })} ·{' '}
          {t('models.bulk.chosenChannels', { count: new Set(chosen.map((row) => row.channel.value)).size })}
        </span>
        <div className='flex gap-2'>
          <Button variant='outline' onClick={onCancel}>
            {t('common.buttons.cancel')}
          </Button>
          <Button disabled={!chosen.length} onClick={() => onAdd(chosen.map((row) => row.rule))}>
            {t('models.bulk.addCount', { count: chosen.length })}
          </Button>
        </div>
      </div>
    </div>
  );
}
