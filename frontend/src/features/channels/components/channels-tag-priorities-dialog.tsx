import { useState } from 'react';
import { RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useChannels } from '../context/channels-context';
import { useChannelRoutingSummaries, useUpdateChannelTagPriorities, type ChannelTagPriorityUpdate } from '../data/channel-routing';

export function ChannelsTagPrioritiesDialog() {
  const { t } = useTranslation();
  const { open, setOpen, selectedTag } = useChannels();
  const query = useChannelRoutingSummaries(open === 'tagPriorities');
  const [tag, setTag] = useState(selectedTag.length === 1 ? selectedTag[0] : '');
  const [drafts, setDrafts] = useState<Record<string, { priority: string; weight: string }>>({});
  const [error, setError] = useState('');
  const [container, setContainer] = useState<HTMLDivElement | null>(null);
  const mutation = useUpdateChannelTagPriorities();
  const tags = [...new Set(query.data?.flatMap((channel) => channel.tags ?? []) ?? [])].sort();
  const activeTag = tag || tags[0] || '';
  const channels = query.data?.filter((channel) => channel.tags?.includes(activeTag)) ?? [];
  const getValues = (channel: (typeof channels)[number]) =>
    drafts[channel.id] ?? {
      priority: String(channel.settings?.tagPriorities.find((entry) => entry.tag === activeTag)?.priority ?? ''),
      weight: String(channel.orderingWeight),
    };
  const updateDraft = (channel: (typeof channels)[number], patch: Partial<{ priority: string; weight: string }>) => {
    setDrafts((previous) => ({ ...previous, [channel.id]: { ...getValues(channel), ...patch } }));
    setError('');
  };
  const save = async () => {
    const input: ChannelTagPriorityUpdate[] = [];
    for (const channel of channels) {
      const draft = drafts[channel.id];
      if (!draft) continue;
      const priority = draft.priority.trim() === '' ? null : Number(draft.priority);
      const weight = Number(draft.weight);
      if (priority != null && (!Number.isInteger(priority) || priority < -2147483648 || priority > 2147483647)) {
        setError(t('channels.routing.invalidPriority'));
        return;
      }
      if (!draft.weight.trim() || !Number.isInteger(weight) || weight < 0 || weight > 100) {
        setError(t('channels.routing.invalidWeight'));
        return;
      }
      const storedPriority = channel.settings?.tagPriorities.find((entry) => entry.tag === activeTag)?.priority ?? null;
      if (priority !== storedPriority || weight !== channel.orderingWeight)
        input.push({ channelId: channel.id, priority, orderingWeight: weight });
    }
    try {
      if (input.length) await mutation.mutateAsync({ tag: activeTag, input });
      setOpen(null);
    } catch {
      /* Keep unsaved rows visible; the mutation reports the error. */
    }
  };
  return (
    <Dialog
      open={open === 'tagPriorities'}
      onOpenChange={(next) => {
        if (!next && !mutation.isPending) setOpen(null);
      }}
    >
      <DialogContent ref={setContainer} className='flex max-h-[85vh] flex-col sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('channels.routing.title')}</DialogTitle>
          <DialogDescription>{t('channels.routing.description')}</DialogDescription>
        </DialogHeader>
        <Select
          value={activeTag}
          disabled={mutation.isPending}
          onValueChange={(next) => {
            setTag(next);
            setDrafts({});
            setError('');
          }}
        >
          <SelectTrigger className='w-full' aria-label={t('channels.routing.tag')}>
            <SelectValue placeholder={t('channels.routing.selectTag')} />
          </SelectTrigger>
          <SelectContent container={container}>
            <SelectGroup>
              {tags.map((item) => (
                <SelectItem key={item} value={item}>
                  {item}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <div className='min-h-0 overflow-auto rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('channels.routing.channel')}</TableHead>
                <TableHead>{t('channels.routing.priority')}</TableHead>
                <TableHead>{t('channels.routing.weight')}</TableHead>
                <TableHead className='text-right'>{t('channels.routing.restorePriority')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {channels.map((channel) => {
                const values = getValues(channel);
                return (
                  <TableRow key={channel.id}>
                    <TableCell className='max-w-52 break-words'>{channel.name}</TableCell>
                    <TableCell>
                      <Input
                        type='number'
                        step={1}
                        className='w-28'
                        placeholder={
                          channel.settings?.globalPriority == null
                            ? t('channels.routing.inherit')
                            : t('channels.routing.useGlobal', { priority: channel.settings.globalPriority })
                        }
                        value={values.priority}
                        aria-label={`${channel.name} ${t('channels.routing.priority')}`}
                        onChange={(event) => updateDraft(channel, { priority: event.target.value })}
                        disabled={mutation.isPending}
                      />
                    </TableCell>
                    <TableCell>
                      <Input
                        type='number'
                        step={1}
                        min={0}
                        max={100}
                        className='w-24'
                        value={values.weight}
                        aria-label={`${channel.name} ${t('channels.routing.weight')}`}
                        onChange={(event) => updateDraft(channel, { weight: event.target.value })}
                        disabled={mutation.isPending}
                      />
                    </TableCell>
                    <TableCell className='text-right'>
                      <Button
                        type='button'
                        variant='outline'
                        size='icon'
                        aria-label={`${t('channels.routing.restorePriority')} · ${channel.name}`}
                        onClick={() => updateDraft(channel, { priority: '' })}
                        disabled={mutation.isPending}
                      >
                        <RotateCcw />
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
              {channels.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className='text-muted-foreground py-8 text-center'>
                    {t(
                      query.isPending
                        ? 'common.loading'
                        : query.isError
                          ? 'channels.routing.loadFailed'
                          : 'channels.routing.noTaggedChannels'
                    )}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div className='text-muted-foreground flex flex-col gap-1 text-xs'>
          <p>{t('channels.routing.orderHint')}</p>
          <p>{t('channels.routing.scopeHint', { tag: activeTag || '—' })}</p>
          <p>{t('channels.routing.inheritHint')}</p>
        </div>
        {error && (
          <p className='text-destructive text-sm' role='alert'>
            {error}
          </p>
        )}
        <DialogFooter className='gap-2 sm:justify-between'>
          <Button
            type='button'
            variant='outline'
            disabled={mutation.isPending || channels.length === 0}
            onClick={() => setDrafts(Object.fromEntries(channels.map((channel) => [channel.id, { ...getValues(channel), priority: '' }])))}
          >
            {t('channels.routing.resetAll')}
          </Button>
          <div className='flex justify-end gap-2'>
            <Button type='button' variant='outline' disabled={mutation.isPending} onClick={() => setOpen(null)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button type='button' disabled={mutation.isPending || !channels.length || query.isError} onClick={save}>
              {t('common.buttons.save')}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
