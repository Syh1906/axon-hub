import { useMemo, useRef, type HTMLAttributes } from 'react';
import { Check, ChevronsUpDown, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { buildChannelSelectOptions, type SelectableChannel } from '../utils/channel-select-options';

type Props = Omit<HTMLAttributes<HTMLDivElement>, 'onChange'> & {
  value: number[];
  onChange: (ids: number[]) => void;
  channels: SelectableChannel[];
  placeholder: string;
};

export function ChannelMultiSelect({ value, onChange, channels, placeholder, className, ...props }: Props) {
  const { t } = useTranslation();
  const container = useRef<HTMLDivElement>(null);
  const options = useMemo(
    () => buildChannelSelectOptions(channels, (type) => t(`channels.types.${type}`, { defaultValue: type })),
    [channels, t]
  );
  const labels = useMemo(() => new Map(options.map((option) => [option.id, option.label])), [options]);
  const selected = new Set(value);

  return (
    <div {...props} ref={container} className={cn('flex min-w-0 flex-col gap-2', className)}>
      {value.length > 0 && (
        <div className='flex flex-wrap gap-1'>
          {value.map((id) => (
            <Badge key={id} variant='secondary' className='max-w-full gap-1'>
              <span className='truncate' title={labels.get(id) ?? `#${id}`}>
                {labels.get(id) ?? `#${id}`}
              </span>
              <button
                type='button'
                className='shrink-0 rounded-sm focus-visible:outline'
                aria-label={t('channels.selector.remove', { name: labels.get(id) ?? `#${id}` })}
                onClick={() => onChange(value.filter((item) => item !== id))}
              >
                <X className='size-3' />
              </button>
            </Badge>
          ))}
        </div>
      )}
      <Popover>
        <PopoverTrigger asChild>
          <Button type='button' variant='outline' className='w-full justify-between' aria-label={placeholder}>
            <span className='truncate'>{placeholder}</span>
            <ChevronsUpDown data-icon='inline-end' />
          </Button>
        </PopoverTrigger>
        <PopoverContent container={container.current ?? undefined} align='start' className='w-[var(--radix-popover-trigger-width)] p-0'>
          <Command>
            <CommandInput placeholder={t('channels.selector.search')} aria-label={t('channels.selector.search')} />
            <CommandList>
              <CommandEmpty>{t('common.noResultsFound')}</CommandEmpty>
              <CommandGroup>
                {options.map((option) => (
                  <CommandItem
                    key={option.id}
                    value={String(option.id)}
                    keywords={[option.label, ...option.keywords]}
                    data-checked={selected.has(option.id)}
                    onSelect={() => onChange(selected.has(option.id) ? value.filter((id) => id !== option.id) : [...value, option.id])}
                  >
                    <Check className={cn('shrink-0', !selected.has(option.id) && 'invisible')} />
                    <span className='min-w-0 break-words'>{option.label}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
    </div>
  );
}
