import { useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { isValidPriceMultiplier } from '@/features/models/data/pricing';
import { useUpdateChannelCostMultiplier } from '../data/channel-routing';
import { useUpdateChannel } from '../data/channels';
import type { Channel } from '../data/schema';

type Props = {
  channel: Channel;
  field: 'orderingWeight' | 'costMultiplier';
  canWrite: boolean;
  displayOverride?: string;
  upstreamFollow?: boolean;
};

export function ChannelRoutingValueCell({ channel, field, canWrite, displayOverride, upstreamFollow }: Props) {
  const { t } = useTranslation();
  const inputID = useId();
  const titleID = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const updateWeight = useUpdateChannel();
  const updateMultiplier = useUpdateChannelCostMultiplier();
  const pending = updateWeight.isPending || updateMultiplier.isPending;
  const current = field === 'orderingWeight' ? channel.orderingWeight : (channel.settings?.modelPriceMultiplier ?? '1');
  const display =
    displayOverride ?? (field === 'costMultiplier' ? `${current}×` : current == null ? t('channels.routing.inherit') : String(current));
  const label = t(`channels.routing.edit.${field}`);

  const changeOpen = (nextOpen: boolean) => {
    if (pending) return;
    if (nextOpen) {
      setValue(String(current));
      setError('');
    }
    setOpen(nextOpen);
  };
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    const number = Number(value);
    if (
      field === 'costMultiplier' ? !isValidPriceMultiplier(value) : !value.trim() || !Number.isInteger(number) || number < 0 || number > 100
    ) {
      setError(t(field === 'costMultiplier' ? 'channels.routing.invalidMultiplier' : 'channels.routing.invalidWeight'));
      return;
    }
    try {
      if (field === 'orderingWeight') await updateWeight.mutateAsync({ id: channel.id, input: { orderingWeight: number } });
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
      <PopoverContent
        align='center'
        aria-labelledby={titleID}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          inputRef.current?.focus();
          inputRef.current?.select();
        }}
      >
        <form onSubmit={save} className='flex flex-col gap-3'>
          <h4 id={titleID} className='font-medium'>
            {label}
          </h4>
          <Label htmlFor={inputID} className='sr-only'>
            {label}
          </Label>
          <Input
            ref={inputRef}
            id={inputID}
            type='number'
            step={field === 'costMultiplier' ? 'any' : 1}
            min={0}
            max={field === 'orderingWeight' ? 100 : undefined}
            value={value}
            onChange={(event) => {
              setValue(event.target.value);
              setError('');
            }}
            disabled={pending}
            aria-invalid={Boolean(error)}
          />
          <p className='text-muted-foreground text-xs'>
            {upstreamFollow ? t('channels.upstream.saveManualHint') : t(`channels.routing.hint.${field}`)}
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
            <Button type='submit' size='sm' disabled={pending}>
              {t(upstreamFollow ? 'channels.upstream.saveAsManual' : 'common.buttons.save')}
            </Button>
          </div>
        </form>
      </PopoverContent>
    </Popover>
  );
}
