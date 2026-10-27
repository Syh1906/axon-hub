import { RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { extractNumberIDAsNumber } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { type ChannelPriority } from '../data/schema';

type Props = {
  value?: ChannelPriority[] | null;
  onChange: (value: ChannelPriority[]) => void;
  channels: { id: string; name: string; tags?: string[] | null }[];
  allowedChannelIDs?: number[] | null;
  channelTags?: string[] | null;
  channelTagsMatchMode?: string | null;
};

export function ChannelPriorityEditor({ value, onChange, channels, allowedChannelIDs, channelTags, channelTagsMatchMode }: Props) {
  const { t } = useTranslation();
  const entries = value ?? [];
  const options = channels.map((channel) => ({ ...channel, numericID: extractNumberIDAsNumber(channel.id) }));
  const allowed = options.filter((channel) => {
    if (allowedChannelIDs?.length && !allowedChannelIDs.includes(channel.numericID)) return false;
    if (!channelTags?.length) return true;
    const matches = (tag: string) => channel.tags?.includes(tag);
    if (channelTagsMatchMode === 'all') return channelTags.every(matches);
    if (channelTagsMatchMode === 'none') return !channelTags.some(matches);
    return channelTags.some(matches);
  });
  const available = allowed.filter((channel) => !entries.some((entry) => entry.channelId === channel.numericID));
  return (
    <div className='space-y-3 border-t pt-4'>
      <div className='flex items-center justify-between gap-2'>
        <h4 className='text-sm font-medium'>{t('apikeys.priorities.title')}</h4>
        {entries.length > 0 && (
          <Button type='button' variant='ghost' size='sm' onClick={() => onChange([])}>
            {t('apikeys.priorities.resetAll')}
          </Button>
        )}
      </div>
      <p className='text-muted-foreground text-xs'>{t('apikeys.priorities.description')}</p>
      {entries.map((entry, index) => {
        const selected = options.find((channel) => channel.numericID === entry.channelId);
        const inScope = allowed.some((channel) => channel.numericID === entry.channelId);
        return (
          <div key={entry.channelId} className='space-y-1'>
            <div className='flex items-center gap-2'>
              <div className='min-w-0 flex-1 truncate rounded-md border px-3 py-2 text-sm'>
                {selected?.name ?? t('apikeys.priorities.channelId', { id: entry.channelId })}
              </div>
              <Input
                type='number'
                step='1'
                className='w-24'
                value={entry.priority}
                aria-label={t('apikeys.priorities.value', { channel: selected?.name ?? entry.channelId })}
                onChange={(event) =>
                  onChange(entries.map((item, position) => (position === index ? { ...item, priority: Number(event.target.value) } : item)))
                }
              />
              <Button
                type='button'
                variant='ghost'
                size='icon'
                title={t('apikeys.priorities.reset')}
                aria-label={t('apikeys.priorities.reset')}
                onClick={() => onChange(entries.filter((_, position) => position !== index))}
              >
                <RotateCcw className='h-4 w-4' />
              </Button>
            </div>
            {!inScope && <p className='text-muted-foreground text-xs'>{t('apikeys.priorities.outOfScope')}</p>}
          </div>
        );
      })}
      <Select
        value=''
        onValueChange={(id) =>
          onChange([
            ...entries,
            { channelId: Number(id), priority: entries.length ? Math.max(...entries.map((entry) => entry.priority)) + 10 : 0 },
          ])
        }
      >
        <SelectTrigger aria-label={t('apikeys.priorities.add')}>
          <SelectValue placeholder={t('apikeys.priorities.add')} />
        </SelectTrigger>
        <SelectContent>
          {available.map((channel) => (
            <SelectItem key={channel.id} value={String(channel.numericID)}>
              {channel.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
