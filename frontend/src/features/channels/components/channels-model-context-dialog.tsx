import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useDebounce } from '@/hooks/use-debounce';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { channelModelContextSchema, type ChannelModelContext } from '@/features/models/data/context-settings';
import { useUpdateChannelSettings } from '../data/channels';
import { type Channel } from '../data/schema';

export function ChannelsModelContextDialog({ channel, onClose }: { channel: Channel; onClose: () => void }) {
  const { t } = useTranslation();
  const save = useUpdateChannelSettings();
  const [draft, setDraft] = useState<Record<string, ChannelModelContext>>(() =>
    Object.fromEntries((channel.settings?.modelContexts ?? []).map((value) => [value.modelId, value]))
  );
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(0);
  const [error, setError] = useState('');
  const debouncedSearch = useDebounce(search, 250);
  const models = useMemo(() => [...new Set([...channel.supportedModels, ...Object.keys(draft)])], [channel.supportedModels, draft]);
  const filtered = useMemo(
    () => models.filter((id) => id.toLowerCase().includes(debouncedSearch.toLowerCase())),
    [models, debouncedSearch]
  );
  const currentPage = Math.min(page, Math.max(0, Math.ceil(filtered.length / 50) - 1));
  const visibleModels = filtered.slice(currentPage * 50, (currentPage + 1) * 50);
  const edit = (id: string, field: 'defaultContextWindow' | 'maxContextWindow', value: string) => {
    setDraft((current) => ({ ...current, [id]: { ...current[id], modelId: id, [field]: value === '' ? null : Number(value) } }));
    setError('');
  };
  const submit = async () => {
    const values = Object.values(draft).filter((value) => value.defaultContextWindow != null || value.maxContextWindow != null);
    if (values.some((value) => !channelModelContextSchema.safeParse(value).success)) {
      setError(t('modelContext.invalidValues'));
      return;
    }
    try {
      await save.mutateAsync({ id: channel.id, patch: { modelContexts: values } });
      toast.success(t('channels.messages.updateSuccess'));
      onClose();
    } catch {
      // The shared mutation reports the server error and preserves this draft.
    }
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) onClose();
      }}
    >
      <DialogContent className='flex max-h-[85vh] flex-col sm:max-w-5xl'>
        <DialogHeader className='text-left'>
          <DialogTitle>{t('modelContext.channelTitle')}</DialogTitle>
          <DialogDescription>{t('modelContext.channelDescription', { name: channel.name })}</DialogDescription>
        </DialogHeader>
        <p className='text-muted-foreground text-xs'>{t('modelContext.defaultFallback')}</p>
        <p className='text-muted-foreground text-xs'>{t('modelContext.requestInheritance')}</p>
        <Input
          aria-label={t('modelContext.search')}
          placeholder={t('modelContext.search')}
          value={search}
          onChange={(event) => {
            setSearch(event.target.value);
            setPage(0);
          }}
        />
        <div className='min-h-0 overflow-auto rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('modelContext.channelModel')}</TableHead>
                <TableHead>{t('modelContext.defaultContextWindow')}</TableHead>
                <TableHead>{t('modelContext.maxContextWindow')}</TableHead>
                <TableHead>{t('modelContext.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {visibleModels.map((id) => {
                const value = draft[id];
                return (
                  <TableRow key={id}>
                    <TableCell className='max-w-64 font-mono text-xs break-all'>{id}</TableCell>
                    {(['defaultContextWindow', 'maxContextWindow'] as const).map((field) => (
                      <TableCell key={field} className='min-w-48'>
                        <Input
                          type='number'
                          min={1}
                          max={2147483647}
                          step={1}
                          aria-label={`${id} ${t(`modelContext.${field}`)}`}
                          placeholder={t('modelContext.inherit')}
                          value={value?.[field] ?? ''}
                          disabled={save.isPending}
                          onChange={(event) => edit(id, field, event.target.value)}
                        />
                        <p className='text-muted-foreground mt-1 text-xs'>
                          {value?.[field] != null ? t('modelContext.overridden') : t('modelContext.requestModel')}
                        </p>
                      </TableCell>
                    ))}
                    <TableCell>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={save.isPending || (value?.defaultContextWindow == null && value?.maxContextWindow == null)}
                        onClick={() => setDraft((current) => ({ ...current, [id]: { modelId: id } }))}
                      >
                        {t('modelContext.restore')}
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
              {!visibleModels.length && (
                <TableRow>
                  <TableCell colSpan={4} className='text-muted-foreground text-center'>
                    {t('modelContext.empty')}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        {filtered.length > 50 && (
          <div className='flex items-center justify-end gap-2 text-xs'>
            <span>
              {currentPage + 1} / {Math.ceil(filtered.length / 50)}
            </span>
            <Button type='button' variant='outline' size='sm' disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>
              {t('modelContext.previous')}
            </Button>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={(currentPage + 1) * 50 >= filtered.length}
              onClick={() => setPage(currentPage + 1)}
            >
              {t('modelContext.next')}
            </Button>
          </div>
        )}
        {error && (
          <p role='alert' className='text-destructive text-xs'>
            {error}
          </p>
        )}
        <DialogFooter className='items-center border-t pt-4 sm:justify-between'>
          <p className='text-muted-foreground text-xs'>{t('modelContext.activation')}</p>
          <div className='flex gap-2'>
            <Button type='button' variant='outline' disabled={save.isPending} onClick={onClose}>
              {t('common.buttons.cancel')}
            </Button>
            <Button type='button' disabled={save.isPending} onClick={submit}>
              {t('common.buttons.save')}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
