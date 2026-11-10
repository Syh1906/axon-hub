import { useTranslation } from 'react-i18next';
import { extractNumberID } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import type { ConsumptionEntry } from '../data/consumption';

export function ChannelIdentity({ entry }: { entry: ConsumptionEntry }) {
  const { t } = useTranslation();
  const tags = entry.channelTags ?? [];
  const provider = entry.channelProvider
    ? t(`channels.providers.${entry.channelProvider}`, { defaultValue: entry.channelProvider })
    : '';

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type='button'
          className='mx-auto mt-0.5 flex h-4 max-w-full items-center justify-center gap-1 overflow-hidden rounded text-xs font-normal text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
          aria-label={t('consumption.channelDetails', { name: entry.name })}
        >
          {tags.slice(0, 2).map((tag) => (
            <Badge key={tag} variant='outline' className='max-w-20 truncate px-1 py-0 leading-4'>{tag}</Badge>
          ))}
          {tags.length > 2 && <span className='shrink-0'>+{tags.length - 2}</span>}
          {provider && <span className='shrink-0'>{tags.length > 0 ? '· ' : ''}{provider}</span>}
          {tags.length === 0 && !provider && <span>{t('consumption.channelDetailsShort')}</span>}
        </button>
      </PopoverTrigger>
      <PopoverContent className='flex max-h-72 flex-col gap-2 overflow-y-auto text-sm'>
        <p className='break-words font-medium'>{entry.name}</p>
        <p className='text-muted-foreground'>{provider} · #{extractNumberID(entry.id)}</p>
        <div className='flex flex-wrap gap-1'>
          {tags.map((tag) => <Badge key={tag} variant='outline' className='max-w-full whitespace-normal break-all'>{tag}</Badge>)}
        </div>
        {entry.channelRemark && <p className='whitespace-pre-wrap break-words text-muted-foreground'>{entry.channelRemark}</p>}
      </PopoverContent>
    </Popover>
  );
}
