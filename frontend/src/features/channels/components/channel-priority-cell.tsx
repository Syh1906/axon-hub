import { useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useUpdateChannelPriorities, type ChannelPrioritiesInput } from '../data/channel-routing';
import type { Channel } from '../data/schema';
import { channelDisplayPriority, parseChannelPriority } from '../utils/channel-priority';

type Props = { channel: Channel; tag: string; canWrite: boolean };

export function ChannelPriorityCell({ channel, tag, canWrite }: Props) {
  const { t } = useTranslation();
  const id = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [globalDraft, setGlobalDraft] = useState('');
  const [tagDrafts, setTagDrafts] = useState<{ tag: string; value: string }[]>([]);
  const update = useUpdateChannelPriorities();
  const label = t('channels.routing.edit.priority');
  const priority = channelDisplayPriority(channel, tag).priority;
  const display = priority == null ? t('channels.routing.inherit') : String(priority);
  const globalPriority = parseChannelPriority(globalDraft);
  const invalid = globalPriority === undefined || tagDrafts.some((draft) => parseChannelPriority(draft.value) === undefined);
  const inherited =
    globalPriority == null ? t('channels.routing.inheritModel') : t('channels.routing.useGlobal', { priority: globalPriority });

  const changeOpen = (next: boolean) => {
    if (update.isPending) return;
    if (next) {
      setGlobalDraft(String(channel.settings?.globalPriority ?? ''));
      setTagDrafts(
        (channel.tags ?? []).map((item) => ({
          tag: item,
          value: String(channel.settings?.tagPriorities?.find((entry) => entry.tag === item)?.priority ?? ''),
        }))
      );
    }
    setOpen(next);
  };
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (update.isPending || invalid || globalPriority === undefined) return;
    const input: ChannelPrioritiesInput = { globalPriority, tagPriorities: [] };
    for (const draft of tagDrafts) {
      const value = parseChannelPriority(draft.value);
      if (value != null) input.tagPriorities.push({ tag: draft.tag, priority: value });
    }
    try {
      await update.mutateAsync({ channelId: channel.id, input });
      setOpen(false);
    } catch {
      // The mutation reports errors; preserve all drafts for retry.
    }
  };

  if (!canWrite) return <span className='font-mono text-sm'>{display}</span>;
  return (
    <Popover open={open} onOpenChange={changeOpen}>
      <PopoverTrigger asChild>
        <Button
          type='button'
          variant='secondary'
          size='sm'
          className='h-8 min-w-16 px-2 font-mono font-normal'
          aria-label={`${label} · ${channel.name}: ${display}`}
        >
          {display}
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align='center'
        side='bottom'
        sideOffset={6}
        collisionPadding={12}
        className='flex w-80 max-w-[calc(100vw-24px)] flex-col'
        aria-labelledby={`${id}-title`}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          inputRef.current?.focus();
          inputRef.current?.select();
        }}
      >
        <form
          onSubmit={save}
          className='flex max-h-[min(32rem,calc(var(--radix-popover-content-available-height)-2rem))] flex-col gap-3'
        >
          <h4 id={`${id}-title`} className='shrink-0 font-medium'>
            {label}
          </h4>
          <div className='flex min-h-0 flex-col gap-4 overflow-y-auto'>
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor={`${id}-global`}>{t('channels.routing.globalPriority')}</Label>
              <Input
                ref={inputRef}
                id={`${id}-global`}
                type='number'
                step={1}
                min={-2147483648}
                max={2147483647}
                value={globalDraft}
                onChange={(event) => setGlobalDraft(event.target.value)}
                disabled={update.isPending}
                placeholder={t('channels.routing.inheritModel')}
                aria-invalid={globalPriority === undefined}
              />
            </div>
            {tagDrafts.length > 0 && (
              <fieldset className='flex min-w-0 flex-col gap-2'>
                <legend className='mb-2 text-sm font-medium'>{t('channels.routing.tagPriorities')}</legend>
                {tagDrafts.map((draft, index) => (
                  <div key={draft.tag} className='grid grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)] items-center gap-2'>
                    <Label htmlFor={`${id}-tag-${index}`} className='block truncate' title={draft.tag}>
                      {draft.tag}
                    </Label>
                    <Input
                      id={`${id}-tag-${index}`}
                      type='number'
                      step={1}
                      min={-2147483648}
                      max={2147483647}
                      value={draft.value}
                      disabled={update.isPending}
                      placeholder={inherited}
                      aria-invalid={parseChannelPriority(draft.value) === undefined}
                      onChange={(event) =>
                        setTagDrafts((previous) => previous.map((item, i) => (i === index ? { ...item, value: event.target.value } : item)))
                      }
                    />
                  </div>
                ))}
              </fieldset>
            )}
            <div className='text-muted-foreground flex flex-col gap-1 text-xs'>
              <p>{t('channels.routing.tagPriorityHint')}</p>
              <p>{t('channels.routing.globalPriorityHint')}</p>
            </div>
            {invalid && (
              <p role='alert' className='text-destructive text-xs'>
                {t('channels.routing.invalidPriority')}
              </p>
            )}
          </div>
          <div className='flex shrink-0 justify-end gap-2'>
            <Button type='button' variant='outline' size='sm' disabled={update.isPending} onClick={() => changeOpen(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button type='submit' size='sm' disabled={update.isPending || invalid}>
              {t('common.buttons.save')}
            </Button>
          </div>
        </form>
      </PopoverContent>
    </Popover>
  );
}
