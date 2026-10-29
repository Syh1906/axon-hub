import { useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useUpdateKeySettings } from '../data/channel-accounts';
import type { ChannelUpstreamState } from '../data/schema';

type Props = {
  channelId: string;
  item: ChannelUpstreamState['keys'][number];
  container: HTMLElement | null;
  disabled: boolean;
};

export function ChannelKeyPriorityCell({ channelId, item, container, disabled }: Props) {
  const { t } = useTranslation();
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState('');
  const [error, setError] = useState(false);
  const update = useUpdateKeySettings(channelId);
  const label = t('channels.accounts.editPriority');
  const changeOpen = (next: boolean) => {
    if (update.isPending) return;
    if (next) {
      setValue(String(item.priority));
      setError(false);
    }
    setOpen(next);
  };
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    const priority = Number(value);
    if (!value.trim() || !Number.isInteger(priority) || priority < -2147483648 || priority > 2147483647) {
      setError(true);
      return;
    }
    try {
      await update.mutateAsync({ keyRef: item.keyRef, remark: item.remark, accountRef: item.accountRef, priority });
      setOpen(false);
    } catch {
      /* The mutation reports the error; keep the draft open. */
    }
  };
  return (
    <Popover open={open} onOpenChange={changeOpen}>
      <PopoverTrigger asChild>
        <Button
          type='button'
          variant='secondary'
          size='sm'
          className='h-8 min-w-14 px-2 font-mono font-normal'
          disabled={disabled || update.isPending}
          aria-label={`${label} · ${item.remark || item.label}: ${item.priority}`}
        >
          {item.priority}
        </Button>
      </PopoverTrigger>
      <PopoverContent
        container={container}
        className='w-64'
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          input.current?.focus();
          input.current?.select();
        }}
      >
        <form onSubmit={save} className='flex flex-col gap-3'>
          <Label htmlFor={id}>{label}</Label>
          <Input
            id={id}
            ref={input}
            type='number'
            step={1}
            min={-2147483648}
            max={2147483647}
            value={value}
            disabled={update.isPending}
            aria-invalid={error}
            onChange={(event) => {
              setValue(event.target.value);
              setError(false);
            }}
          />
          <p className='text-muted-foreground text-xs'>{t('channels.accounts.priorityHint')}</p>
          {error && (
            <p role='alert' className='text-destructive text-xs'>
              {t('channels.routing.invalidPriority')}
            </p>
          )}
          <div className='flex justify-end gap-2'>
            <Button type='button' variant='outline' size='sm' disabled={update.isPending} onClick={() => changeOpen(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button type='submit' size='sm' disabled={update.isPending}>
              {t('common.buttons.save')}
            </Button>
          </div>
        </form>
      </PopoverContent>
    </Popover>
  );
}
