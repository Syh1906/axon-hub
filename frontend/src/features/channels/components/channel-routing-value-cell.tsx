import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { isValidPriceMultiplier } from '@/features/models/data/pricing';
import { useUpdateChannelCostMultiplier, useUpdateChannelTagPriorities } from '../data/channel-routing';
import { useUpdateChannel } from '../data/channels';
import type { Channel } from '../data/schema';
import { channelDisplayPriority } from '../utils/channel-priority';

type Props = { channel: Channel; field: 'priority' | 'orderingWeight' | 'costMultiplier'; tag: string; canWrite: boolean };

export function ChannelRoutingValueCell({ channel, field, tag, canWrite }: Props) {
  const { t } = useTranslation();
  const inputID = useId();
  const titleID = useId();
  const [open, setOpen] = useState(false);
  const [selectedTag, setSelectedTag] = useState(tag);
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const [container, setContainer] = useState<HTMLDivElement | null>(null);
  const updatePriority = useUpdateChannelTagPriorities();
  const updateWeight = useUpdateChannel();
  const updateMultiplier = useUpdateChannelCostMultiplier();
  const pending = updatePriority.isPending || updateWeight.isPending || updateMultiplier.isPending;
  const priorityFor = (selected: string) => channel.settings?.tagPriorities?.find((entry) => entry.tag === selected)?.priority;
  const displayPriority = channelDisplayPriority(channel, tag);
  const current =
    field === 'priority'
      ? displayPriority.priority
      : field === 'orderingWeight'
        ? channel.orderingWeight
        : (channel.settings?.modelPriceMultiplier ?? '1');
  const display = field === 'costMultiplier' ? `${current}x` : current == null ? t('channels.routing.inherit') : String(current);
  const label = t(`channels.routing.edit.${field}`);

  const changeTag = (nextTag: string) => {
    setSelectedTag(nextTag);
    setValue(String(priorityFor(nextTag) ?? ''));
    setError('');
  };
  const changeOpen = (nextOpen: boolean) => {
    if (pending) return;
    if (nextOpen) {
      const nextTag = displayPriority.tag;
      setSelectedTag(nextTag);
      setValue(String(field === 'priority' ? (priorityFor(nextTag) ?? '') : (current ?? '')));
      setError('');
    }
    setOpen(nextOpen);
  };
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    const number = Number(value);
    if (field === 'priority' && !selectedTag) {
      setError(t('channels.routing.selectTag'));
      return;
    }
    if (
      field === 'costMultiplier'
        ? !isValidPriceMultiplier(value)
        : (field === 'orderingWeight' || value.trim() !== '') &&
          (!value.trim() ||
            !Number.isInteger(number) ||
            number < (field === 'orderingWeight' ? 0 : -2147483648) ||
            number > (field === 'orderingWeight' ? 100 : 2147483647))
    ) {
      setError(
        t(
          field === 'costMultiplier'
            ? 'channels.routing.invalidMultiplier'
            : field === 'priority'
              ? 'channels.routing.invalidPriority'
              : 'channels.routing.invalidWeight'
        )
      );
      return;
    }
    try {
      if (field === 'priority')
        await updatePriority.mutateAsync({
          tag: selectedTag,
          input: [{ channelId: channel.id, priority: value.trim() === '' ? null : number }],
        });
      else if (field === 'orderingWeight') await updateWeight.mutateAsync({ id: channel.id, input: { orderingWeight: number } });
      else await updateMultiplier.mutateAsync({ channelId: channel.id, multiplier: value });
      setOpen(false);
    } catch {
      // The mutation reports errors; keep the editor and its draft open.
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
      <PopoverContent ref={setContainer} align='center' aria-labelledby={titleID}>
        <form onSubmit={save} className='flex flex-col gap-3'>
          <h4 id={titleID} className='font-medium'>
            {label}
          </h4>
          {field === 'priority' && (
            <Select value={selectedTag} onValueChange={changeTag} disabled={pending || Boolean(tag)}>
              <SelectTrigger className='w-full' aria-label={t('channels.routing.tag')}>
                <SelectValue placeholder={t('channels.routing.selectTag')} />
              </SelectTrigger>
              <SelectContent container={container}>
                <SelectGroup>
                  {(channel.tags ?? []).map((item) => (
                    <SelectItem key={item} value={item}>
                      {item}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          )}
          <Label htmlFor={inputID} className='sr-only'>
            {label}
          </Label>
          <Input
            id={inputID}
            type='number'
            step={field === 'costMultiplier' ? 'any' : 1}
            min={field === 'priority' ? -2147483648 : 0}
            max={field === 'orderingWeight' ? 100 : field === 'priority' ? 2147483647 : undefined}
            value={value}
            onChange={(event) => {
              setValue(event.target.value);
              setError('');
            }}
            disabled={pending}
            placeholder={field === 'priority' ? t('channels.routing.inherit') : undefined}
            aria-invalid={Boolean(error)}
          />
          <p className='text-muted-foreground text-xs'>
            {t(`channels.routing.hint.${field}`, { tag: selectedTag || t('channels.routing.selectTag') })}
          </p>
          {error && (
            <p role='alert' className='text-destructive text-xs'>
              {error}
            </p>
          )}
          <div className='flex justify-end gap-2'>
            <Button type='button' variant='outline' size='sm' onClick={() => changeOpen(false)} disabled={pending}>
              {t('common.buttons.cancel')}
            </Button>
            <Button type='submit' size='sm' disabled={pending || (field === 'priority' && !selectedTag)}>
              {t('common.buttons.save')}
            </Button>
          </div>
        </form>
      </PopoverContent>
    </Popover>
  );
}
