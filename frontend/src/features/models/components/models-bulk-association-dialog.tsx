import { useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { extractNumberIDAsNumber } from '@/lib/utils';
import { useDebounce } from '@/hooks/use-debounce';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { useAllChannelSummarys } from '@/features/channels/data/channels';
import { useModels } from '../context/models-context';
import { appendModelAssociations } from '../data/models';
import type { ModelAssociation } from '../data/schema';
import { appendAssociations, MAX_ASSOCIATION_PRIORITY, clampAssociationPriority } from '../utils/bulk-associations';

export function ModelsBulkAssociationDialog() {
  const { t } = useTranslation();
  const label = (key: string) => t(`models.bulk.${key}`);
  const { selectedModels, setOpen } = useModels();
  const client = useQueryClient();
  const channels = useAllChannelSummarys();
  const [mode, setMode] = useState<'channels' | 'tags'>('channels');
  const [targets, setTargets] = useState<string[]>([]);
  const [search, setSearch] = useState('');
  const [modelSearch, setModelSearch] = useState('');
  const query = useDebounce(search, 200).toLowerCase();
  const [names, setNames] = useState<Record<string, string>>({});
  const [excluded, setExcluded] = useState<string[]>([]);
  const [priority, setPriority] = useState(0);
  const [pending, setPending] = useState(false);
  const [results, setResults] = useState<Record<string, string>>({});
  const [models] = useState(selectedModels);
  const nodes = channels.data?.edges.map((edge) => edge.node) ?? [];
  const options =
    mode === 'channels'
      ? nodes.map((node) => ({ id: node.id, name: node.name }))
      : [...new Set(nodes.flatMap((node) => node.tags ?? []))].sort().map((tag) => ({ id: tag, name: tag }));
  const rows = useMemo(
    () =>
      models.map((model) => {
        const modelID = (names[model.id] ?? (model.matchType === 'exact' ? model.modelID : '')).trim();
        const matching = nodes.filter(
          (node) =>
            (mode === 'channels' ? targets.includes(node.id) : node.tags?.some((tag) => targets.includes(tag))) &&
            node.allModelEntries?.some((entry) => entry.requestModel === modelID)
        );
        const candidates: ModelAssociation[] =
          !modelID || !matching.length
            ? []
            : mode === 'channels'
              ? matching.map((node) => ({
                  type: 'channel_model',
                  priority,
                  disabled: false,
                  channelModel: { channelId: extractNumberIDAsNumber(node.id), modelId: modelID },
                }))
              : [{ type: 'channel_tags_model', priority, disabled: false, channelTagsModel: { channelTags: targets, modelId: modelID } }];
        const added = appendAssociations(model.settings.associations, candidates).slice(model.settings.associations.length);
        return { model, modelID, candidates: added, duplicates: candidates.length - added.length };
      }),
    [models, nodes, mode, targets, names, priority]
  );
  const actionable = rows.filter((row) => row.candidates.length && !excluded.includes(row.model.id) && results[row.model.id] !== 'success');
  async function save() {
    setPending(true);
    for (const row of actionable) {
      try {
        await appendModelAssociations(row.model.id, row.candidates);
        setResults((previous) => ({ ...previous, [row.model.id]: 'success' }));
      } catch (error) {
        setResults((previous) => ({ ...previous, [row.model.id]: error instanceof Error ? error.message : label('failed') }));
      }
    }
    await client.invalidateQueries({ queryKey: ['models'] });
    setPending(false);
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) setOpen(null);
      }}
    >
      <DialogContent
        className='flex max-h-[90vh] flex-col sm:max-w-4xl'
        onEscapeKeyDown={(event) => {
          if (pending) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>{label('title')}</DialogTitle>
          <DialogDescription>
            {t('models.bulk.selectedModels', { count: models.length })} · {label('help')}
          </DialogDescription>
        </DialogHeader>
        <fieldset disabled={pending} className='flex min-h-0 flex-col gap-3'>
          <div className='flex gap-2'>
            {(['channels', 'tags'] as const).map((value) => (
              <Button
                key={value}
                variant={mode === value ? 'default' : 'outline'}
                onClick={() => {
                  setMode(value);
                  setTargets([]);
                }}
              >
                {label(value)}
              </Button>
            ))}
          </div>
          <Input
            aria-label={label(mode === 'channels' ? 'searchChannels' : 'searchTags')}
            placeholder={label(mode === 'channels' ? 'searchChannels' : 'searchTags')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <div className='flex max-h-32 flex-wrap gap-3 overflow-auto rounded-md border p-3'>
            {channels.error ? (
              <p role='alert'>{t('common.loadError')}</p>
            ) : channels.isLoading ? (
              <p>{t('common.loading')}</p>
            ) : (
              options
                .filter((option) => option.name.toLowerCase().includes(query))
                .map((option) => (
                  <label key={option.id} className='flex items-center gap-2'>
                    <Checkbox
                      checked={targets.includes(option.id)}
                      onCheckedChange={(checked) =>
                        setTargets(checked ? [...targets, option.id] : targets.filter((id) => id !== option.id))
                      }
                    />
                    {option.name}
                  </label>
                ))
            )}
          </div>
          <label className='flex items-center gap-2'>
            {label('priority')}
            <Input
              type='number'
              className='w-24'
              min={0}
              max={MAX_ASSOCIATION_PRIORITY}
              value={priority}
              onChange={(e) => setPriority(clampAssociationPriority(Number(e.target.value)))}
            />
          </label>
          <p className='text-muted-foreground text-xs'>{label('mappingHelp')}</p>
          <Input
            aria-label={label('searchModels')}
            placeholder={label('searchModels')}
            value={modelSearch}
            onChange={(event) => setModelSearch(event.target.value)}
          />
          <div className='max-h-[40vh] overflow-auto rounded-md border'>
            <table className='w-full text-left text-sm'>
              <thead className='bg-background sticky top-0'>
                <tr>
                  <th className='p-3'>{label('select')}</th>
                  <th>{label('model')}</th>
                  <th>{label('forward')}</th>
                  <th>{label('status')}</th>
                </tr>
              </thead>
              <tbody>
                {rows
                  .filter((row) => `${row.model.name} ${row.model.modelID}`.toLowerCase().includes(modelSearch.toLowerCase()))
                  .map((row) => (
                    <tr key={row.model.id} className='h-14 border-t'>
                      <td className='p-3'>
                        <Checkbox
                          aria-label={row.model.name}
                          disabled={!row.candidates.length || results[row.model.id] === 'success'}
                          checked={!!row.candidates.length && !excluded.includes(row.model.id)}
                          onCheckedChange={(checked) =>
                            setExcluded(checked ? excluded.filter((id) => id !== row.model.id) : [...excluded, row.model.id])
                          }
                        />
                      </td>
                      <td className='max-w-52 truncate pr-3'>
                        {row.model.name}
                        <div className='text-muted-foreground text-xs'>{row.model.modelID}</div>
                      </td>
                      <td>
                        <Input
                          aria-label={`${label('forward')} ${row.model.name}`}
                          value={names[row.model.id] ?? (row.model.matchType === 'exact' ? row.model.modelID : '')}
                          disabled={results[row.model.id] === 'success'}
                          placeholder={label('enterModel')}
                          onChange={(e) => setNames({ ...names, [row.model.id]: e.target.value })}
                        />
                      </td>
                      <td className='max-w-64 px-3'>
                        {results[row.model.id] === 'success'
                          ? label('saved')
                          : results[row.model.id] ||
                            (row.candidates.length
                              ? t('models.bulk.newCount', { count: row.candidates.length })
                              : row.duplicates
                                ? label('existing')
                                : row.modelID
                                  ? label('unmatched')
                                  : label('enterModel'))}
                        {row.duplicates > 0 && (
                          <div className='text-muted-foreground text-xs'>{t('models.bulk.duplicates', { count: row.duplicates })}</div>
                        )}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </fieldset>
        <p className='text-muted-foreground shrink-0 text-sm'>
          {t('models.bulk.summary', {
            ready: actionable.length,
            unmatched: rows.filter((row) => row.modelID && !row.candidates.length && !row.duplicates).length,
            pending: rows.filter((row) => !row.modelID).length,
          })}
        </p>
        <DialogFooter>
          <Button variant='outline' disabled={pending} onClick={() => setOpen(null)}>
            {t('common.buttons.close')}
          </Button>
          <Button disabled={pending || !actionable.length || !!channels.error} onClick={() => void save()}>
            {pending ? label('saving') : t('models.bulk.apply', { count: actionable.length })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
