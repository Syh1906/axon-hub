import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { graphqlRequest } from '@/gql/graphql';
import { extractNumberIDAsNumber } from '@/lib/utils';
import { AutoCompleteSelect } from '@/components/auto-complete-select';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { readBackupChannelReferences, backupChannelSourceKey, channelMappingSourceKey, type ChannelRestoreMapping, type BackupChannelReference } from '../data/backup-channel-mapping';

const targetsSchema = z.array(z.object({ id: z.string(), name: z.string(), type: z.string(), tags: z.array(z.string()).nullable() }));

export function RestoreChannelMapping({ file, value, onChange, includeChannels }: {
  file: File;
  value: ChannelRestoreMapping[];
  onChange: (value: ChannelRestoreMapping[]) => void;
  includeChannels: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const loadedFile = useRef<File | null>(null);
  const [sources, setSources] = useState<BackupChannelReference[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const [source, setSource] = useState('');
  const [target, setTarget] = useState('');
  const targets = useQuery({
    queryKey: ['channels', 'restore-targets'],
    enabled: open,
    queryFn: async ({ signal }) => {
      const data = await graphqlRequest<{ allChannelSummarys: unknown }>(
        'query RestoreChannelTargets { allChannelSummarys(includeArchived: true) { id name type tags } }',
        {}, undefined, { signal },
      );
      return targetsSchema.parse(data.allChannelSummarys);
    },
    staleTime: 60_000,
  });
  useEffect(() => {
    if (!open || loadedFile.current === file) return;
    let active = true;
    setLoading(true);
    setError(false);
    readBackupChannelReferences(file).then(
      (refs) => { if (active) { loadedFile.current = file; setSources(refs); } },
      () => { if (active) setError(true); },
    ).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [file, open]);
  const sourceOptions = useMemo(() => sources.map((ch) => ({
    value: backupChannelSourceKey(ch), label: [ch.name, ...ch.tags, ch.type, ch.id > 0 ? `#${ch.id}` : ''].filter(Boolean).join(' · '),
  })), [sources]);
  const sourceByKey = useMemo(() => new Map(sources.map((item) => [backupChannelSourceKey(item), item])), [sources]);
  const selectedSource = sourceByKey.get(source);
  const canCreate = includeChannels && selectedSource?.has_config === true;
  const targetOptions = useMemo(() => [
    ...(canCreate ? [{ value: '0', label: t('system.restore.channelMapping.newChannel') }] : []),
    ...(targets.data ?? []).map((ch) => ({
      value: String(extractNumberIDAsNumber(ch.id)), label: [ch.name, ...(ch.tags ?? []), ch.type, `#${extractNumberIDAsNumber(ch.id)}`].filter(Boolean).join(' · '),
    })),
  ], [targets.data, canCreate, t]);
  const sourceLabels = useMemo(() => new Map(sourceOptions.map((item) => [item.value, item.label])), [sourceOptions]);
  const targetLabels = useMemo(() => new Map(targetOptions.map((item) => [Number(item.value), item.label])), [targetOptions]);

  return (
    <div className='flex flex-col gap-3'>
      <p className='text-sm text-muted-foreground'>{t('system.restore.channelMapping.help')}</p>
      <Button type='button' variant='outline' className='self-start' onClick={() => setOpen(!open)} aria-expanded={open}>
        {t('system.restore.channelMapping.title')}
      </Button>
      {open && (
        <div className='flex flex-col gap-3 rounded-md border p-3'>
          {(error || targets.isError) && <Alert variant='destructive'><AlertDescription>{t('system.restore.channelMapping.readError')}</AlertDescription></Alert>}
          <Label id='restore-source-label' htmlFor='restore-source-channel'>{t('system.restore.channelMapping.source')}</Label>
          <AutoCompleteSelect inputProps={{ id: 'restore-source-channel', 'aria-labelledby': 'restore-source-label' }} selectedValue={source} onSelectedValueChange={setSource} items={sourceOptions} emptyMessage={t('common.noResultsFound')} isLoading={loading} placeholder={t('system.restore.channelMapping.source')} />
          <Label id='restore-target-label' htmlFor='restore-target-channel'>{t('system.restore.channelMapping.target')}</Label>
          <AutoCompleteSelect inputProps={{ id: 'restore-target-channel', 'aria-labelledby': 'restore-target-label' }} selectedValue={target} onSelectedValueChange={setTarget} items={targetOptions} emptyMessage={t('common.noResultsFound')} isLoading={targets.isLoading} placeholder={t('system.restore.channelMapping.target')} />
          <Button type='button' variant='secondary' className='self-start' disabled={!selectedSource || !target || (!canCreate && target === '0')} onClick={() => {
            if (!selectedSource || (!canCreate && target === '0')) return;
            onChange([...value.filter((m) => channelMappingSourceKey(m) !== source), {
              sourceID: selectedSource.id,
              ...(selectedSource.id === 0 ? { sourceName: selectedSource.name } : {}),
              targetID: Number(target),
            }]);
            setSource(''); setTarget('');
          }}>{t('system.restore.channelMapping.assign')}</Button>
          <div className='flex max-h-56 flex-col gap-2 overflow-y-auto'>
            {value.map((mapping) => (
              <div key={channelMappingSourceKey(mapping)} className='flex items-center justify-between gap-2 text-sm'>
                <p className='min-w-0 break-words'>{sourceLabels.get(channelMappingSourceKey(mapping)) ?? mapping.sourceName ?? `#${mapping.sourceID}`} → {mapping.targetID === 0 ? t('system.restore.channelMapping.newChannel') : targetLabels.get(mapping.targetID) ?? `#${mapping.targetID}`}</p>
                <Button type='button' variant='ghost' size='sm' onClick={() => onChange(value.filter((m) => channelMappingSourceKey(m) !== channelMappingSourceKey(mapping)))}>{t('system.restore.channelMapping.remove')}</Button>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
